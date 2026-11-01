const { app, BrowserWindow, dialog, ipcMain, session, shell } = require("electron");
const crypto = require("node:crypto");
const fs = require("node:fs");
const fsp = require("node:fs/promises");
const http = require("node:http");
const https = require("node:https");
const path = require("node:path");
const { planSync } = require("./sync-engine.cjs");
const { validateProfile } = require("./profile-validation.cjs");

const profilePath = () => path.join(app.getPath("userData"), "profiles.json");
const statePath = (id) => path.join(app.getPath("userData"), "sync-state", `${id}.json`);
const recoveryPath = (id) => path.join(app.getPath("userData"), "recovery", id);
const csrfTokens = new Map();
const runningProfiles = new Set();
const profileSessions = new Map();
let mainWindow;

function emit(profileId, message) {
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send("sync:event", { profileId, message });
  }
}

function sessionFor(profile) {
  const key = `${profile.serverUrl}|${profile.username}`;
  if (!profileSessions.has(key)) {
    const partition = crypto.createHash("sha256").update(key).digest("hex").slice(0, 24);
    profileSessions.set(key, session.fromPartition(`persist:asns-${partition}`));
  }
  return profileSessions.get(key);
}

function apiURL(profile, route) {
  return new URL(route, `${profile.serverUrl}/`);
}

async function cookiesFor(profile, target) {
  const cookies = await sessionFor(profile).cookies.get({ url: target.origin });
  return cookies.map((cookie) => `${cookie.name}=${cookie.value}`).join("; ");
}

function httpRequest(target, method, headers = {}, body = null, streamPath = null) {
  return new Promise((resolve, reject) => {
    const transport = target.protocol === "https:" ? https : http;
    const request = transport.request(target, { method, headers }, (response) => {
      const chunks = [];
      let total = 0;
      response.on("data", (chunk) => {
        total += chunk.length;
        if (total > 32 * 1024 * 1024) {
          request.destroy(new Error("ASNS API response exceeded 32 MiB"));
          return;
        }
        chunks.push(chunk);
      });
      response.on("end", () => resolve({
        statusCode: response.statusCode || 0,
        headers: response.headers,
        body: Buffer.concat(chunks),
      }));
      response.on("error", reject);
    });
    request.setTimeout(30 * 60 * 1000, () => request.destroy(new Error("ASNS request timed out")));
    request.on("error", (error) => {
      if (target.protocol === "https:" &&
          (error.code === "EPROTO" || error.code === "ERR_SSL_WRONG_VERSION_NUMBER") &&
          /WRONG_VERSION_NUMBER/i.test(error.message)) {
        reject(new Error("HTTPS handshake failed. Standard ASNS installs use http://<nas-ip>:8080; use https:// only when TLS is configured."));
        return;
      }
      reject(error);
    });
    if (streamPath) {
      const stream = fs.createReadStream(streamPath);
      stream.on("error", (error) => request.destroy(error));
      stream.on("end", () => request.end());
      stream.pipe(request, { end: false });
    } else {
      request.end(body || undefined);
    }
  });
}

async function apiRequest(profile, method, route, body, allowStatuses = []) {
  const target = apiURL(profile, route);
  const headers = { Accept: "application/json" };
  const cookie = await cookiesFor(profile, target);
  if (cookie) headers.Cookie = cookie;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (method !== "GET" && method !== "HEAD" && csrfTokens.has(profile.id)) {
    headers["X-CSRF-Token"] = csrfTokens.get(profile.id);
  }
  const response = await httpRequest(target, method, headers, body === undefined ? null : Buffer.from(JSON.stringify(body)));
  if (response.statusCode < 200 || response.statusCode >= 300) {
    let message = `ASNS returned HTTP ${response.statusCode}`;
    try {
      message = JSON.parse(response.body.toString("utf8")).error || message;
    } catch {}
    if (allowStatuses.includes(response.statusCode)) return { statusCode: response.statusCode, data: null };
    const error = new Error(message);
    error.statusCode = response.statusCode;
    throw error;
  }
  if (response.statusCode === 204 || response.body.length === 0) return { statusCode: response.statusCode, data: null };
  try {
    return { statusCode: response.statusCode, data: JSON.parse(response.body.toString("utf8")) };
  } catch {
    throw new Error("ASNS returned an invalid response");
  }
}

async function login(profile, username, password) {
  const checked = validateProfile({ ...profile, username }, { requireLocalFolder: false });
  if (typeof password !== "string" || password.length === 0 || password.length > 1024) throw new Error("Enter your ASNS password");
  const target = apiURL(checked, "/api/v1/auth/login");
  const response = await httpRequest(target, "POST", {
    Accept: "application/json",
    "Content-Type": "application/json",
    "Content-Length": Buffer.byteLength(JSON.stringify({ username, password })),
  }, Buffer.from(JSON.stringify({ username, password })));
  if (response.statusCode !== 200) {
    let message = `Sign-in failed (HTTP ${response.statusCode})`;
    try {
      message = JSON.parse(response.body.toString("utf8")).error || message;
    } catch {}
    throw new Error(message);
  }
  const cookieHeaders = response.headers["set-cookie"] || [];
  const cookieLine = cookieHeaders.find((line) => line.startsWith("asns_session="));
  if (!cookieLine) throw new Error("ASNS did not issue a session cookie");
  const [pair, ...attributes] = cookieLine.split(";");
  const separator = pair.indexOf("=");
  const cookie = {
    url: checked.serverUrl,
    name: pair.slice(0, separator),
    value: pair.slice(separator + 1),
    path: "/",
    httpOnly: true,
    secure: checked.serverUrl.startsWith("https:"),
    sameSite: "strict",
    expirationDate: Date.now() / 1000 + 12 * 60 * 60,
  };
  for (const attribute of attributes) {
    const [key, value] = attribute.trim().split("=", 2);
    if (key.toLowerCase() === "path" && value) cookie.path = value;
    if (key.toLowerCase() === "max-age" && Number.isFinite(Number(value))) {
      cookie.expirationDate = Date.now() / 1000 + Number(value);
    }
  }
  await sessionFor(checked).cookies.set(cookie);
  const data = JSON.parse(response.body.toString("utf8"));
  csrfTokens.set(checked.id, data.csrfToken);
  return { username: data.username };
}

async function authStatus(profile) {
  const checked = validateProfile(profile, { requireLocalFolder: false });
  const result = await apiRequest(checked, "GET", "/api/v1/auth/me");
  csrfTokens.set(checked.id, result.data.csrfToken);
  return { username: result.data.username };
}

function globRegex(pattern) {
  let result = "^";
  const value = pattern.replaceAll("\\", "/").replace(/^\/+/, "");
  for (let index = 0; index < value.length; index += 1) {
    const character = value[index];
    if (character === "*" && value[index + 1] === "*") {
      index += 1;
      if (value[index + 1] === "/") {
        index += 1;
        result += "(?:.*/)?";
      } else {
        result += ".*";
      }
    } else if (character === "*") {
      result += "[^/]*";
    } else if (character === "?") {
      result += "[^/]";
    } else {
      result += character.replace(/[|\\{}()[\]^$+?.]/g, "\\$&");
    }
  }
  return new RegExp(`${result}$`);
}

function pathIncluded(relative, profile) {
  const normalized = relative.split(path.sep).join("/");
  if (normalized.split("/").some((part) => part === ".asns-conflicts" || part === ".asns-deleted")) return false;
  if (profile.excludePatterns.some((pattern) => {
    const matcher = globRegex(pattern);
    return matcher.test(normalized) || matcher.test(`${normalized}/__asns_probe__`);
  })) return false;
  if (profile.includePatterns.length && !profile.includePatterns.some((pattern) => globRegex(pattern).test(normalized))) {
    return false;
  }
  return true;
}

async function localManifest(root, profile) {
  const files = [];
  const allDirectories = [];
  async function visit(directory) {
    const directoryInfo = await fsp.lstat(directory);
    if (directoryInfo.isSymbolicLink() || !directoryInfo.isDirectory()) {
      throw new Error(`Selected folder contains a non-directory sync path: ${directory}`);
    }
    const entries = await fsp.readdir(directory, { withFileTypes: true });
    for (const entry of entries) {
      if (files.length + allDirectories.length >= 100000) {
        throw new Error("Local sync manifest exceeds the 100000-entry limit");
      }
      const absolute = path.join(directory, entry.name);
      const relative = path.relative(root, absolute);
      const info = await fsp.lstat(absolute);
      if (entry.isSymbolicLink() || info.isSymbolicLink()) continue;
      if (!pathIncluded(relative, { ...profile, includePatterns: [] })) continue;
      if (info.isDirectory()) {
        allDirectories.push(relative);
        await visit(absolute);
      } else if (info.isFile() && pathIncluded(relative, profile)) {
        const sha256 = await hashFile(absolute);
        const afterHash = await fsp.stat(absolute);
        if (info.size !== afterHash.size || info.mtimeMs !== afterHash.mtimeMs ||
            info.ino !== afterHash.ino || info.dev !== afterHash.dev) {
          throw new Error(`File changed while it was being scanned: ${relative}`);
        }
        files.push({ path: relative.split(path.sep).join("/"), sizeBytes: info.size, sha256 });
      }
    }
  }
  await visit(root);
  files.sort((left, right) => left.path.localeCompare(right.path));
  const directories = allDirectories.filter((directory) => {
    const normalized = directory.split(path.sep).join("/");
    const hasIncludedFile = files.some((file) => file.path.startsWith(`${normalized}/`));
    const explicitlyIncluded = !profile.includePatterns.length ||
      profile.includePatterns.some((pattern) => globRegex(pattern).test(normalized) ||
        globRegex(pattern).test(`${normalized}/__asns_probe__`));
    return hasIncludedFile || explicitlyIncluded;
  });
  directories.sort((left, right) => left.split(path.sep).length - right.split(path.sep).length);
  return { files, directories };
}

function hashFile(filePath) {
  return new Promise((resolve, reject) => {
    const hash = crypto.createHash("sha256");
    const stream = fs.createReadStream(filePath);
    stream.on("data", (chunk) => hash.update(chunk));
    stream.on("error", reject);
    stream.on("end", () => resolve(hash.digest("hex")));
  });
}

async function readState(profile) {
  try {
    const value = JSON.parse(await fsp.readFile(statePath(profile.id), "utf8"));
    return value && typeof value === "object" ? value : {};
  } catch (error) {
    if (error.code === "ENOENT") return {};
    throw new Error(`Cannot read sync history: ${error.message}`);
  }
}

async function writeState(profile, state) {
  const destination = statePath(profile.id);
  await fsp.mkdir(path.dirname(destination), { recursive: true, mode: 0o700 });
  const temporary = `${destination}.tmp`;
  await fsp.writeFile(temporary, JSON.stringify(state, null, 2), { mode: 0o600 });
  await fsp.rename(temporary, destination);
}

function validateRelative(relative) {
  if (!relative || relative.includes("\\") || relative.startsWith("/") ||
      relative.split("/").some((part) => part === "" || part === "." || part === "..")) {
    throw new Error("The server returned an unsafe file path");
  }
}

function localPath(root, relative) {
  validateRelative(relative);
  const resolvedRoot = path.resolve(root);
  const target = path.resolve(resolvedRoot, ...relative.split("/"));
  if (!target.startsWith(`${resolvedRoot}${path.sep}`)) throw new Error("File path escaped the selected folder");
  return target;
}

async function ensureLocalParents(root, relative) {
  const destination = localPath(root, relative);
  const parent = path.dirname(destination);
  const relativeParent = path.relative(root, parent);
  let current = path.resolve(root);
  for (const part of relativeParent.split(path.sep).filter(Boolean)) {
    current = path.join(current, part);
    try {
      const info = await fsp.lstat(current);
      if (info.isSymbolicLink() || !info.isDirectory()) throw new Error("A sync destination parent is not a real folder");
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
      await fsp.mkdir(current, { mode: 0o700 });
    }
  }
  return destination;
}

function remotePath(profile, relative) {
  validateRelative(relative);
  return profile.remoteFolder ? `${profile.remoteFolder}/${relative}` : relative;
}

async function remoteManifest(profile) {
  const query = new URLSearchParams();
  if (profile.remoteFolder) query.set("directory", profile.remoteFolder);
  const result = await apiRequest(profile, "GET", `/api/v1/sync/manifest?${query}`);
  return (result.data.entries || []).flatMap((entry) => {
    if (entry.directory) return [];
    const relative = profile.remoteFolder ? path.posix.relative(profile.remoteFolder, entry.path) : entry.path;
    if (!relative || relative.startsWith("../") || relative === "..") throw new Error("The server returned a file outside the selected NAS folder");
    validateRelative(relative);
    return [{ path: relative, sizeBytes: entry.sizeBytes, sha256: entry.sha256 }];
  });
}

async function ensureRemoteDirectory(profile, directory) {
  const parts = directory.split("/").filter(Boolean);
  let current = "";
  for (const part of parts) {
    current = current ? `${current}/${part}` : part;
    const result = await apiRequest(profile, "POST", "/api/v1/files/directory", { path: current }, [409]);
    if (result.statusCode !== 204 && result.statusCode !== 409) {
      throw new Error(`Could not create NAS folder ${current}`);
    }
  }
}

async function ensureRemoteDirectories(profile, paths) {
  const unique = [...new Set(paths.filter(Boolean))].sort((a, b) => a.split("/").length - b.split("/").length);
  for (const directory of unique) {
    await ensureRemoteDirectory(profile, directory);
  }
}

async function copyRecovery(profile, root, relative, label) {
  const source = localPath(root, relative);
  const info = await fsp.lstat(source).catch((error) => error.code === "ENOENT" ? null : Promise.reject(error));
  if (!info || !info.isFile() || info.isSymbolicLink()) return null;
  const destination = localPath(recoveryPath(profile.id), `${label}/${new Date().toISOString().replaceAll(":", "-")}/${relative}`);
  await fsp.mkdir(path.dirname(destination), { recursive: true, mode: 0o700 });
  await fsp.copyFile(source, destination);
  return destination;
}

async function uploadFile(profile, root, relative, expectedRemoteHash) {
  const source = localPath(root, relative);
  const info = await fsp.stat(source);
  if (!info.isFile()) throw new Error(`Not a regular file: ${relative}`);
  const fullPath = remotePath(profile, relative);
  const target = apiURL(profile, `/api/v1/sync/file?${new URLSearchParams({
    path: fullPath,
    expectedSha256: expectedRemoteHash,
  })}`);
  const headers = {
    "Content-Type": "application/octet-stream",
    "Content-Length": info.size,
    Accept: "application/json",
  };
  const cookie = await cookiesFor(profile, target);
  if (cookie) headers.Cookie = cookie;
  headers["X-CSRF-Token"] = csrfTokens.get(profile.id);
  const response = await httpRequest(target, "PUT", headers, null, source);
  if (response.statusCode !== 204) {
    let message = `Upload failed for ${relative} (HTTP ${response.statusCode})`;
    try {
      message = JSON.parse(response.body.toString("utf8")).error || message;
    } catch {}
    throw new Error(message);
  }
}

async function assertLocalHash(targetPath, relative, expectedHash) {
  const current = await fsp.lstat(targetPath).catch((error) => error.code === "ENOENT" ? null : Promise.reject(error));
  if (expectedHash === null) {
    if (current) throw new Error(`PC file appeared after the sync scan: ${relative}`);
    return null;
  }
  if (!current?.isFile() || current.isSymbolicLink() || await hashFile(targetPath) !== expectedHash) {
    throw new Error(`PC file changed after the sync scan: ${relative}`);
  }
  return current;
}

async function downloadFile(profile, root, relative, expectedHash, expectedLocalHash) {
  const targetPath = await ensureLocalParents(root, relative);
  const temporary = `${targetPath}.asns-download-${crypto.randomBytes(6).toString("hex")}`;
  try {
    await assertLocalHash(targetPath, relative, expectedLocalHash);
    await streamDownload(profile, relative, temporary, expectedHash);
    const current = await assertLocalHash(targetPath, relative, expectedLocalHash);
    if (current) {
      const recovery = await copyRecovery(profile, root, relative, "replaced-local-files");
      if (!recovery || await hashFile(recovery) !== expectedLocalHash) {
        throw new Error(`Could not verify the local replacement recovery copy: ${relative}`);
      }
    }
    await fsp.rename(temporary, targetPath);
  } finally {
    await fsp.rm(temporary, { force: true });
  }
}

async function streamDownload(profile, relative, destination, expectedHash) {
  const remote = apiURL(profile, `/api/v1/files/download?${new URLSearchParams({ path: remotePath(profile, relative) })}`);
  const cookie = await cookiesFor(profile, remote);
  const headers = cookie ? { Cookie: cookie } : {};
  const transport = remote.protocol === "https:" ? https : http;
  await new Promise((resolve, reject) => {
    const request = transport.request(remote, { method: "GET", headers }, (response) => {
      if (response.statusCode !== 200) {
        const chunks = [];
      let captured = 0;
      let received = 0;
      let settled = false;
      const fail = () => {
        if (settled) return;
        settled = true;
        reject(new Error(`Download failed for ${relative} (HTTP ${response.statusCode}): ${Buffer.concat(chunks).toString("utf8")}`));
      };
      response.on("data", (chunk) => {
        received += chunk.length;
        if (captured < 1024) {
          const excerpt = chunk.subarray(0, 1024 - captured);
          chunks.push(excerpt);
          captured += excerpt.length;
        }
        if (received > 1024 * 1024) {
          response.destroy();
          fail();
        }
      });
      response.on("end", fail);
      response.on("error", fail);
      return;
    }
      const output = fs.createWriteStream(destination, { mode: 0o600, flags: "wx" });
      response.on("error", reject);
      output.on("error", reject);
      output.on("finish", resolve);
      response.pipe(output);
    });
    request.setTimeout(30 * 60 * 1000, () => request.destroy(new Error("ASNS download timed out")));
    request.on("error", reject);
    request.end();
  });
  const actualHash = await hashFile(destination);
  if (actualHash !== expectedHash) {
    await fsp.rm(destination, { force: true });
    throw new Error(`Downloaded content hash mismatch for ${relative}`);
  }
}

async function deleteRemote(profile, root, relative, expectedHash) {
  const recoveryRoot = recoveryPath(profile.id);
  const recoveryFile = localPath(recoveryRoot, `deleted-from-nas/${new Date().toISOString().replaceAll(":", "-")}/${relative}`);
  await fsp.mkdir(path.dirname(recoveryFile), { recursive: true, mode: 0o700 });
  const temporary = `${recoveryFile}.asns-recovery-${crypto.randomBytes(6).toString("hex")}`;
  try {
    await streamDownload(profile, relative, temporary, expectedHash);
    await fsp.rename(temporary, recoveryFile);
  } finally {
    await fsp.rm(temporary, { force: true });
  }
  await apiRequest(profile, "DELETE", `/api/v1/files?${new URLSearchParams({
    path: remotePath(profile, relative),
    expectedSha256: expectedHash,
  })}`);
}

async function deleteLocal(profile, root, relative, expectedHash) {
  const source = localPath(root, relative);
  if (await hashFile(source) !== expectedHash) {
    throw new Error(`Local file changed after deletion confirmation: ${relative}`);
  }
  const recovery = await copyRecovery(profile, root, relative, "deleted-from-pc");
  if (!recovery || await hashFile(recovery) !== expectedHash) {
    throw new Error(`Could not verify the local deletion recovery copy: ${relative}`);
  }
  if (await hashFile(source) !== expectedHash) {
    throw new Error(`Local file changed while preparing deletion recovery: ${relative}`);
  }
  await fsp.rm(source, { force: false });
}

async function readProfileStore() {
  try {
    const profiles = JSON.parse(await fsp.readFile(profilePath(), "utf8"));
    return Array.isArray(profiles) ? profiles : [];
  } catch (error) {
    if (error.code === "ENOENT") return [];
    throw new Error(`Cannot read profiles: ${error.message}`);
  }
}

function confirmationToken(profileId, deletions, local, remote, previous) {
  const localByPath = new Map(local.files.map((entry) => [entry.path, entry.sha256]));
  const remoteByPath = new Map(remote.map((entry) => [entry.path, entry.sha256]));
  const reviewed = deletions.map(({ type, path: filePath }) => ({
    type,
    path: filePath,
    localHash: localByPath.get(filePath) || null,
    remoteHash: remoteByPath.get(filePath) || null,
    previous: previous[filePath] || null,
  }));
  return crypto.createHash("sha256").update(JSON.stringify({ profileId, reviewed })).digest("hex");
}

async function syncProfile(profileId, confirmDeletions = false, reviewedToken = "") {
  if (runningProfiles.has(profileId)) throw new Error("This profile is already syncing");
  runningProfiles.add(profileId);
  try {
    const profiles = await readProfileStore();
    const raw = profiles.find((profile) => profile.id === profileId);
    if (!raw) throw new Error("Sync profile no longer exists");
    const profile = validateProfile(raw);
    const root = await fsp.realpath(profile.localFolder);
    const rootInfo = await fsp.stat(root);
    if (!rootInfo.isDirectory()) throw new Error("Selected local path is not a directory");
    await authStatus(profile);
    emit(profile.id, "Scanning selected folders…");
    await ensureRemoteDirectory(profile, profile.remoteFolder);
    const [local, remote, previous] = await Promise.all([
      localManifest(root, profile),
      remoteManifest(profile),
      readState(profile),
    ]);
    const plan = planSync(local.files, remote, previous, { propagateDeletes: profile.syncDeletes });
    const counts = {};
    for (const action of plan.actions) counts[action.type] = (counts[action.type] || 0) + 1;
    const deletions = plan.actions.filter((action) => action.type === "deleteLocal" || action.type === "deleteRemote");
    const token = deletions.length ? confirmationToken(profile.id, deletions, local, remote, previous) : "";
    if (deletions.length && (!confirmDeletions || reviewedToken !== token)) {
      emit(profile.id, `${deletions.length} deletion(s) need your confirmation; no files were deleted.`);
      return { status: "needs-confirmation", actions: plan.actions, counts, deletionToken: token };
    }

    await ensureRemoteDirectories(profile, [
      profile.remoteFolder,
      ...local.directories.map((directory) => path.posix.join(profile.remoteFolder, directory)),
    ]);
    const remoteDirectories = await apiRequest(profile, "GET", `/api/v1/sync/manifest?${new URLSearchParams(
      profile.remoteFolder ? { directory: profile.remoteFolder } : {},
    )}`);
    const downloadDirectories = (remoteDirectories.data.entries || [])
      .filter((entry) => entry.directory)
      .map((entry) => profile.remoteFolder ? path.posix.relative(profile.remoteFolder, entry.path) : entry.path)
      .filter((relative) => relative && relative !== ".." && !relative.startsWith("../"));
    for (const directory of downloadDirectories) {
      validateRelative(directory);
      const destination = localPath(root, directory);
      await fsp.mkdir(destination, { recursive: true, mode: 0o700 });
    }

    for (let index = 0; index < plan.actions.length; index += 1) {
      const action = plan.actions[index];
      emit(profile.id, `${index + 1}/${plan.actions.length}: ${action.type} ${action.path}`);
      if (action.type === "upload") {
        const parent = path.posix.dirname(action.path);
        if (parent !== ".") await ensureRemoteDirectory(profile, path.posix.join(profile.remoteFolder, parent));
        const existingRemote = remote.find((item) => item.path === action.path);
        await uploadFile(profile, root, action.path, existingRemote?.sha256 || "");
      } else if (action.type === "download") {
        const entry = remote.find((item) => item.path === action.path);
        if (!entry) throw new Error(`NAS file disappeared during sync: ${action.path}`);
        const localEntry = local.files.find((item) => item.path === action.path);
        await downloadFile(profile, root, action.path, entry.sha256, localEntry?.sha256 ?? null);
      } else if (action.type === "deleteRemote") {
        const entry = remote.find((item) => item.path === action.path);
        if (!entry) continue;
        await deleteRemote(profile, root, action.path, entry.sha256);
      } else if (action.type === "deleteLocal") {
        const entry = local.files.find((item) => item.path === action.path);
        if (!entry) throw new Error(`PC file disappeared during sync: ${action.path}`);
        await deleteLocal(profile, root, action.path, entry.sha256);
      }
    }

    const [freshLocal, freshRemote] = await Promise.all([localManifest(root, profile), remoteManifest(profile)]);
    const finalState = {};
    const localByPath = new Map(freshLocal.files.map((entry) => [entry.path, entry.sha256]));
    const remoteByPath = new Map(freshRemote.map((entry) => [entry.path, entry.sha256]));
    const finalPaths = new Set([...localByPath.keys(), ...remoteByPath.keys()]);
    for (const relative of finalPaths) {
      if (localByPath.get(relative) !== remoteByPath.get(relative)) {
        throw new Error(`Files changed during sync; run sync again to reconcile: ${relative}`);
      }
      finalState[relative] = {
        localHash: localByPath.get(relative),
        remoteHash: remoteByPath.get(relative),
      };
    }
    await writeState(profile, finalState);
    emit(profile.id, `Sync complete: ${plan.actions.length} file operation(s).`);
    return { status: "completed", actions: plan.actions, counts };
  } finally {
    runningProfiles.delete(profileId);
  }
}

async function saveProfiles(profiles) {
  if (!Array.isArray(profiles) || profiles.length > 50) throw new Error("At most 50 sync profiles are supported");
  const checked = profiles.map((profile) => validateProfile(profile, { allowIncomplete: true, requireLocalFolder: false }));
  if (new Set(checked.map((profile) => profile.id)).size !== checked.length) throw new Error("Sync profile IDs must be unique");
  const destination = profilePath();
  await fsp.mkdir(path.dirname(destination), { recursive: true, mode: 0o700 });
  const temporary = `${destination}.tmp`;
  await fsp.writeFile(temporary, JSON.stringify(checked, null, 2), { mode: 0o600 });
  await fsp.rename(temporary, destination);
}

function registerIPC() {
  ipcMain.handle("profiles:load", () => readProfileStore());
  ipcMain.handle("profiles:save", (_event, profiles) => saveProfiles(profiles));
  ipcMain.handle("folder:choose", async () => {
    const result = await dialog.showOpenDialog(mainWindow, { properties: ["openDirectory", "createDirectory"] });
    return result.canceled ? null : result.filePaths[0];
  });
  ipcMain.handle("auth:login", (_event, profile, username, password) => login(profile, username, password));
  ipcMain.handle("auth:status", (_event, profile) => authStatus(profile));
  ipcMain.handle("auth:logout", async (_event, profile) => {
    const checked = validateProfile(profile, { requireLocalFolder: false });
    try {
      await authStatus(checked);
      await apiRequest(checked, "POST", "/api/v1/auth/logout");
    } finally {
      csrfTokens.delete(checked.id);
      const target = apiURL(checked, "/api/v1/auth/logout");
      const stored = await sessionFor(checked).cookies.get({ url: target.origin });
      for (const cookie of stored) {
        await sessionFor(checked).cookies.remove(target.origin, cookie.name);
      }
    }
  });
  ipcMain.handle("sync:run", (_event, profileId, confirmDeletions, reviewedToken) =>
    syncProfile(profileId, confirmDeletions === true, typeof reviewedToken === "string" ? reviewedToken : ""));
  ipcMain.handle("recovery:open", async (_event, profileId) => {
    if (!/^[a-f0-9-]{36}$/i.test(profileId || "")) throw new Error("Invalid profile");
    const directory = recoveryPath(profileId);
    await fsp.mkdir(directory, { recursive: true, mode: 0o700 });
    const error = await shell.openPath(directory);
    if (error) throw new Error(error);
  });
}

async function scheduledSync() {
  const now = Date.now();
  const profiles = await readProfileStore();
  for (const profile of profiles) {
    if (!profile.intervalMinutes || runningProfiles.has(profile.id)) continue;
    const dueAt = profileSessions.get(`schedule:${profile.id}`) || 0;
    if (now < dueAt) continue;
    profileSessions.set(`schedule:${profile.id}`, now + profile.intervalMinutes * 60 * 1000);
    syncProfile(profile.id).catch((error) => emit(profile.id, `Scheduled sync failed: ${error.message}`));
  }
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1120,
    height: 800,
    minWidth: 760,
    minHeight: 600,
    title: "ASNS Sync",
    webPreferences: {
      preload: path.join(__dirname, "preload.cjs"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });
  if (process.env.ASNS_DESKTOP_DEV_URL) {
    mainWindow.loadURL(`${process.env.ASNS_DESKTOP_DEV_URL}/?desktop=sync`);
  } else {
    const indexPath = app.isPackaged
      ? path.join(process.resourcesPath, "frontend", "index.html")
      : path.resolve(__dirname, "../frontend/dist/index.html");
    mainWindow.loadFile(indexPath, { query: { desktop: "sync" } });
  }
}

app.whenReady().then(() => {
  registerIPC();
  createWindow();
  setInterval(() => {
    scheduledSync().catch((error) => emit("", `Scheduler error: ${error.message}`));
  }, 30 * 1000);
  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});

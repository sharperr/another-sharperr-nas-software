const path = require("node:path");

function validateProfile(profile, options = {}) {
  if (!profile || typeof profile !== "object" || !/^[a-f0-9-]{36}$/i.test(profile.id || "")) {
    throw new Error("Invalid sync profile");
  }
  let serverUrl = profile.serverUrl;
  if (typeof serverUrl !== "string" || !serverUrl.trim()) {
    if (!options.allowIncomplete) throw new Error("Enter your ASNS server URL");
    serverUrl = "";
  } else {
    let base;
    try {
      base = new URL(serverUrl);
    } catch {
      if (!options.allowIncomplete) throw new Error("Enter a valid server URL, for example http://nas.example:8080");
      base = null;
    }
    if (!base) {
      serverUrl = serverUrl.trim();
    } else {
      if (!["http:", "https:"].includes(base.protocol) || base.username || base.password ||
          base.search || base.hash || (base.pathname !== "/" && base.pathname !== "")) {
        throw new Error("Server URL must be an HTTP(S) origin without credentials, path, query, or fragment");
      }
      serverUrl = base.origin;
    }
  }
  if (typeof profile.localFolder !== "string" ||
      (options.requireLocalFolder !== false && !path.isAbsolute(profile.localFolder))) {
    throw new Error("Choose an existing local folder");
  }
  if (profile.localFolder && !path.isAbsolute(profile.localFolder)) {
    throw new Error("Choose an existing local folder");
  }
  const remoteFolder = typeof profile.remoteFolder === "string" ? profile.remoteFolder.replace(/\/+$/, "") : null;
  if (remoteFolder === null || remoteFolder.includes("\\") || remoteFolder.includes("\0") ||
      remoteFolder.startsWith("/") || (remoteFolder && remoteFolder.split("/").some((part) => !part || part === "." || part === ".."))) {
    throw new Error("NAS folder must be a relative path inside your ASNS storage");
  }
  if (typeof profile.name !== "string" || profile.name.length > 100 ||
      typeof profile.username !== "string" || profile.username.length > 256) {
    throw new Error("Profile name and username must be valid strings");
  }
  if (!Number.isInteger(profile.intervalMinutes) || profile.intervalMinutes < 0 || profile.intervalMinutes > 10080) {
    throw new Error("Sync interval must be manual (0) or between 1 minute and 7 days");
  }
  if (!Array.isArray(profile.includePatterns) || !Array.isArray(profile.excludePatterns) ||
      profile.includePatterns.some((entry) => typeof entry !== "string" || entry.length > 256) ||
      profile.excludePatterns.some((entry) => typeof entry !== "string" || entry.length > 256)) {
    throw new Error("Include and exclude patterns must be lists of strings up to 256 characters");
  }
  return { ...profile, serverUrl, remoteFolder };
}

module.exports = { validateProfile };

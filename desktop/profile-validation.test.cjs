const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { validateProfile } = require("./profile-validation.cjs");

function profile(overrides = {}) {
  return {
    id: "c04dc4c8-16a5-452e-891b-4174daf166b0",
    name: "Sync profile",
    serverUrl: "",
    username: "",
    localFolder: "",
    remoteFolder: "",
    intervalMinutes: 0,
    includePatterns: [],
    excludePatterns: [],
    syncDeletes: true,
    ...overrides,
  };
}

test("new incomplete profiles can be saved before server and folder are selected", () => {
  const result = validateProfile(profile(), { allowIncomplete: true, requireLocalFolder: false });
  assert.equal(result.serverUrl, "");
  assert.equal(result.localFolder, "");
});

test("connection validation does not require the local folder", () => {
  const result = validateProfile(profile({ serverUrl: "https://nas.example:8080/" }), { requireLocalFolder: false });
  assert.equal(result.serverUrl, "https://nas.example:8080");
  assert.throws(() => validateProfile(profile({ serverUrl: "" }), { requireLocalFolder: false }), /server URL/);
});

test("sync validation requires an absolute local folder", () => {
  assert.throws(() => validateProfile(profile({ serverUrl: "http://nas.example:8080" })), /local folder/);
  const result = validateProfile(profile({
    serverUrl: "http://nas.example:8080",
    localFolder: path.resolve("selected-folder"),
  }));
  assert.equal(result.localFolder, path.resolve("selected-folder"));
});

test("NAS destination rejects traversal and normalizes trailing separators", () => {
  assert.throws(() => validateProfile(profile({ remoteFolder: "../outside" }), {
    allowIncomplete: true,
    requireLocalFolder: false,
  }), /relative path/);
  assert.throws(() => validateProfile(profile({ remoteFolder: "safe//outside" }), {
    allowIncomplete: true,
    requireLocalFolder: false,
  }), /relative path/);
  assert.equal(validateProfile(profile({ remoteFolder: "safe/folder///" }), {
    allowIncomplete: true,
    requireLocalFolder: false,
  }).remoteFolder, "safe/folder");
});

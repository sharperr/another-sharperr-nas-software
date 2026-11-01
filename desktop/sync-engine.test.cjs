const test = require("node:test");
const assert = require("node:assert/strict");
const { planSync } = require("./sync-engine.cjs");

const file = (path, sha256) => ({ path, sha256 });

test("first sync uploads local-only files and downloads NAS-only files", () => {
  const plan = planSync([file("pc.txt", "a")], [file("nas.txt", "b")], {});
  assert.deepEqual(plan.actions.map(({ type, path }) => [type, path]), [
    ["download", "nas.txt"],
    ["upload", "pc.txt"],
  ]);
});

test("local-only changes upload while remote-only changes download", () => {
  const previous = {
    localChange: { localHash: "old-a", remoteHash: "old-a" },
    remoteChange: { localHash: "old-b", remoteHash: "old-b" },
  };
  const plan = planSync(
    [file("localChange", "new-a"), file("remoteChange", "old-b")],
    [file("localChange", "old-a"), file("remoteChange", "new-b")],
    previous,
  );
  assert.deepEqual(plan.actions.map(({ type, path }) => [type, path]), [
    ["upload", "localChange"],
    ["download", "remoteChange"],
  ]);
});

test("simultaneous divergent changes prefer the NAS version", () => {
  const plan = planSync(
    [file("conflict", "pc-new")],
    [file("conflict", "nas-new")],
    { conflict: { localHash: "old", remoteHash: "old" } },
  );
  assert.equal(plan.actions.length, 1);
  assert.deepEqual(plan.actions[0], {
    type: "download",
    path: "conflict",
    conflict: true,
    reason: "both sides changed; NAS copy wins",
  });
});

test("deletions are planned only when explicitly enabled", () => {
  const previous = {
    deletedOnPC: { localHash: "a", remoteHash: "a" },
    deletedOnNAS: { localHash: "b", remoteHash: "b" },
  };
  const local = [file("deletedOnNAS", "b")];
  const remote = [file("deletedOnPC", "a")];
  assert.equal(planSync(local, remote, previous).actions.length, 2);
  const plan = planSync(local, remote, previous, { propagateDeletes: true });
  assert.deepEqual(plan.actions.map(({ type, path }) => [type, path]).sort(), [
    ["deleteRemote", "deletedOnPC"],
    ["deleteLocal", "deletedOnNAS"],
  ].sort());
});

test("deletion and replacement edits restore changed copies instead of deleting", () => {
  const plan = planSync(
    [file("pc-edited", "new")],
    [file("nas-edited", "new")],
    {
      "pc-edited": { localHash: "old", remoteHash: "old" },
      "nas-edited": { localHash: "old", remoteHash: "old" },
    },
    { propagateDeletes: true },
  );
  assert.deepEqual(plan.actions.map(({ type, path }) => [type, path]).sort(), [
    ["upload", "pc-edited"],
    ["download", "nas-edited"],
  ].sort());
});

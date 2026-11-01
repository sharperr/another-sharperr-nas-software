function planSync(localEntries, remoteEntries, previousState, options = {}) {
  const propagateDeletes = options.propagateDeletes === true;
  const local = new Map(localEntries.filter((entry) => !entry.directory).map((entry) => [entry.path, entry]));
  const remote = new Map(remoteEntries.filter((entry) => !entry.directory).map((entry) => [entry.path, entry]));
  const previous = previousState || {};
  const actions = [];
  const nextState = {};
  const paths = [...new Set([...local.keys(), ...remote.keys(), ...Object.keys(previous)])].sort();

  for (const path of paths) {
    const localEntry = local.get(path);
    const remoteEntry = remote.get(path);
    const old = previous[path];
    if (localEntry && remoteEntry && localEntry.sha256 === remoteEntry.sha256) {
      nextState[path] = { localHash: localEntry.sha256, remoteHash: remoteEntry.sha256 };
      continue;
    }

    if (!old) {
      if (localEntry && remoteEntry) {
        actions.push({ type: "download", path, conflict: true, reason: "initial conflict; NAS copy wins" });
        nextState[path] = { localHash: localEntry.sha256, remoteHash: remoteEntry.sha256 };
      } else if (localEntry) {
        actions.push({ type: "upload", path, reason: "new on PC" });
        nextState[path] = { localHash: localEntry.sha256, remoteHash: localEntry.sha256 };
      } else if (remoteEntry) {
        actions.push({ type: "download", path, reason: "new on NAS" });
        nextState[path] = { localHash: remoteEntry.sha256, remoteHash: remoteEntry.sha256 };
      }
      continue;
    }

    if (localEntry && remoteEntry) {
      const localChanged = localEntry.sha256 !== old.localHash;
      const remoteChanged = remoteEntry.sha256 !== old.remoteHash;
      if (localChanged && !remoteChanged) {
        actions.push({ type: "upload", path, reason: "changed on PC" });
        nextState[path] = { localHash: localEntry.sha256, remoteHash: localEntry.sha256 };
      } else {
        actions.push({
          type: "download", path,
          conflict: localChanged && remoteChanged,
          reason: localChanged && remoteChanged ? "both sides changed; NAS copy wins" : "changed on NAS",
        });
        nextState[path] = { localHash: remoteEntry.sha256, remoteHash: remoteEntry.sha256 };
      }
      continue;
    }

    if (!localEntry && remoteEntry) {
      if (propagateDeletes && remoteEntry.sha256 === old.remoteHash) {
        actions.push({ type: "deleteRemote", path, reason: "deleted on PC" });
      } else {
        actions.push({ type: "download", path, reason: "new or changed on NAS" });
        nextState[path] = { localHash: remoteEntry.sha256, remoteHash: remoteEntry.sha256 };
      }
      continue;
    }

    if (localEntry && !remoteEntry) {
      if (propagateDeletes && localEntry.sha256 === old.localHash) {
        actions.push({ type: "deleteLocal", path, reason: "deleted on NAS" });
      } else {
        actions.push({ type: "upload", path, reason: "new or changed on PC" });
        nextState[path] = { localHash: localEntry.sha256, remoteHash: localEntry.sha256 };
      }
    }
  }

  return { actions, nextState };
}

module.exports = { planSync };

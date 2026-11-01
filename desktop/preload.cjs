const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("asnsDesktop", {
  loadProfiles: () => ipcRenderer.invoke("profiles:load"),
  saveProfiles: (profiles) => ipcRenderer.invoke("profiles:save", profiles),
  chooseFolder: () => ipcRenderer.invoke("folder:choose"),
  login: (profile, username, password) =>
    ipcRenderer.invoke("auth:login", profile, username, password),
  authStatus: (profile) => ipcRenderer.invoke("auth:status", profile),
  logout: (profile) => ipcRenderer.invoke("auth:logout", profile),
  sync: (profileId, confirmDeletions = false, reviewedToken = "") =>
    ipcRenderer.invoke("sync:run", profileId, confirmDeletions, reviewedToken),
  openRecovery: (profileId) => ipcRenderer.invoke("recovery:open", profileId),
  onSyncEvent: (callback) => {
    const listener = (_event, data) => callback(data);
    ipcRenderer.on("sync:event", listener);
    return () => ipcRenderer.removeListener("sync:event", listener);
  },
});

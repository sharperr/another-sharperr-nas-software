import type { BackupArchive, BackupSettings, Disk, DiskHealth, Identity, ManagedUser, MetricSample, RAIDArray, RestoreJob, RestoreStage, ServiceStatus, SMBPermission, SMBShare, SMBUser, StorageListing, SystemSettings, SystemSnapshot } from "../types/system";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body && !(init.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(path, {
    credentials: "same-origin",
    ...init,
    headers,
  });
  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as { error?: string } | null;
    throw new Error(body?.error ?? `Request failed (${response.status})`);
  }
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

export const api = {
  login: (username: string, password: string) =>
    request<Identity>("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
  me: () => request<Identity>("/api/v1/auth/me"),
  logout: (csrfToken: string) =>
    request<void>("/api/v1/auth/logout", {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken },
    }),
  system: () => request<SystemSnapshot>("/api/v1/system"),
  disks: () => request<{ disks: Disk[] }>("/api/v1/disks"),
  diskHealth: () => request<{ disks: DiskHealth[] }>("/api/v1/disks/smart"),
  raidArrays: () => request<{ arrays: RAIDArray[] }>("/api/v1/raid"),
  systemSettings: () => request<SystemSettings>("/api/v1/system/settings"),
  updateSystemSettings: (settings: Partial<SystemSettings>, csrfToken: string) =>
    request<SystemSettings>("/api/v1/system/settings", {
      method: "PUT",
      headers: { "X-CSRF-Token": csrfToken },
      body: JSON.stringify(settings),
    }),
  services: () => request<{ services: ServiceStatus[] }>("/api/v1/services"),
  restartService: (name: string, csrfToken: string) =>
    request<void>(`/api/v1/services/${encodeURIComponent(name)}/restart`, {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken },
    }),
  monitoringHistory: (hours: number) =>
    request<{ hours: number; samples: MetricSample[] }>(`/api/v1/monitoring/history?hours=${hours}`),
  backups: () => request<{ settings: BackupSettings; archives: BackupArchive[] }>("/api/v1/backups"),
  deleteBackup: (name: string, csrfToken: string) =>
    request<void>(`/api/v1/backups/${encodeURIComponent(name)}`, {
      method: "DELETE",
      headers: { "X-CSRF-Token": csrfToken },
    }),
  setBackupSettings: (settings: BackupSettings, csrfToken: string) =>
    request<BackupSettings>("/api/v1/backups/settings", {
      method: "PUT",
      headers: { "X-CSRF-Token": csrfToken },
      body: JSON.stringify(settings),
    }),
  runBackup: (csrfToken: string) =>
    request<void>("/api/v1/backups/run", {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken },
    }),
  stageBackupRestore: (name: string, csrfToken: string) =>
    request<RestoreStage>("/api/v1/backups/restore/stage", {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken },
      body: JSON.stringify({ name }),
    }),
  applyBackupRestore: (id: string, confirmation: string, csrfToken: string) =>
    request<RestoreJob>("/api/v1/backups/restore/apply", {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken },
      body: JSON.stringify({ id, confirmation }),
    }),
  backupRestoreStatus: (id: string) =>
    request<RestoreJob>(`/api/v1/backups/restore/${encodeURIComponent(id)}`),
  shares: () => request<{ shares: SMBShare[]; users: SMBUser[] }>("/api/v1/shares"),
  createShare: (name: string, permissions: SMBPermission[], csrfToken: string) =>
    request<SMBShare>("/api/v1/shares", {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken },
      body: JSON.stringify({ name, permissions }),
    }),
  updateShare: (id: string, name: string, permissions: SMBPermission[], csrfToken: string) =>
    request<SMBShare>(`/api/v1/shares/${encodeURIComponent(id)}`, {
      method: "PUT",
      headers: { "X-CSRF-Token": csrfToken },
      body: JSON.stringify({ name, permissions }),
    }),
  deleteShare: (id: string, csrfToken: string) =>
    request<void>(`/api/v1/shares/${encodeURIComponent(id)}`, {
      method: "DELETE",
      headers: { "X-CSRF-Token": csrfToken },
    }),
  createSMBUser: (username: string, password: string, csrfToken: string) =>
    request<SMBUser>("/api/v1/smb-users", {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken },
      body: JSON.stringify({ username, password }),
    }),
  updateSMBPassword: (username: string, password: string, csrfToken: string) =>
    request<void>(`/api/v1/smb-users/${encodeURIComponent(username)}/password`, {
      method: "PUT",
      headers: { "X-CSRF-Token": csrfToken },
      body: JSON.stringify({ password }),
    }),
  deleteSMBUser: (username: string, csrfToken: string) =>
    request<void>(`/api/v1/smb-users/${encodeURIComponent(username)}`, {
      method: "DELETE",
      headers: { "X-CSRF-Token": csrfToken },
    }),
  users: () => request<{ users: ManagedUser[] }>("/api/v1/users"),
  createUser: (username: string, password: string, quotaBytes: number, csrfToken: string) =>
    request<ManagedUser>("/api/v1/users", {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken },
      body: JSON.stringify({ username, password, quotaBytes }),
    }),
  setUserQuota: (userID: number, quotaBytes: number, csrfToken: string) =>
    request<void>(`/api/v1/users/${userID}/quota`, {
      method: "PUT",
      headers: { "X-CSRF-Token": csrfToken },
      body: JSON.stringify({ quotaBytes }),
    }),
  files: (directory: string) => {
    const query = new URLSearchParams();
    if (directory) query.set("directory", directory);
    return request<StorageListing>(`/api/v1/files?${query.toString()}`);
  },
  createDirectory: (path: string, csrfToken: string) =>
    request<void>("/api/v1/files/directory", {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken },
      body: JSON.stringify({ path }),
    }),
  uploadFile: (file: File, directory: string, csrfToken: string) => {
    const body = new FormData();
    body.set("file", file);
    body.set("directory", directory);
    return request<void>("/api/v1/files/upload", {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken },
      body,
    });
  },
  deleteFile: (path: string, csrfToken: string) =>
    request<void>(`/api/v1/files?${new URLSearchParams({ path })}`, {
      method: "DELETE",
      headers: { "X-CSRF-Token": csrfToken },
    }),
  downloadFile: async (path: string) => {
    const response = await fetch(`/api/v1/files/download?${new URLSearchParams({ path })}`, {
      credentials: "same-origin",
    });
    if (!response.ok) {
      const body = (await response.json().catch(() => null)) as { error?: string } | null;
      throw new Error(body?.error ?? `Download failed (${response.status})`);
    }
    return response.blob();
  },
};

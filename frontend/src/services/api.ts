import type { Disk, Identity, ManagedUser, StorageListing, SystemSnapshot } from "../types/system";

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

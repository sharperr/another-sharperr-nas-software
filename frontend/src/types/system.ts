export interface Identity {
  id: number;
  username: string;
  role: string;
  csrfToken: string;
}

export interface SystemSnapshot {
  cpuPercent: number | null;
  memory: {
    totalBytes: number;
    usedBytes: number;
    availableBytes: number;
    percent: number;
  };
  network: {
    receiveBytesPerSecond: number;
    sendBytesPerSecond: number;
  };
  uptimeSeconds: number;
  collectedAt: string;
}

export interface Disk {
  name: string;
  model: string;
  serial: string;
  capacityBytes: number;
  removable: boolean;
}

export interface ManagedUser {
  id: number;
  username: string;
  role: "admin" | "user";
  quotaBytes: number;
  storageUsedBytes: number;
  createdAt: string;
}

export interface StorageEntry {
  name: string;
  path: string;
  directory: boolean;
  sizeBytes: number;
  modifiedAt: string;
}

export interface StorageListing {
  entries: StorageEntry[];
  directory: string;
  usedBytes: number;
  quotaBytes: number;
}

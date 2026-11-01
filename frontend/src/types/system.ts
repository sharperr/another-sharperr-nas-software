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

export interface MetricSample {
  collectedAt: string;
  cpuPercent: number | null;
  memoryTotalBytes: number;
  memoryUsedBytes: number;
  receiveBytesPerSecond: number;
  sendBytesPerSecond: number;
  uptimeSeconds: number;
}

export interface BackupSettings {
  enabled: boolean;
  intervalHours: number;
  destination: string;
  nextRunAt?: string;
  lastRunAt?: string;
  lastStatus: string;
  lastError?: string;
}

export interface BackupArchive {
  name: string;
  sizeBytes: number;
  createdAt: string;
}

export interface RestoreStage {
  id: string;
  createdAt: string;
  fileCount: number;
  totalSizeBytes: number;
}

export interface RestoreJob {
  id: string;
  status: "queued" | "verifying" | "stopping" | "starting" | "succeeded" | "failed";
  error?: string;
  updatedAt: string;
}

export interface SMBPermission {
  username: string;
  access: "read" | "write";
}

export interface SMBUser {
  username: string;
  createdAt: string;
}

export interface SMBShare {
  id: string;
  name: string;
  path: string;
  permissions: SMBPermission[];
  createdAt: string;
}

export interface Disk {
  name: string;
  model: string;
  serial: string;
  capacityBytes: number;
  removable: boolean;
}

export interface DiskHealth {
  device: string;
  model?: string;
  serial?: string;
  healthy?: boolean;
  temperatureC?: number;
  smartAvailable: boolean;
  error?: string;
}

export interface RAIDArray {
  name: string;
  level: string;
  state: "healthy" | "degraded" | "inactive" | "active";
  devices: string[];
  activeDevices: number;
  raidDevices: number;
  degraded: number;
  syncAction?: string;
  syncPercent?: number;
  mdadmState?: string;
  detailError?: string;
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

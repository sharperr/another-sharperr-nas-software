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

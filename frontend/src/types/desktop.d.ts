export interface SyncProfile {
  id: string;
  name: string;
  serverUrl: string;
  username: string;
  localFolder: string;
  remoteFolder: string;
  intervalMinutes: number;
  includePatterns: string[];
  excludePatterns: string[];
  syncDeletes: boolean;
}

export interface SyncAction {
  type: string;
  path: string;
  reason?: string;
}

export interface SyncResult {
  status: "completed" | "needs-confirmation";
  actions: SyncAction[];
  counts: Record<string, number>;
  deletionToken?: string;
}

declare global {
  interface Window {
    asnsDesktop: {
      loadProfiles(): Promise<SyncProfile[]>;
      saveProfiles(profiles: SyncProfile[]): Promise<void>;
      chooseFolder(): Promise<string | null>;
      login(profile: SyncProfile, username: string, password: string): Promise<{ username: string }>;
      authStatus(profile: SyncProfile): Promise<{ username: string }>;
      logout(profile: SyncProfile): Promise<void>;
      sync(profileId: string, confirmDeletions?: boolean, reviewedToken?: string): Promise<SyncResult>;
      openRecovery(profileId: string): Promise<void>;
      onSyncEvent(callback: (event: { profileId: string; message: string }) => void): () => void;
    };
  }
}

export {};

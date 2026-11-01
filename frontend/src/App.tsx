import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Activity, Bell, Check, ChevronDown, ChevronRight, CircleHelp, Cpu, Database,
  Download, FileUp, Folder, FolderPlus, HardDrive, LayoutDashboard, LogOut,
  Menu, Moon, Network, RefreshCw, Search, Settings, ShieldCheck,
  SlidersHorizontal, Sun, Trash2, UserPlus, UserRound, X,
} from "lucide-react";
import { Button } from "./components/ui/button";
import { api } from "./services/api";
import type { BackupArchive, BackupSettings, Disk, DiskHealth, Identity, ManagedUser, MetricSample, RAIDArray, RestoreJob, RestoreStage, SMBPermission, SMBShare, SMBUser, StorageEntry, StorageListing, SystemSnapshot } from "./types/system";

type Page =
  | "Overview"
  | "Storage"
  | "File manager"
  | "Sharing"
  | "Applications"
  | "Monitoring"
  | "Users"
  | "Backups";
type Popover = "search" | "notifications" | "profile" | null;
type Tone = "blue" | "purple" | "green" | "amber";

const workspacePages: { label: Page; icon: typeof LayoutDashboard }[] = [
  { label: "Overview", icon: LayoutDashboard },
  { label: "Storage", icon: HardDrive },
  { label: "File manager", icon: Database },
  { label: "Sharing", icon: Network },
  { label: "Applications", icon: Activity },
];
const managementPages: { label: Page; icon: typeof LayoutDashboard }[] = [
  { label: "Monitoring", icon: SlidersHorizontal },
  { label: "Users", icon: UserRound },
  { label: "Backups", icon: RefreshCw },
];

function storedBoolean(key: string, fallback: boolean): boolean {
  try {
    const stored = window.localStorage.getItem(key);
    return stored === null ? fallback : stored === "true";
  } catch {
    return fallback;
  }
}

function savePreference(key: string, value: boolean): void {
  try {
    window.localStorage.setItem(key, String(value));
  } catch {
    // The preference remains usable for the current page when storage is disabled.
  }
}

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value < 0) return "Unavailable";
  if (value === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const order = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  const size = value / 1024 ** order;
  return `${size.toFixed(order >= 3 ? 1 : 0)} ${units[order]}`;
}

function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "Unavailable";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return days > 0 ? `${days}d ${hours}h ${minutes}m` : `${hours}h ${minutes}m`;
}

function Login({ onLogin }: { onLogin: (identity: Identity) => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setBusy(true);
    try {
      onLogin(await api.login(username, password));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to sign in");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="login-screen">
      <form className="login-card" onSubmit={submit}>
        <div className="brand-mark" aria-hidden="true">A</div>
        <p className="eyebrow">ANOTHER SHARPERR NAS SOFTWARE</p>
        <h1>Welcome to your NAS</h1>
        <p className="muted">Sign in to securely manage your storage server.</p>
        <label htmlFor="username">Username
          <input id="username" name="username" autoComplete="username" value={username}
            onChange={(event) => setUsername(event.target.value)} required />
        </label>
        <label htmlFor="password">Password
          <input id="password" name="password" autoComplete="current-password" type="password"
            value={password} onChange={(event) => setPassword(event.target.value)} required />
        </label>
        {error && <div className="form-error" role="alert">{error}</div>}
        <Button className="primary-button wide" disabled={busy}>
          {busy ? "Signing in…" : "Sign in"}
        </Button>
        <p className="login-foot"><ShieldCheck size={14} /> Sessions expire automatically after 12 hours</p>
      </form>
    </main>
  );
}

function quotaFromGiB(value: string): number {
  const parsed = Number(value);
  const bytes = Math.round(parsed * 1024 ** 3);
  if (!Number.isFinite(parsed) || parsed < 0 || !Number.isSafeInteger(bytes)) {
    throw new Error("Enter a valid GiB amount, or 0 for unlimited.");
  }
  return bytes;
}

function quotaGiB(bytes: number): string {
  return bytes === 0 ? "0" : String(bytes / 1024 ** 3);
}

function UserManagementPage({ identity }: { identity: Identity }) {
  const [users, setUsers] = useState<ManagedUser[]>([]);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [quota, setQuota] = useState("10");
  const [quotaInputs, setQuotaInputs] = useState<Record<number, string>>({});
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const loadUsers = useCallback(async () => {
    setError("");
    try {
      setUsers((await api.users()).users);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to load users");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void loadUsers(); }, [loadUsers]);

  async function createUser(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      await api.createUser(username, password, quotaFromGiB(quota), identity.csrfToken);
      setUsername("");
      setPassword("");
      await loadUsers();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to create user");
    } finally {
      setSaving(false);
    }
  }

  async function updateQuota(event: React.FormEvent<HTMLFormElement>, user: ManagedUser) {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      await api.setUserQuota(user.id, quotaFromGiB(quotaInputs[user.id] ?? quotaGiB(user.quotaBytes)), identity.csrfToken);
      await loadUsers();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to update storage limit");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="management-stack">
      {error && <div className="error-banner" role="alert">{error}</div>}
      <section className="panel user-create-panel">
        <div className="panel-heading">
          <div><h2><UserPlus size={16} /> Create an account</h2>
            <p>Each account gets an isolated ASNS storage folder and its own quota.</p></div>
        </div>
        <form className="user-create-form" onSubmit={(event) => void createUser(event)}>
          <label>Username
            <input value={username} onChange={(event) => setUsername(event.target.value)}
              pattern="[A-Za-z0-9._-]{1,64}" maxLength={64} autoComplete="off" required />
          </label>
          <label>Temporary password
            <input type="password" value={password} onChange={(event) => setPassword(event.target.value)}
              minLength={12} maxLength={72} autoComplete="new-password" required />
          </label>
          <label>Storage limit (GiB)
            <input type="number" min="0" step="any" value={quota}
              onChange={(event) => setQuota(event.target.value)} required />
          </label>
          <Button className="primary-button" disabled={saving}><UserPlus size={15} /> Create user</Button>
          <small>Use 0 for unlimited. Passwords must be at least 12 characters.</small>
        </form>
      </section>

      <section className="panel managed-users-panel">
        <div className="panel-heading">
          <div><h2>Accounts and storage</h2><p>Usage counts regular files inside each account's ASNS folder.</p></div>
          <Button variant="outline" className="outline-button" onClick={() => void loadUsers()} disabled={loading}>
            <RefreshCw size={14} /> Refresh
          </Button>
        </div>
        {loading ? <div className="management-empty">Loading accounts…</div> : users.length === 0
          ? <div className="management-empty">No accounts are configured.</div>
          : <div className="managed-user-list">
            {users.map((user) => {
              const percent = user.quotaBytes > 0
                ? Math.min(100, user.storageUsedBytes / user.quotaBytes * 100)
                : 0;
              return (
                <article className="managed-user-row" key={user.id}>
                  <div className="managed-user-summary">
                    <span className="profile-avatar"><UserRound size={15} /></span>
                    <div><strong>{user.username}</strong><small>{user.role === "admin" ? "Administrator" : "Standard account"}</small></div>
                  </div>
                  <div className="managed-user-quota">
                    <div className="managed-quota-label">
                      <span>{formatBytes(user.storageUsedBytes)} used</span>
                      <span>{user.quotaBytes === 0 ? "Unlimited" : `of ${formatBytes(user.quotaBytes)}`}</span>
                    </div>
                    <div className="quota-track"><span style={{ width: `${percent}%` }} /></div>
                  </div>
                  {user.role === "user" ? (
                    <form className="quota-edit-form" onSubmit={(event) => void updateQuota(event, user)}>
                      <label className="sr-only" htmlFor={`quota-${user.id}`}>Storage limit for {user.username} in GiB</label>
                      <input id={`quota-${user.id}`} type="number" min="0" step="any"
                        value={quotaInputs[user.id] ?? quotaGiB(user.quotaBytes)}
                        onChange={(event) => setQuotaInputs((values) => ({ ...values, [user.id]: event.target.value }))} />
                      <span>GiB</span>
                      <Button variant="outline" className="outline-button" disabled={saving}>Save</Button>
                    </form>
                  ) : <span className="admin-account-badge">Full access</span>}
                </article>
              );
            })}
          </div>}
        <p className="quota-footnote">Changing a limit below current usage keeps existing files but blocks uploads until usage is back under the limit.</p>
      </section>
    </div>
  );
}

function MetricChart({ title, unit, samples, value }: {
  title: string;
  unit: string;
  samples: MetricSample[];
  value: (sample: MetricSample) => number | null;
}) {
  const allValues = samples.map(value).filter((item): item is number => item !== null && Number.isFinite(item));
  const maximum = Math.max(1, ...allValues);
  const stride = Math.max(1, Math.ceil(samples.length / 180));
  const points = samples.filter((_, index) => index % stride === 0 || index === samples.length - 1)
    .map((sample, index, list) => {
      const current = value(sample);
      if (current === null || !Number.isFinite(current)) return null;
      const x = list.length < 2 ? 0 : index * 100 / (list.length - 1);
      const y = 96 - Math.min(1, current / maximum) * 88;
      return `${x.toFixed(2)},${y.toFixed(2)}`;
    }).filter((point): point is string => point !== null).join(" ");
  return (
    <section className="panel history-chart">
      <div className="panel-heading"><div><h2>{title}</h2><p>{allValues.length} real samples · {unit}</p></div></div>
      {allValues.length < 2
        ? <div className="chart-empty">History will appear after ASNS collects more samples.</div>
        : <svg viewBox="0 0 100 100" preserveAspectRatio="none" role="img" aria-label={`${title} history chart`}>
          <line x1="0" y1="8" x2="100" y2="8" />
          <line x1="0" y1="52" x2="100" y2="52" />
          <line x1="0" y1="96" x2="100" y2="96" />
          <polyline points={points} />
        </svg>}
      {allValues.length > 0 && <div className="history-range"><span>Oldest: {new Date(samples[0].collectedAt).toLocaleString()}</span>
        <strong>Peak: {Math.max(...allValues).toFixed(1)} {unit}</strong>
        <span>Latest: {new Date(samples[samples.length - 1].collectedAt).toLocaleString()}</span></div>}
    </section>
  );
}

function MonitoringPage() {
  const [hours, setHours] = useState(24);
  const [samples, setSamples] = useState<MetricSample[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    setError("");
    try {
      setSamples((await api.monitoringHistory(hours)).samples);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to load monitoring history");
    } finally {
      setLoading(false);
    }
  }, [hours]);
  useEffect(() => { void refresh(); }, [refresh]);

  const memoryPercent = (sample: MetricSample) =>
    sample.memoryTotalBytes > 0 ? sample.memoryUsedBytes * 100 / sample.memoryTotalBytes : null;

  return (
    <div className="management-stack">
      {error && <div className="error-banner" role="alert">{error}</div>}
      <section className="panel monitoring-toolbar">
        <div><h2><Activity size={16} /> Host history</h2>
          <p>Samples are read from Linux and retained locally for seven days.</p></div>
        <div className="monitoring-controls">
          <label htmlFor="history-hours">Time range</label>
          <select id="history-hours" value={hours} onChange={(event) => setHours(Number(event.target.value))}>
            <option value={1}>1 hour</option><option value={6}>6 hours</option>
            <option value={24}>24 hours</option><option value={168}>7 days</option>
          </select>
          <Button variant="outline" className="outline-button" disabled={loading} onClick={() => void refresh()}>
            <RefreshCw size={14} /> Refresh
          </Button>
        </div>
      </section>
      {loading && samples.length === 0
        ? <div className="management-empty">Loading host history…</div>
        : <>
          <MetricChart title="Processor utilization" unit="%" samples={samples} value={(sample) => sample.cpuPercent} />
          <MetricChart title="Memory utilization" unit="%" samples={samples} value={memoryPercent} />
          <MetricChart title="Network receive rate" unit="B/s" samples={samples} value={(sample) => sample.receiveBytesPerSecond} />
          <MetricChart title="Network send rate" unit="B/s" samples={samples} value={(sample) => sample.sendBytesPerSecond} />
          <p className="quota-footnote">Monitoring samples are recorded every 10 seconds. SMART health and temperatures are unavailable until the ASNS Agent is installed.</p>
        </>}
    </div>
  );
}

function SharingPage({ identity }: { identity: Identity }) {
  const [shares, setShares] = useState<SMBShare[]>([]);
  const [users, setUsers] = useState<SMBUser[]>([]);
  const [permissions, setPermissions] = useState<Record<string, SMBPermission[]>>({});
  const [newUsername, setNewUsername] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [shareName, setShareName] = useState("");
  const [shareUsername, setShareUsername] = useState("");
  const [shareAccess, setShareAccess] = useState<"read" | "write">("read");
  const [passwordEdits, setPasswordEdits] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    setError("");
    try {
      const result = await api.shares();
      setShares(result.shares);
      setUsers(result.users);
      setPermissions((current) => Object.fromEntries(result.shares.map((share) => [
        share.id, current[share.id] ?? share.permissions,
      ])));
      if (!shareUsername && result.users.length > 0) setShareUsername(result.users[0].username);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to load SMB configuration");
    } finally {
      setLoading(false);
    }
  }, [shareUsername]);
  useEffect(() => { void refresh(); }, [refresh]);

  async function createUser(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await api.createSMBUser(newUsername, newPassword, identity.csrfToken);
      setNewUsername("");
      setNewPassword("");
      setNotice("SMB account created. Its password is separate from ASNS sign-in.");
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to create SMB account");
    } finally {
      setBusy(false);
    }
  }

  async function createShare(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!shareUsername) {
      setError("Create an SMB account before creating a share.");
      return;
    }
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await api.createShare(shareName, [{ username: shareUsername, access: shareAccess }], identity.csrfToken);
      setShareName("");
      setNotice("SMB share created and Samba configuration reloaded.");
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to create SMB share");
    } finally {
      setBusy(false);
    }
  }

  function updatePermission(share: SMBShare, username: string, access: "read" | "write") {
    setPermissions((current) => {
      const values = current[share.id] ?? share.permissions;
      return { ...current, [share.id]: values.map((entry) => entry.username === username ? { ...entry, access } : entry) };
    });
  }

  async function savePermissions(share: SMBShare) {
    setBusy(true);
    setError("");
    try {
      await api.updateShare(share.id, share.name, permissions[share.id] ?? share.permissions, identity.csrfToken);
      setNotice(`Permissions saved for ${share.name}.`);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to update share permissions");
    } finally {
      setBusy(false);
    }
  }

  async function removePermission(share: SMBShare, username: string) {
    const updated = (permissions[share.id] ?? share.permissions).filter((entry) => entry.username !== username);
    setPermissions((current) => ({ ...current, [share.id]: updated }));
  }

  async function removeShare(share: SMBShare) {
    if (!window.confirm(`Remove the SMB share "${share.name}"? Its files will remain in ${share.path}.`)) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteShare(share.id, identity.csrfToken);
      setNotice(`Share ${share.name} removed; stored files were preserved.`);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to remove share");
    } finally {
      setBusy(false);
    }
  }

  async function updatePassword(user: SMBUser) {
    const password = passwordEdits[user.username] ?? "";
    setBusy(true);
    setError("");
    try {
      await api.updateSMBPassword(user.username, password, identity.csrfToken);
      setPasswordEdits((values) => ({ ...values, [user.username]: "" }));
      setNotice(`Password updated for ${user.username}.`);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to update SMB password");
    } finally {
      setBusy(false);
    }
  }

  async function removeUser(user: SMBUser) {
    if (!window.confirm(`Delete SMB account ${user.username}? It must first be removed from all shares.`)) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteSMBUser(user.username, identity.csrfToken);
      setNotice(`SMB account ${user.username} deleted.`);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to delete SMB account");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="management-stack">
      {error && <div className="error-banner" role="alert">{error}</div>}
      {notice && <div className="status-banner"><div className="status-symbol"><Check size={17} /></div>
        <div className="status-message"><strong>{notice}</strong></div></div>}
      {loading ? <div className="management-empty">Connecting to the restricted ASNS Agent…</div> : <>
        <section className="panel smb-accounts-panel">
          <div className="panel-heading"><div><h2><UserPlus size={16} /> SMB accounts</h2>
            <p>These credentials are separate from ASNS web accounts.</p></div></div>
          <form className="smb-user-create-form" onSubmit={(event) => void createUser(event)}>
            <label>SMB username<input value={newUsername} onChange={(event) => setNewUsername(event.target.value)}
              pattern="asns_[a-z0-9][a-z0-9_-]{0,26}" maxLength={32} placeholder="asns_media" required /></label>
            <label>Password<input type="password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)}
              minLength={12} maxLength={72} autoComplete="new-password" required /></label>
            <Button className="primary-button" disabled={busy || !identity.role}>Create SMB account</Button>
          </form>
          {users.length === 0 ? <div className="management-empty">No SMB accounts exist yet.</div> :
            <div className="smb-user-list">{users.map((user) => (
              <article className="smb-user-row" key={user.username}>
                <div><strong>{user.username}</strong><small>Created {new Date(user.createdAt).toLocaleDateString()}</small></div>
                <input type="password" minLength={12} maxLength={72} placeholder="New password"
                  aria-label={`New password for ${user.username}`} value={passwordEdits[user.username] ?? ""}
                  onChange={(event) => setPasswordEdits((values) => ({ ...values, [user.username]: event.target.value }))} />
                <Button variant="outline" className="outline-button" disabled={busy || (passwordEdits[user.username] ?? "").length < 12}
                  onClick={() => void updatePassword(user)}>Update password</Button>
                <Button variant="outline" className="outline-button danger-outline" disabled={busy} onClick={() => void removeUser(user)}>Delete</Button>
              </article>
            ))}</div>}
        </section>
        <section className="panel smb-shares-panel">
          <div className="panel-heading"><div><h2><Network size={16} /> Network shares</h2>
            <p>Shares expose only ASNS-created folders under <code>/srv/asns/shares</code>.</p></div></div>
          <form className="smb-share-create-form" onSubmit={(event) => void createShare(event)}>
            <label>Share name<input value={shareName} onChange={(event) => setShareName(event.target.value)}
              pattern="[A-Za-z0-9][A-Za-z0-9_-]{0,63}" maxLength={64} required /></label>
            <label>Account<select value={shareUsername} onChange={(event) => setShareUsername(event.target.value)} required>
              {users.map((user) => <option value={user.username} key={user.username}>{user.username}</option>)}
            </select></label>
            <label>Access<select value={shareAccess} onChange={(event) => setShareAccess(event.target.value as "read" | "write")}>
              <option value="read">Read only</option><option value="write">Read and write</option>
            </select></label>
            <Button className="primary-button" disabled={busy || users.length === 0}>Create share</Button>
          </form>
          {shares.length === 0 ? <div className="management-empty">No SMB shares are configured.</div> :
            <div className="smb-share-list">{shares.map((share) => (
              <article className="smb-share-card" key={share.id}>
                <div className="smb-share-heading"><div><strong>{share.name}</strong>
                  <small>Network path: \\&lt;NAS-address&gt;\{share.name} · Data path: {share.path}</small></div>
                  <Button variant="outline" className="outline-button danger-outline" disabled={busy} onClick={() => void removeShare(share)}>Remove share</Button>
                </div>
                <div className="smb-permissions">
                  {(permissions[share.id] ?? share.permissions).map((permission) => (
                    <div className="smb-permission-row" key={permission.username}>
                      <strong>{permission.username}</strong>
                      <select value={permission.access} onChange={(event) => updatePermission(share, permission.username, event.target.value as "read" | "write")}>
                        <option value="read">Read only</option><option value="write">Read and write</option>
                      </select>
                      <Button variant="outline" className="outline-button danger-outline" disabled={busy}
                        onClick={() => void removePermission(share, permission.username)}>Remove access</Button>
                    </div>
                  ))}
                  {users.some((user) => !(permissions[share.id] ?? share.permissions).some((permission) => permission.username === user.username)) && (
                    <AddSharePermission share={share} users={users} permissions={permissions[share.id] ?? share.permissions}
                      disabled={busy} onAdd={(username, access) => setPermissions((current) => ({
                        ...current, [share.id]: [...(current[share.id] ?? share.permissions), { username, access }],
                      }))} />
                  )}
                  <Button variant="outline" className="outline-button" disabled={busy} onClick={() => void savePermissions(share)}>Save permissions</Button>
                </div>
              </article>
            ))}</div>}
          <p className="quota-footnote">Removing a share removes its Samba definition but preserves the files. Network firewall and router exposure remain under your control.</p>
        </section>
      </>}
    </div>
  );
}

function AddSharePermission({ share, users, permissions, disabled, onAdd }: {
  share: SMBShare;
  users: SMBUser[];
  permissions: SMBPermission[];
  disabled: boolean;
  onAdd: (username: string, access: "read" | "write") => void;
}) {
  const [username, setUsername] = useState(users.find((user) => !permissions.some((permission) => permission.username === user.username))?.username ?? "");
  const [access, setAccess] = useState<"read" | "write">("read");
  const available = users.filter((user) => !permissions.some((permission) => permission.username === user.username));
  if (available.length === 0) return null;
  return <form className="smb-add-permission" onSubmit={(event) => {
    event.preventDefault();
    if (username) onAdd(username, access);
  }}>
    <select value={username} onChange={(event) => setUsername(event.target.value)}>
      {available.map((user) => <option key={user.username} value={user.username}>{user.username}</option>)}
    </select>
    <select value={access} onChange={(event) => setAccess(event.target.value as "read" | "write")}>
      <option value="read">Read only</option><option value="write">Read and write</option>
    </select>
    <Button variant="outline" className="outline-button" disabled={disabled}>Add access</Button>
    <span className="sr-only">Add SMB account permissions for {share.name}</span>
  </form>;
}

function BackupsPage({ identity }: { identity: Identity }) {
  const [settings, setSettings] = useState<BackupSettings>({
    enabled: false, intervalHours: 24, destination: "", lastStatus: "",
  });
  const [archives, setArchives] = useState<BackupArchive[]>([]);
  const [stage, setStage] = useState<RestoreStage | null>(null);
  const [restoreJob, setRestoreJob] = useState<RestoreJob | null>(null);
  const [confirmation, setConfirmation] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    setError("");
    try {
      const result = await api.backups();
      setSettings(result.settings);
      setArchives(result.archives);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to load backups");
    }
  }, []);
  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), 5000);
    return () => window.clearInterval(timer);
  }, [refresh]);
  useEffect(() => {
    if (!restoreJob || ["succeeded", "failed"].includes(restoreJob.status)) return;
    let active = true;
    let timer: number;
    const poll = async () => {
      try {
        const job = await api.backupRestoreStatus(restoreJob.id);
        if (!active) return;
        setRestoreJob(job);
        if (job.status === "succeeded") setNotice("Restore completed; ASNS has restarted.");
        if (job.status === "failed") setError(job.error || "Restore failed");
      } catch {
        if (active) setNotice("Waiting for ASNS to restart after applying the restore…");
      }
      if (active) timer = window.setTimeout(poll, 2000);
    };
    timer = window.setTimeout(poll, 1000);
    return () => { active = false; window.clearTimeout(timer); };
  }, [restoreJob]);

  async function saveSettings(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setNotice("");
    try {
      setSettings(await api.setBackupSettings(settings, identity.csrfToken));
      setNotice("Backup schedule saved.");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to save backup settings");
    } finally {
      setBusy(false);
    }
  }

  async function runBackup() {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await api.runBackup(identity.csrfToken);
      setNotice("Backup started. Its status will update here.");
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to start backup");
    } finally {
      setBusy(false);
    }
  }

  async function stageRestore(archive: BackupArchive) {
    setBusy(true);
    setError("");
    setNotice("");
    setRestoreJob(null);
    try {
      setStage(await api.stageBackupRestore(archive.name, identity.csrfToken));
      setConfirmation("");
      setNotice("Archive integrity and SQLite checks passed; restore is staged.");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Backup verification failed");
    } finally {
      setBusy(false);
    }
  }

  async function applyRestore() {
    if (!stage || confirmation !== "RESTORE") return;
    setBusy(true);
    setError("");
    try {
      const job = await api.applyBackupRestore(stage.id, confirmation, identity.csrfToken);
      setRestoreJob(job);
      setNotice("Restore queued. ASNS will briefly restart while the verified snapshot is applied.");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to apply restore");
    } finally {
      setBusy(false);
    }
  }

  async function deleteArchive(archive: BackupArchive) {
    if (!window.confirm(`Permanently delete backup ${archive.name}? This cannot be undone.`)) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteBackup(archive.name, identity.csrfToken);
      if (stage) setStage(null);
      setRestoreJob(null);
      setNotice(`Backup ${archive.name} deleted.`);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to delete backup");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="management-stack">
      {error && <div className="error-banner" role="alert">{error}</div>}
      {notice && <div className="status-banner"><div className="status-symbol"><Check size={17} /></div>
        <div className="status-message"><strong>{notice}</strong></div></div>}
      <section className="panel backup-settings-panel">
        <div className="panel-heading"><div><h2><Database size={16} /> Backup schedule</h2>
          <p>Archives include a consistent SQLite snapshot and ASNS-managed user files.</p></div></div>
        <form className="backup-settings-form" onSubmit={(event) => void saveSettings(event)}>
          <label className="backup-enabled"><input type="checkbox" checked={settings.enabled}
            onChange={(event) => setSettings({ ...settings, enabled: event.target.checked })} /> Enable scheduled backups</label>
          <label>Repeat every
            <input type="number" min="1" max="720" value={settings.intervalHours}
              onChange={(event) => setSettings({ ...settings, intervalHours: Number(event.target.value) })} required />
            <span>hours</span>
          </label>
          <label>Destination folder
            <input value={settings.destination} onChange={(event) => setSettings({ ...settings, destination: event.target.value })}
              placeholder="(ASNS backup root)" />
          </label>
          <div className="backup-settings-actions">
            <Button className="primary-button" disabled={busy}>Save schedule</Button>
            <Button type="button" variant="outline" className="outline-button" disabled={busy} onClick={() => void runBackup()}>
              <Database size={14} /> Run backup now
            </Button>
          </div>
          <small>Destination is a folder relative to the ASNS backup root. Mount external storage at <code>/var/lib/asns/backups</code> and ensure the ASNS service account can write to it. Archives are kept until an administrator deletes them.</small>
        </form>
        <div className="backup-status">
          <Detail label="Last run" value={settings.lastRunAt ? new Date(settings.lastRunAt).toLocaleString() : "Never"} />
          <Detail label="Status" value={settings.lastStatus || "Not run"} />
          <Detail label="Next run" value={settings.enabled && settings.nextRunAt ? new Date(settings.nextRunAt).toLocaleString() : "Schedule disabled"} />
        </div>
        {settings.lastError && <p className="error-text">{settings.lastError}</p>}
      </section>
      <section className="panel backup-history-panel">
        <div className="panel-heading"><div><h2>Backup history</h2><p>{archives.length} archives · integrity checked before restore</p></div>
          <Button variant="outline" className="outline-button" onClick={() => void refresh()}><RefreshCw size={14} /> Refresh</Button></div>
        {archives.length === 0 ? <div className="management-empty">No backups yet. Run one now or enable a schedule.</div> :
          <div className="backup-list">{archives.map((archive) => (
            <article className="backup-row" key={archive.name}>
              <div><strong>{archive.name}</strong><small>{new Date(archive.createdAt).toLocaleString()} · {formatBytes(archive.sizeBytes)}</small></div>
              <div className="backup-row-actions">
                <Button variant="outline" className="outline-button" disabled={busy} onClick={() => void stageRestore(archive)}>
                  <ShieldCheck size={14} /> Verify restore
                </Button>
                <Button variant="outline" className="outline-button danger-outline" disabled={busy} onClick={() => void deleteArchive(archive)}>
                  <Trash2 size={14} /> Delete
                </Button>
              </div>
            </article>
          ))}</div>}
      </section>
      {stage && <section className="panel restore-stage-panel">
        <div className="panel-heading"><div><h2>Verified restore staged</h2>
          <p>Snapshot time: {new Date(stage.createdAt).toLocaleString()} · {stage.fileCount} files · {formatBytes(stage.totalSizeBytes)}</p></div>
          <Button variant="outline" className="outline-button" onClick={() => { setStage(null); setRestoreJob(null); }}>Cancel</Button></div>
        <p>This will replace the active ASNS database and managed files. The service must restart to apply it. Type <strong>RESTORE</strong> to confirm.</p>
        {restoreJob && <p className={restoreJob.status === "failed" ? "error-text" : "restore-job-state"}>
          Restore status: {restoreJob.status}{restoreJob.error ? ` — ${restoreJob.error}` : ""}
        </p>}
        <div className="restore-confirm">
          <input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} aria-label="Type RESTORE to confirm" />
          <Button className="danger-button" disabled={busy || confirmation !== "RESTORE" || Boolean(restoreJob)} onClick={() => void applyRestore()}>
            Apply restore
          </Button>
        </div>
      </section>}
    </div>
  );
}

function FileManagerPage({ identity }: { identity: Identity }) {
  const [directory, setDirectory] = useState("");
  const [listing, setListing] = useState<StorageListing | null>(null);
  const [folderName, setFolderName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  const refresh = useCallback(async () => {
    setError("");
    try {
      setListing(await api.files(directory));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to load your storage");
    }
  }, [directory]);

  useEffect(() => { void refresh(); }, [refresh]);

  async function upload(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    if (!file) return;
    setBusy(true);
    setError("");
    try {
      await api.uploadFile(file, directory, identity.csrfToken);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Upload failed");
    } finally {
      setBusy(false);
      event.target.value = "";
    }
  }

  async function createFolder(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const name = folderName.trim();
    if (!name || name.includes("/") || name.includes("\\")) {
      setError("Enter a folder name without path separators.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api.createDirectory(directory ? `${directory}/${name}` : name, identity.csrfToken);
      setFolderName("");
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to create folder");
    } finally {
      setBusy(false);
    }
  }

  async function download(entry: StorageEntry) {
    try {
      const blob = await api.downloadFile(entry.path);
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = entry.name;
      link.click();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Download failed");
    }
  }

  async function remove(entry: StorageEntry) {
    if (!window.confirm(`Delete ${entry.name}? Only empty folders can be deleted.`)) return;
    setError("");
    try {
      await api.deleteFile(entry.path, identity.csrfToken);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to delete entry");
    }
  }

  const usedPercent = listing?.quotaBytes
    ? Math.min(100, listing.usedBytes / listing.quotaBytes * 100)
    : 0;

  return (
    <div className="management-stack">
      {error && <div className="error-banner" role="alert">{error}</div>}
      <section className="panel user-storage-summary">
        <div><span className="eyebrow">YOUR ASNS STORAGE</span>
          <strong>{listing ? formatBytes(listing.usedBytes) : "—"}</strong>
          <small>{listing?.quotaBytes ? `of ${formatBytes(listing.quotaBytes)} used` : "No storage limit"}</small>
        </div>
        <div className="quota-track"><span style={{ width: `${usedPercent}%` }} /></div>
      </section>
      <section className="panel file-manager-panel">
        <div className="panel-heading">
          <div><h2><Folder size={16} /> Personal files</h2>
            <p>Only you can access files in this account's private storage.</p></div>
          <Button variant="outline" className="outline-button" onClick={() => void refresh()}><RefreshCw size={14} /> Refresh</Button>
        </div>
        <div className="file-toolbar">
          <div className="file-breadcrumb">
            <button onClick={() => setDirectory("")}>My files</button>
            {directory && <><ChevronRight size={14} /><span>{directory}</span></>}
          </div>
          <div className="file-actions">
            <form onSubmit={(event) => void createFolder(event)}>
              <input aria-label="New folder name" value={folderName} onChange={(event) => setFolderName(event.target.value)}
                placeholder="Folder name" required />
              <Button variant="outline" className="outline-button" disabled={busy}><FolderPlus size={14} /> New folder</Button>
            </form>
            <input ref={fileInput} className="sr-only" type="file" onChange={(event) => void upload(event)} />
            <Button className="primary-button" disabled={busy} onClick={() => fileInput.current?.click()}>
              <FileUp size={14} /> {busy ? "Working…" : "Upload"}
            </Button>
          </div>
        </div>
        <div className="file-list">
          {directory && <button className="file-row file-parent" onClick={() => setDirectory(directory.split("/").slice(0, -1).join("/"))}>
            <Folder size={16} /><span>..</span><small>Parent folder</small>
          </button>}
          {listing?.entries.map((entry) => (
            <div className="file-row" key={entry.path}>
              {entry.directory ? <Folder size={16} /> : <Database size={16} />}
              {entry.directory
                ? <button className="file-name" onClick={() => setDirectory(entry.path)}>{entry.name}</button>
                : <button className="file-name" onClick={() => void download(entry)}>{entry.name}</button>}
              <small>{entry.directory ? "Folder" : formatBytes(entry.sizeBytes)}</small>
              {!entry.directory && <button className="file-action" aria-label={`Download ${entry.name}`}
                onClick={() => void download(entry)}><Download size={15} /></button>}
              <button className="file-action danger-action" aria-label={`Delete ${entry.name}`}
                onClick={() => void remove(entry)}><Trash2 size={15} /></button>
            </div>
          ))}
          {listing && listing.entries.length === 0 && <div className="management-empty">This folder is empty. Upload a file or create a folder to get started.</div>}
          {!listing && <div className="management-empty">Loading your files…</div>}
        </div>
        <p className="quota-footnote">Uploads are limited to 1 GiB per file and are blocked when they would exceed your assigned quota.</p>
      </section>
    </div>
  );
}

function App() {
  const [identity, setIdentity] = useState<Identity | null>(null);
  const [authReady, setAuthReady] = useState(false);
  const [authError, setAuthError] = useState("");
  const [snapshot, setSnapshot] = useState<SystemSnapshot | null>(null);
  const [disks, setDisks] = useState<Disk[]>([]);
  const [diskHealth, setDiskHealth] = useState<DiskHealth[]>([]);
  const [smartError, setSmartError] = useState("");
  const [raidArrays, setRaidArrays] = useState<RAIDArray[]>([]);
  const [raidError, setRaidError] = useState("");
  const [systemError, setSystemError] = useState("");
  const [page, setPage] = useState<Page>("Overview");
  const [simpleMode, setSimpleMode] = useState(() => storedBoolean("asns.simple-mode", true));
  const [lightMode, setLightMode] = useState(() => storedBoolean("asns.light-mode", false));
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [popover, setPopover] = useState<Popover>(null);
  const [search, setSearch] = useState("");
  const [notice, setNotice] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [cpuHistory, setCpuHistory] = useState<number[]>([]);
  const [memoryHistory, setMemoryHistory] = useState<number[]>([]);
  const searchInput = useRef<HTMLInputElement>(null);
  const refreshInFlight = useRef(false);

  useEffect(() => {
    let active = true;
    api.me().then((user) => {
      if (active) setIdentity(user);
    }).catch((error: unknown) => {
      if (!active) return;
      if (!(error instanceof Error) || error.message !== "authentication required") {
        setAuthError(error instanceof Error ? error.message : "Unable to connect to the ASNS API");
      }
    }).finally(() => {
      if (active) setAuthReady(true);
    });
    return () => { active = false; };
  }, []);

  useEffect(() => savePreference("asns.simple-mode", simpleMode), [simpleMode]);
  useEffect(() => savePreference("asns.light-mode", lightMode), [lightMode]);

  const refresh = useCallback(async () => {
    if (refreshInFlight.current) return;
    refreshInFlight.current = true;
    setRefreshing(true);
    try {
      const [systemResult, diskResult, smartResult, raidResult] = await Promise.allSettled([
        api.system(), api.disks(), api.diskHealth(), api.raidArrays(),
      ]);
      const errors: string[] = [];

      if (systemResult.status === "fulfilled") {
        const current = systemResult.value;
        const currentCpuPercent = current.cpuPercent;
        setSnapshot(current);
        setLastUpdated(new Date(current.collectedAt));
        if (currentCpuPercent !== null) {
          setCpuHistory((values) => [...values.slice(-19), currentCpuPercent]);
        }
        setMemoryHistory((values) => [...values.slice(-19), current.memory.percent]);
      } else {
        errors.push(systemResult.reason instanceof Error ? systemResult.reason.message : "System metrics are unavailable");
        if (systemResult.reason instanceof Error && systemResult.reason.message === "authentication required") {
          setIdentity(null);
        }
      }

      if (diskResult.status === "fulfilled") {
        setDisks(diskResult.value.disks);
      } else {
        errors.push(diskResult.reason instanceof Error ? diskResult.reason.message : "Disk discovery is unavailable");
        if (diskResult.reason instanceof Error && diskResult.reason.message === "authentication required") {
          setIdentity(null);
        }
      }
      if (smartResult.status === "fulfilled") {
        setDiskHealth(smartResult.value.disks);
        setSmartError("");
      } else {
        setSmartError(smartResult.reason instanceof Error ? smartResult.reason.message : "SMART information is unavailable");
      }
      if (raidResult.status === "fulfilled") {
        setRaidArrays(raidResult.value.arrays);
        setRaidError("");
      } else {
        setRaidError(raidResult.reason instanceof Error ? raidResult.reason.message : "RAID status is unavailable");
      }
      setSystemError(errors.join("; "));
    } finally {
      refreshInFlight.current = false;
      setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    if (!identity) return;
    let cancelled = false;
    let timer: number | undefined;
    const poll = async () => {
      await refresh();
      if (!cancelled) timer = window.setTimeout(poll, 3000);
    };
    void poll();
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [identity, refresh]);

  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setNotice(""), 5000);
    return () => window.clearTimeout(timer);
  }, [notice]);

  useEffect(() => {
    if (popover === "search") searchInput.current?.focus();
    function handleKey(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setPopover(null);
        setSidebarOpen(false);
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setPopover("search");
      }
    }
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  }, [popover]);

  const totalStorage = useMemo(
    () => disks.reduce((total, disk) => total + disk.capacityBytes, 0),
    [disks],
  );
  const filteredPages = useMemo(() => {
    const term = search.trim().toLowerCase();
    const availableWorkspacePages = workspacePages.filter((item) => item.label !== "Sharing" || identity?.role === "admin");
    const availableManagementPages = managementPages.filter((item) =>
      !["Users", "Backups"].includes(item.label) || identity?.role === "admin");
    return [...availableWorkspacePages, ...availableManagementPages].filter((item) =>
      item.label.toLowerCase().includes(term),
    );
  }, [identity?.role, search]);

  function navigate(nextPage: Page) {
    setPage(nextPage);
    setPopover(null);
    setSidebarOpen(false);
    setSearch("");
  }

  async function signOut() {
    if (!identity) return;
    try {
      await api.logout(identity.csrfToken);
      setIdentity(null);
      setPopover(null);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Unable to end this session");
    }
  }

  if (!authReady) {
    return <div className="loading-screen"><div className="spinner" />Connecting securely to ASNS…</div>;
  }
  if (authError) {
    return (
      <main className="login-screen">
        <section className="login-card" aria-labelledby="connection-title">
          <div className="brand-mark" aria-hidden="true">A</div>
          <p className="eyebrow">ASNS CONNECTION</p>
          <h1 id="connection-title">Server unavailable</h1>
          <p className="muted">{authError}</p>
          <Button className="primary-button wide" onClick={() => window.location.reload()}>Retry connection</Button>
        </section>
      </main>
    );
  }
  if (!identity) return <Login onLogin={setIdentity} />;

  const statusLabel = systemError
    ? "Some system information is unavailable"
    : snapshot ? "System monitoring is active" : "Collecting the first system sample";

  return (
    <div className={`app-shell ${lightMode ? "theme-light" : ""}`}>
      {sidebarOpen && (
        <button className="sidebar-backdrop" onClick={() => setSidebarOpen(false)} aria-label="Close navigation" />
      )}
      <aside className={`sidebar ${sidebarOpen ? "sidebar-open" : ""}`} aria-label="Main navigation">
        <div className="brand">
          <div className="brand-mark small" aria-hidden="true">A</div>
          <div><strong>ASNS</strong><span>STORAGE CONSOLE</span></div>
          <button className="icon-button mobile-close" onClick={() => setSidebarOpen(false)} aria-label="Close navigation">
            <X size={17} />
          </button>
        </div>

        <div className="server-switch">
          <div className="server-avatar"><Database size={16} /></div>
          <div className="server-label">
            <strong>NAS server</strong>
            <span><i className={`status-dot ${systemError ? "status-dot-error" : ""}`} />
              {systemError ? "Needs attention" : snapshot ? "Monitoring active" : "Connecting…"}
            </span>
          </div>
          <span className="server-version">v0.1.0</span>
        </div>

        <div className="nav-caption">WORKSPACE</div>
        <nav aria-label="Workspace">
          {workspacePages.filter((item) => item.label !== "Sharing" || identity.role === "admin").map(({ label, icon: Icon }) => (
            <NavButton key={label} label={label} icon={<Icon size={17} />} active={page === label}
              onClick={() => navigate(label)} />
          ))}
        </nav>
        <div className="nav-caption manage-caption">MANAGE</div>
        <nav aria-label="Management">
          {managementPages.filter((item) => !["Users", "Backups"].includes(item.label) || identity.role === "admin").map(({ label, icon: Icon }) => (
            <NavButton key={label} label={label} icon={<Icon size={17} />} active={page === label}
              onClick={() => navigate(label)} />
          ))}
        </nav>

        <div className="sidebar-bottom">
          <div className="help-card">
            <div className="help-icon"><CircleHelp size={17} /></div>
            <strong>Built for your home lab</strong>
            <p>ASNS runs on your existing Linux server. It never replaces your operating system.</p>
            <button onClick={() => setNotice("Installation and development instructions are in the ASNS README.")}>
              Getting started <ChevronRight size={14} />
            </button>
          </div>
          <button className="nav-item settings-button" onClick={() => setNotice("System settings are not implemented in ASNS 0.1.0.")}>
            <Settings size={17} /><span>System settings</span>
          </button>
          <div className="sidebar-version">Another Sharperr NAS Software<span className="version-badge">BETA</span></div>
        </div>
      </aside>

      <main className="main-area">
        <header className="topbar">
          <button className="icon-button menu-button md:hidden" onClick={() => setSidebarOpen(true)}
            aria-label="Open navigation"><Menu size={19} /></button>
          <div className="breadcrumb"><span>Workspace</span><ChevronRight size={14} /><strong>{page}</strong></div>

          <div className="topbar-right">
            <div className="mode-toggle" aria-label="Interface detail">
              <span className={simpleMode ? "toggle-active" : ""}>Simple</span>
              <button type="button" className={`toggle ${simpleMode ? "" : "toggle-on"}`}
                aria-label={`Switch to ${simpleMode ? "advanced" : "simple"} mode`}
                aria-pressed={!simpleMode} onClick={() => setSimpleMode((value) => !value)}>
                <i />
              </button>
              <span className={!simpleMode ? "toggle-active" : ""}>Advanced</span>
            </div>
            <span className="topbar-divider" />

            <div className="popover-anchor">
              <button className="icon-button search-button" aria-label="Search navigation"
                aria-expanded={popover === "search"}
                onClick={() => setPopover(popover === "search" ? null : "search")}>
                <Search size={18} />
              </button>
              {popover === "search" && (
                <div className="header-popover search-popover">
                  <label htmlFor="navigation-search">Find a workspace</label>
                  <input id="navigation-search" ref={searchInput} value={search}
                    onChange={(event) => setSearch(event.target.value)} placeholder="Search pages…" />
                  <div className="search-results">
                    {filteredPages.length ? filteredPages.map(({ label, icon: Icon }) => (
                      <button key={label} onClick={() => navigate(label)}>
                        <Icon size={15} /><span>{label}</span><ChevronRight size={14} />
                      </button>
                    )) : <p>No matching workspace.</p>}
                  </div>
                        <span className="popover-hint">Press Esc to close · Ctrl K to search</span>
                </div>
              )}
            </div>

            <div className="popover-anchor">
              <button className="icon-button notification-button" aria-label="Notifications"
                aria-expanded={popover === "notifications"}
                onClick={() => setPopover(popover === "notifications" ? null : "notifications")}>
                <Bell size={18} />
              </button>
              {popover === "notifications" && (
                <div className="header-popover notification-popover">
                  <div className="popover-title"><strong>Notifications</strong><span>ASNS 0.1.0</span></div>
                  <div className="notification-empty"><Bell size={20} /><strong>No notifications yet</strong>
                    <p>System alerts are not available in this foundation release.</p>
                  </div>
                </div>
              )}
            </div>

            <button className="icon-button theme-button" aria-label={lightMode ? "Switch to dark mode" : "Switch to light mode"}
              title={lightMode ? "Switch to dark mode" : "Switch to light mode"}
              onClick={() => setLightMode((value) => !value)}>
              {lightMode ? <Moon size={17} /> : <Sun size={17} />}
            </button>
            <span className="topbar-divider" />

            <div className="popover-anchor profile-anchor">
              <button className="profile-button" aria-expanded={popover === "profile"}
                onClick={() => setPopover(popover === "profile" ? null : "profile")}>
                <span className="profile-avatar">{identity.username.slice(0, 1).toUpperCase()}</span>
                <span className="profile-name">{identity.username}<small>{identity.role === "admin" ? "Administrator" : "Standard account"}</small></span>
                <ChevronDown size={14} className="profile-chevron" />
              </button>
              {popover === "profile" && (
                <div className="header-popover profile-popover">
                  <div className="profile-summary"><span className="profile-avatar">{identity.username.slice(0, 1).toUpperCase()}</span>
                    <span><strong>{identity.username}</strong><small>{identity.role === "admin" ? "Administrator account" : "Standard account"}</small></span>
                  </div>
                  <button className="signout-button" onClick={() => void signOut()}><LogOut size={15} /> Sign out</button>
                </div>
              )}
            </div>
          </div>
        </header>

        <div className="content">
          <div className="page-heading">
            <div>
              <div className="eyebrow">{page === "Overview" ? "YOUR SERVER AT A GLANCE" : "ASNS WORKSPACE"}</div>
              <h1>{page === "Overview" ? "Good to see you." : page}</h1>
              <p>{page === "Overview"
                ? "A clear picture of what's happening on your NAS."
                : page === "Storage"
                  ? "Physical block devices discovered on this Linux host."
                  : page === "Monitoring"
                    ? "Live system telemetry and seven days of persisted host history."
                  : page === "Users"
                    ? "Create accounts and manage storage limits."
                    : page === "File manager"
                      ? "Manage files in your private, quota-limited ASNS storage."
                  : `${page} is part of the ASNS roadmap.`}</p>
            </div>
            {page === "Overview" && (
              <Button variant="outline" className="outline-button refresh-button" onClick={() => void refresh()}
                disabled={refreshing} aria-label="Refresh system information">
                <RefreshCw size={15} className={refreshing ? "refresh-spinning" : ""} />
                <span>{refreshing ? "Updating" : "Refresh"}</span>
              </Button>
            )}
          </div>

          {systemError && (
            <div className="error-banner" role="status">
              <span>{systemError}</span>
              <button onClick={() => void refresh()} disabled={refreshing}>Retry</button>
            </div>
          )}

          {page === "Storage" && (
            <StoragePage disks={disks} health={diskHealth} smartError={smartError} raidArrays={raidArrays}
              raidError={raidError} error={systemError} onRefresh={() => void refresh()} />
          )}
          {page === "Users" && identity.role === "admin" && <UserManagementPage identity={identity} />}
          {page === "File manager" && <FileManagerPage identity={identity} />}
          {page === "Monitoring" && <MonitoringPage />}
          {page === "Sharing" && identity.role === "admin" && <SharingPage identity={identity} />}
          {page === "Backups" && identity.role === "admin" && <BackupsPage identity={identity} />}
          {page !== "Overview" && page !== "Storage" && page !== "Users" && page !== "File manager" && page !== "Monitoring" &&
            <ComingSoonPage page={page} />}

          {page === "Overview" && (
            <>
              <section className={`status-banner ${systemError ? "status-banner-warning" : ""}`}>
                <div className="status-symbol"><ShieldCheck size={19} /></div>
                <div className="status-message">
                  <strong>{statusLabel}</strong>
                  <p>{snapshot
                    ? "Live host data, read directly from Linux. No demonstration metrics."
                    : "Waiting for the first live host reading."}</p>
                </div>
                <div className="system-meta">
                  <span><i className={`status-dot ${systemError ? "status-dot-error" : snapshot ? "" : "status-dot-idle"}`} />
                    {systemError ? "CHECK DATA" : snapshot ? "LIVE DATA" : "CONNECTING"}
                  </span>
                  <span>UPTIME <b>{snapshot ? formatDuration(snapshot.uptimeSeconds) : "—"}</b></span>
                </div>
              </section>

              <section className="metric-grid" aria-label="System metrics">
                <MetricCard title="Processor" icon={<Cpu size={17} />}
                  value={snapshot?.cpuPercent == null ? "—" : `${snapshot.cpuPercent.toFixed(1)}%`}
                  detail={snapshot?.cpuPercent == null ? "Waiting for CPU sample" : "Current processor utilization"}
                  percent={snapshot?.cpuPercent ?? 0} tone="blue" history={cpuHistory}
                  help="CPU usage is calculated from Linux processor time counters." simpleMode={simpleMode}
                  explanation="How much of your server's processor is busy right now." />
                <MetricCard title="Memory" icon={<Activity size={17} />}
                  value={snapshot ? `${snapshot.memory.percent.toFixed(1)}%` : "—"}
                  detail={snapshot ? `${formatBytes(snapshot.memory.usedBytes)} of ${formatBytes(snapshot.memory.totalBytes)} in use` : "Waiting for memory data"}
                  percent={snapshot?.memory.percent ?? 0} tone="purple" history={memoryHistory}
                  help="Memory in use is calculated from total RAM minus Linux MemAvailable." simpleMode={simpleMode}
                  explanation="The share of memory your server is currently using." />
                <MetricCard title="Physical storage" icon={<HardDrive size={17} />}
                  value={disks.length ? formatBytes(totalStorage) : "—"}
                  detail={disks.length ? `${disks.length} ${disks.length === 1 ? "device" : "devices"} · raw capacity` : "No physical devices detected"}
                  percent={0} tone="green" help="Raw device capacity, not filesystem usage or free space."
                  simpleMode={simpleMode} explanation="Drive size only; free space is not available yet." />
                <MetricCard title="Network receive" icon={<Network size={17} />}
                  value={snapshot ? `${formatBytes(snapshot.network.receiveBytesPerSecond)}/s` : "—"}
                  detail={snapshot ? `Transmit ${formatBytes(snapshot.network.sendBytesPerSecond)}/s` : "Waiting for network counters"}
                  percent={0} tone="amber" help="Combined inbound and outbound traffic across non-loopback interfaces."
                  simpleMode={simpleMode} explanation="Data arriving at your server each second." />
              </section>

              <section className="lower-grid">
                <div className="panel performance-panel">
                  <div className="panel-heading">
                    <div><h2>System performance</h2>
                      <p>{simpleMode ? "How busy your processor and memory have been." : "One-minute history · sampled every 3 seconds"}</p>
                    </div>
                    <span className={`live-tag ${snapshot ? "" : "live-tag-idle"}`}>
                      <i />{snapshot ? "LIVE" : "WAITING"}
                    </span>
                  </div>
                  <div className="chart-legend">
                    <span><i className="legend-cpu" /> Processor</span>
                    <span><i className="legend-memory" /> Memory</span>
                    <span className="chart-window">Last minute</span>
                  </div>
                  <div className="chart-wrap">
                    {cpuHistory.length < 2 && memoryHistory.length < 2
                      ? <div className="chart-empty">Live samples appear here as ASNS collects them.</div>
                      : <PerformanceChart cpu={cpuHistory} memory={memoryHistory} />}
                  </div>
                  <div className="chart-axis"><span>60s ago</span><span>45s</span><span>30s</span><span>15s</span><span>Now</span></div>
                </div>

                <div className="panel storage-panel">
                  <div className="panel-heading">
                    <div><h2>Physical disks</h2><p>Devices detected on this host</p></div>
                    <button className="panel-action" onClick={() => navigate("Storage")}>Details <ChevronRight size={14} /></button>
                  </div>
                  {disks.length ? (
                    <div className="disk-list">
                      {disks.slice(0, 4).map((disk) => (
                        <div className="disk-row" key={disk.name}>
                          <div className="disk-icon"><HardDrive size={17} /></div>
                          <div className="disk-info"><strong>{disk.model || disk.name}</strong>
                            <span>{disk.name} · {disk.serial ? `S/N ${disk.serial}` : "Serial not exposed"}</span>
                          </div>
                          <b>{formatBytes(disk.capacityBytes)}</b>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="empty-state"><HardDrive size={22} />
                      <span>{systemError ? "Disk information is unavailable" : "No physical disks detected"}</span>
                      <small>Virtual machines may not expose underlying drives.</small>
                    </div>
                  )}
                  <div className="storage-foot">
                    <span><span className="health-dot" /> Discovered through Linux sysfs</span>
                    <span>{disks.length} {disks.length === 1 ? "device" : "devices"}</span>
                  </div>
                </div>
              </section>

              {!simpleMode && snapshot && (
                <section className="panel advanced-panel">
                  <div className="panel-heading">
                    <div><h2>Host details</h2><p>Additional live metrics for troubleshooting</p></div>
                    <span className="subtle-caption">PROCFS / SYSFS</span>
                  </div>
                  <div className="advanced-grid">
                    <Detail label="CPU utilization" value={snapshot.cpuPercent === null ? "Sampling…" : `${snapshot.cpuPercent.toFixed(2)}%`} />
                    <Detail label="Memory available" value={`${formatBytes(snapshot.memory.availableBytes)} / ${formatBytes(snapshot.memory.totalBytes)}`} />
                    <Detail label="Network receive" value={`${formatBytes(snapshot.network.receiveBytesPerSecond)}/s`} />
                    <Detail label="Network transmit" value={`${formatBytes(snapshot.network.sendBytesPerSecond)}/s`} />
                    <Detail label="System uptime" value={formatDuration(snapshot.uptimeSeconds)} />
                    <Detail label="Last sample" value={lastUpdated?.toLocaleTimeString() ?? "Unavailable"} />
                  </div>
                </section>
              )}

              <section className="panel activity-panel">
                <div className="panel-heading">
                  <div><h2>System status</h2><p>Connection and monitoring status</p></div>
                  <span className="subtle-caption">{lastUpdated ? `UPDATED ${lastUpdated.toLocaleTimeString()}` : "AWAITING FIRST SAMPLE"}</span>
                </div>
                <div className="activity-row">
                  <div className={`activity-icon ${snapshot && !systemError ? "" : "activity-unavailable"}`}>
                    {snapshot && !systemError ? <Check size={16} /> : <ShieldCheck size={16} />}
                  </div>
                  <div className="activity-copy">
                    <strong>{systemError ? "Some host information could not be read" : snapshot ? "Connected to the ASNS service" : "Waiting for the ASNS service"}</strong>
                    <span>{systemError || (snapshot
                      ? "Metrics are collected from Linux procfs and sysfs; no privileged operation is performed."
                      : "ASNS will show live system metrics after the first successful sample.")}</span>
                  </div>
                  <time>{refreshing ? "Updating…" : "Every 3 seconds"}</time>
                </div>
              </section>

              <footer className="page-footer">
                <span>ASNS <b>0.1.0</b> · Self-hosted NAS management</span>
                <span><span className={`health-dot ${systemError ? "status-dot-error" : snapshot ? "" : "status-dot-idle"}`} />
                  {systemError ? "Partial connection" : snapshot ? "Service connected" : "Connecting"}
                </span>
              </footer>
            </>
          )}
        </div>
      </main>

      {notice && <div className="toast" role="status" aria-live="polite">
        <span>{notice}</span><button onClick={() => setNotice("")} aria-label="Dismiss notification"><X size={15} /></button>
      </div>}
    </div>
  );
}

function NavButton({ label, icon, active, onClick }: {
  label: Page;
  icon: React.ReactNode;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button className={`nav-item ${active ? "selected" : ""}`} onClick={onClick}
      aria-current={active ? "page" : undefined}>
      {icon}<span>{label}</span>{active && <span className="nav-active-dot" aria-hidden="true" />}
    </button>
  );
}

function MetricCard({ title, icon, value, detail, percent, tone, history, help, simpleMode, explanation }: {
  title: string;
  icon: React.ReactNode;
  value: string;
  detail: string;
  percent: number;
  tone: Tone;
  history?: number[];
  help: string;
  simpleMode: boolean;
  explanation: string;
}) {
  const measured = title === "Processor" || title === "Memory";
  const hasHistory = history !== undefined && history.length > 1;
  return (
    <article className={`metric-card tone-${tone}`} title={help}>
      <div className="metric-top">
        <span className="metric-title">{title}</span><span className="metric-icon">{icon}</span>
      </div>
      <div className="metric-value">{value}</div>
      <div className="metric-detail">{detail}</div>
      {simpleMode && <p className="metric-explanation">{explanation}</p>}
      <div className="metric-bottom">
        {hasHistory
          ? <MiniChart values={history} />
          : <div className={`progress-track ${measured ? "progress-idle" : "progress-neutral"}`}>
            <span style={{ width: `${Math.min(100, Math.max(0, percent))}%` }} />
          </div>}
        <span>{hasHistory ? "LIVE" : measured && value !== "—" ? `${Math.round(percent)}%` : "HOST"}</span>
      </div>
    </article>
  );
}

function MiniChart({ values }: { values: number[] }) {
  const points = values.map((value, index) =>
    `${(index / Math.max(1, values.length - 1)) * 100},${30 - Math.max(0, Math.min(100, value)) * 0.28}`,
  ).join(" ");
  return (
    <svg className="mini-chart" viewBox="0 0 100 32" preserveAspectRatio="none" aria-hidden="true">
      <polyline points={points} fill="none" stroke="currentColor" strokeWidth="2" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

function PerformanceChart({ cpu, memory }: { cpu: number[]; memory: number[] }) {
  function points(values: number[]) {
    return values.map((value, index) =>
      `${(index / Math.max(1, values.length - 1)) * 600},${190 - Math.max(0, Math.min(100, value)) * 1.7}`,
    ).join(" ");
  }
  return (
    <svg className="performance-chart" viewBox="0 0 600 200" preserveAspectRatio="none"
      role="img" aria-label="One-minute live processor and memory utilization">
      {[20, 70, 120, 170].map((y) => <line key={y} x1="0" x2="600" y1={y} y2={y} />)}
      {cpu.length > 1 && <polyline className="chart-cpu" points={points(cpu)} />}
      {memory.length > 1 && <polyline className="chart-memory" points={points(memory)} />}
    </svg>
  );
}

function StoragePage({ disks, health, smartError, raidArrays, raidError, error, onRefresh }: {
  disks: Disk[];
  health: DiskHealth[];
  smartError: string;
  raidArrays: RAIDArray[];
  raidError: string;
  error: string;
  onRefresh: () => void;
}) {
  const capacity = disks.reduce((total, disk) => total + disk.capacityBytes, 0);
  const critical = health.some((disk) => disk.healthy === false);
  const checked = health.length > 0 && health.every((disk) => disk.healthy !== undefined);
  const overallHealth = critical ? "Critical" : checked ? "Healthy" : smartError ? "Unavailable" : "Unknown";
  return (
    <>
    <section className="panel full-storage-panel">
      <div className="panel-heading">
        <div><h2>Physical disks</h2>
          <p>Live Linux sysfs discovery. Raw device size is not filesystem free space.</p>
        </div>
        <Button variant="outline" className="outline-button" onClick={onRefresh}><RefreshCw size={14} /> Refresh</Button>
      </div>
      <div className="storage-summary">
        <div><span>DEVICES</span><strong>{disks.length}</strong></div>
        <div><span>RAW CAPACITY</span><strong>{disks.length ? formatBytes(capacity) : "—"}</strong></div>
        <div><span>HEALTH</span><strong className={critical ? "critical-health" : checked ? "healthy-health" : "unknown-health"}>{overallHealth}</strong></div>
      </div>
      {disks.length ? (
        <div className="full-disk-list">
          {disks.map((disk) => {
            const smart = health.find((item) => item.device === disk.name);
            return (
              <article className="full-disk-row" key={disk.name}>
                <div className="disk-icon"><HardDrive size={17} /></div>
                <div className="full-disk-info"><strong>{disk.model || disk.name}</strong>
                  <span>{disk.name} · {disk.serial ? `Serial ${disk.serial}` : "Serial not exposed"} · {disk.removable ? "Removable device" : "Fixed device"}</span>
                  <small className={smart?.healthy === false ? "critical-health" : smart?.healthy === true ? "healthy-health" : "unknown-health"}>
                    SMART {smart?.healthy === undefined ? (smart?.error || smartError || "Not reported") : smart.healthy ? "Healthy" : "Critical"}
                    {smart?.temperatureC !== undefined ? ` · ${smart.temperatureC}°C` : ""}
                  </small>
                </div>
                <span className="disk-capacity"><b>{formatBytes(disk.capacityBytes)}</b><small>raw capacity</small></span>
              </article>
            );
          })}
        </div>
      ) : (
        <div className="empty-state"><HardDrive size={22} />
          <span>{error ? "Disk information is unavailable" : "No physical disks were detected"}</span>
          <small>{error || "Virtual machines may not expose underlying physical drives."}</small>
        </div>
      )}
      <div className="storage-disclaimer">
        <ShieldCheck size={15} />
        {smartError ? `SMART data unavailable: ${smartError}` : "SMART status and temperature are reported only when supported by the device and smartmontools."}
      </div>
    </section>
    <section className="panel full-storage-panel">
      <div className="panel-heading"><div><h2>Software RAID arrays</h2>
        <p>Read-only status from Linux md and mdadm. ASNS does not create, reshape, or alter arrays.</p></div>
        <Button variant="outline" className="outline-button" onClick={onRefresh}><RefreshCw size={14} /> Refresh</Button>
      </div>
      {raidError ? <div className="empty-state"><HardDrive size={22} />
        <span>RAID status is unavailable</span><small>{raidError}</small>
      </div> : raidArrays.length ? (
        <div className="full-disk-list">
          {raidArrays.map((array) => {
            const stateClass = array.state === "healthy" ? "healthy-health" :
              array.state === "degraded" ? "critical-health" : "unknown-health";
            return <article className="full-disk-row" key={array.name}>
              <div className="disk-icon"><HardDrive size={17} /></div>
              <div className="full-disk-info"><strong>/dev/{array.name} · {array.level}</strong>
                <span>Members: {array.devices.length ? array.devices.join(", ") : "not reported"}</span>
                <small className={stateClass}>
                  {array.state} · {array.activeDevices}/{array.raidDevices} active
                  {array.degraded > 0 ? ` · ${array.degraded} missing or failed` : ""}
                  {array.mdadmState ? ` · mdadm: ${array.mdadmState}` : ""}
                  {array.detailError ? ` · ${array.detailError}` : ""}
                </small>
              </div>
              {array.syncAction && <span className="disk-capacity">
                <b>{array.syncPercent === undefined ? array.syncAction : `${array.syncPercent.toFixed(1)}%`}</b>
                <small>{array.syncAction}</small>
              </span>}
            </article>;
          })}
        </div>
      ) : <div className="empty-state"><HardDrive size={22} />
        <span>No mdadm RAID arrays were detected</span><small>Existing software RAID arrays appear here; this page does not create or modify arrays.</small>
      </div>}
    </section>
    </>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return <div><span>{label}</span><strong>{value}</strong></div>;
}

function ComingSoonPage({ page }: { page: Page }) {
  const descriptions: Record<Page, string> = {
    Overview: "",
    Storage: "",
    "File manager": "Browse, upload, download, and organize files stored on your NAS.",
    Sharing: "Configure SMB shares and manage access permissions.",
    Applications: "Docker application installation is intentionally not included in this implementation.",
    Monitoring: "Review live CPU, memory, and network samples collected directly from Linux.",
    Users: "Manage local ASNS accounts, roles, and active sessions.",
    Backups: "Configure destinations, scheduled jobs, and restore points.",
  };
  return (
    <section className="coming-soon">
      <div className="coming-icon"><SlidersHorizontal size={22} /></div>
      <span className="eyebrow">{page === "Applications" ? "DEFERRED BY REQUEST" : "PLANNED FOR A FUTURE RELEASE"}</span>
      <h2>{page} is not implemented yet</h2>
      <p>{descriptions[page]}</p>
      <p className="coming-note">This section does not display mock data or issue system commands.</p>
    </section>
  );
}

export default App;

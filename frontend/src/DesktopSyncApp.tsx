import { useCallback, useEffect, useMemo, useState } from "react";
import { Activity, Check, ChevronRight, Folder, HardDrive, Plus, RefreshCw, ShieldCheck, Trash2 } from "lucide-react";
import { Button } from "./components/ui/button";
import type { SyncAction, SyncProfile, SyncResult } from "./types/desktop";
import "./desktop-sync.css";

const intervals = [
  { value: 0, label: "Manual only" },
  { value: 5, label: "Every 5 minutes" },
  { value: 15, label: "Every 15 minutes" },
  { value: 60, label: "Every hour" },
  { value: 360, label: "Every 6 hours" },
  { value: 1440, label: "Daily" },
];

function createProfile(index: number): SyncProfile {
  return {
    id: crypto.randomUUID(),
    name: `Sync profile ${index}`,
    serverUrl: "",
    username: "",
    localFolder: "",
    remoteFolder: "",
    intervalMinutes: 0,
    includePatterns: [],
    excludePatterns: [],
    syncDeletes: true,
  };
}

function fromLines(value: string): string[] {
  return value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
}

function actionSummary(actions: SyncAction[]) {
  return actions.slice(0, 10).map((action) => `${action.type}: ${action.path}`).join("\n");
}

export default function DesktopSyncApp() {
  const [profiles, setProfiles] = useState<SyncProfile[]>([]);
  const [selectedId, setSelectedId] = useState("");
  const [connected, setConnected] = useState<Record<string, string>>({});
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [includeText, setIncludeText] = useState("");
  const [excludeText, setExcludeText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("Create a profile, sign in, then start a sync.");
  const [lastResult, setLastResult] = useState<SyncResult | null>(null);

  const selected = useMemo(() => profiles.find((profile) => profile.id === selectedId) ?? null, [profiles, selectedId]);
  const save = useCallback(async (next: SyncProfile[]) => {
    await window.asnsDesktop.saveProfiles(next);
    setProfiles(next);
  }, []);

  useEffect(() => {
    let active = true;
    window.asnsDesktop.loadProfiles().then((items) => {
      if (!active) return;
      setProfiles(items);
      setSelectedId(items[0]?.id ?? "");
    }).catch((reason: unknown) => {
      if (active) setError(reason instanceof Error ? reason.message : "Unable to load profiles");
    });
    const unsubscribe = window.asnsDesktop.onSyncEvent((event) => {
      if (active && (event.profileId === selectedId || !event.profileId)) setNotice(event.message);
    });
    return () => { active = false; unsubscribe(); };
  }, [selectedId]);

  useEffect(() => {
    if (!selected) {
      setIncludeText("");
      setExcludeText("");
      return;
    }
    setIncludeText(selected.includePatterns.join("\n"));
    setExcludeText(selected.excludePatterns.join("\n"));
    setUsername(selected.username);
    window.asnsDesktop.authStatus(selected).then((identity) => {
      setConnected((current) => ({ ...current, [selected.id]: identity.username }));
    }).catch(() => {
      setConnected((current) => {
        const next = { ...current };
        delete next[selected.id];
        return next;
      });
    });
  }, [selected?.id]);

  function updateSelected(changes: Partial<SyncProfile>) {
    if (!selected) return;
    if (changes.serverUrl !== undefined && changes.serverUrl !== selected.serverUrl) {
      setConnected((current) => {
        const next = { ...current };
        delete next[selected.id];
        return next;
      });
    }
    setProfiles((current) => current.map((profile) => profile.id === selected.id ? { ...profile, ...changes } : profile));
  }

  async function persistSelected(): Promise<boolean> {
    if (!selected) return false;
    const next = profiles.map((profile) => profile.id === selected.id
      ? { ...profile, includePatterns: fromLines(includeText), excludePatterns: fromLines(excludeText) }
      : profile);
    try {
      await save(next);
      setError("");
      setNotice("Profile settings saved.");
      return true;
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to save profile");
      return false;
    }
  }

  async function addProfile() {
    const profile = createProfile(profiles.length + 1);
    const next = [...profiles, profile];
    await save(next);
    setSelectedId(profile.id);
    setNotice("Profile created. Choose your local folder and NAS destination.");
    setError("");
  }

  async function removeProfile() {
    if (!selected || !window.confirm(`Remove profile "${selected.name}"? Files already synced on either side are not removed.`)) return;
    const next = profiles.filter((profile) => profile.id !== selected.id);
    await save(next);
    setConnected((current) => {
      const copy = { ...current };
      delete copy[selected.id];
      return copy;
    });
    setSelectedId(next[0]?.id ?? "");
    setNotice("Profile removed. Synced files were left untouched.");
  }

  async function chooseFolder() {
    const folder = await window.asnsDesktop.chooseFolder();
    if (folder) updateSelected({ localFolder: folder });
  }

  async function signIn(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selected) return;
    setBusy(true);
    setError("");
    try {
      const result = await window.asnsDesktop.login(selected, username.trim(), password);
      const next = profiles.map((profile) => profile.id === selected.id
        ? { ...profile, username: result.username }
        : profile);
      await save(next);
      setConnected((current) => ({ ...current, [selected.id]: result.username }));
      setPassword("");
      setNotice(`Connected to ${selected.serverUrl} as ${result.username}.`);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to sign in");
    } finally {
      setBusy(false);
    }
  }

  async function signOut() {
    if (!selected) return;
    setBusy(true);
    try {
      await window.asnsDesktop.logout(selected);
      setConnected((current) => {
        const next = { ...current };
        delete next[selected.id];
        return next;
      });
      setNotice("Signed out. The password was not saved.");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to sign out");
    } finally {
      setBusy(false);
    }
  }

  async function syncNow() {
    if (!selected) return;
    setBusy(true);
    setError("");
    setLastResult(null);
    try {
      if (!await persistSelected()) return;
      let result = await window.asnsDesktop.sync(selected.id);
      for (let attempt = 0; result.status === "needs-confirmation" && attempt < 3; attempt += 1) {
        const deletions = result.actions.filter((action) => action.type === "deleteLocal" || action.type === "deleteRemote");
        const details = actionSummary(deletions);
        const more = deletions.length > 10 ? `\n…and ${deletions.length - 10} more` : "";
        if (!window.confirm(`${deletions.length} file deletion(s) will be propagated. Recovery copies are created before deletion.\n\n${details}${more}\n\nContinue?`)) {
          setNotice("Sync cancelled; no deletions were applied.");
          setLastResult(result);
          return;
        }
        result = await window.asnsDesktop.sync(selected.id, true, result.deletionToken);
      }
      setLastResult(result);
      setNotice(result.status === "completed"
        ? `Sync completed with ${result.actions.length} file operation(s).`
        : "Files changed during confirmation. Review the updated deletion list and run sync again.");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Sync failed");
    } finally {
      setBusy(false);
    }
  }

  async function openRecovery() {
    if (!selected) return;
    try {
      await window.asnsDesktop.openRecovery(selected.id);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Unable to open recovery folder");
    }
  }

  return (
    <div className="desktop-sync-shell">
      <header className="desktop-sync-header">
        <div className="brand-mark" aria-hidden="true">A</div>
        <div><strong>ASNS Sync</strong><span>PC ↔ NAS FOLDER SYNC</span></div>
        <span className="desktop-sync-platform">Windows · Linux</span>
      </header>
      <div className="desktop-sync-body">
        <aside className="desktop-profile-list">
          <div className="desktop-profile-heading"><strong>Profiles</strong>
            <Button className="icon-button" aria-label="Add sync profile" onClick={() => void addProfile()}><Plus size={16} /></Button>
          </div>
          {profiles.map((profile) => <button key={profile.id}
            className={`desktop-profile-item ${selectedId === profile.id ? "active" : ""}`}
            onClick={() => setSelectedId(profile.id)}>
            <Folder size={16} /><span>{profile.name || "Untitled profile"}</span>
            {connected[profile.id] && <i className="status-dot" />}
          </button>)}
          {!profiles.length && <p className="desktop-empty-hint">Add a profile to get started.</p>}
          <div className="desktop-security-note"><ShieldCheck size={16} />
            <span>Passwords are never saved. Session cookies stay in this app’s private profile.</span></div>
        </aside>
        <main className="desktop-sync-main">
          {!selected ? <div className="desktop-welcome"><HardDrive size={36} /><h1>Sync selected folders</h1>
            <p>Create a profile to connect your PC folder with ASNS storage.</p>
            <Button className="primary-button" onClick={() => void addProfile()}><Plus size={15} /> Create profile</Button>
          </div> : <>
            <div className="desktop-sync-title"><div><span className="eyebrow">SYNC PROFILE</span>
              <input className="desktop-profile-name" aria-label="Profile name" value={selected.name}
                onChange={(event) => updateSelected({ name: event.target.value })} /></div>
              <div className="desktop-title-actions">
                <Button variant="outline" className="outline-button" disabled={busy} onClick={() => void persistSelected()}>
                  <Check size={15} /> Save
                </Button>
                <Button variant="outline" className="outline-button danger-outline" disabled={busy} onClick={() => void removeProfile()}>
                  <Trash2 size={15} /> Remove
                </Button>
              </div>
            </div>

            {error && <div className="error-banner" role="alert">{error}</div>}
            <div className="desktop-sync-notice"><Activity size={17} /><span>{notice}</span></div>

            <section className="panel desktop-sync-panel">
              <div className="panel-heading"><div><h2>Server connection</h2>
                <p>Use your ASNS server URL and normal ASNS web account.</p></div>
                <span className={`desktop-connection-state ${connected[selected.id] ? "connected" : ""}`}>
                  {connected[selected.id] ? `Connected: ${connected[selected.id]}` : "Not connected"}
                </span></div>
              <label>Server URL<input type="url" value={selected.serverUrl} placeholder="http://nas.example:8080"
                onChange={(event) => updateSelected({ serverUrl: event.target.value })} required /></label>
              <form className="desktop-login-row" onSubmit={(event) => void signIn(event)}>
                <label>Username<input value={username} onChange={(event) => setUsername(event.target.value)}
                  autoComplete="username" disabled={busy || Boolean(connected[selected.id])} required /></label>
                <label>Password<input type="password" value={password} onChange={(event) => setPassword(event.target.value)}
                  autoComplete="current-password" required /></label>
                {connected[selected.id]
                  ? <Button type="button" variant="outline" className="outline-button" disabled={busy} onClick={() => void signOut()}>Sign out</Button>
                  : <Button className="primary-button" disabled={busy}>{busy ? "Connecting…" : "Connect"}</Button>}
              </form>
              <p className="quota-footnote">For HTTP, use only a trusted LAN. HTTPS is recommended, especially outside your home network.</p>
            </section>

            <section className="panel desktop-sync-panel">
              <div className="panel-heading"><div><h2>Folder mapping</h2>
                <p>Only these selected folders are scanned and synchronized.</p></div></div>
              <label>PC folder</label>
              <div className="desktop-folder-row">
                <input value={selected.localFolder} readOnly placeholder="Choose a folder on this PC" />
                <Button variant="outline" className="outline-button" onClick={() => void chooseFolder()}>Browse…</Button>
              </div>
              <label>ASNS storage folder<input value={selected.remoteFolder} onChange={(event) =>
                updateSelected({ remoteFolder: event.target.value.replace(/^\/+/, "") })}
                placeholder="Backups/PC or leave blank for storage root" /></label>
            </section>

            <section className="panel desktop-sync-panel">
              <div className="panel-heading"><div><h2>Sync behavior</h2>
                <p>NAS wins simultaneous edits. Before replacing local files, ASNS Sync saves a recovery copy.</p></div></div>
              <label>Automatic schedule<select value={selected.intervalMinutes} onChange={(event) =>
                updateSelected({ intervalMinutes: Number(event.target.value) })}>
                {intervals.map((interval) => <option value={interval.value} key={interval.value}>{interval.label}</option>)}
              </select></label>
              <div className="desktop-pattern-grid">
                <label>Include patterns <small>Leave blank to include all files. One glob per line.</small>
                  <textarea rows={4} value={includeText} onChange={(event) => setIncludeText(event.target.value)}
                    placeholder={"Documents/**\nPhotos/**/*.jpg"} /></label>
                <label>Exclude patterns <small>One glob per line. Recovery folders are always excluded.</small>
                  <textarea rows={4} value={excludeText} onChange={(event) => setExcludeText(event.target.value)}
                    placeholder={"**/*.tmp\n**/node_modules/**"} /></label>
              </div>
              <label className="desktop-delete-option"><input type="checkbox" checked={selected.syncDeletes}
                onChange={(event) => updateSelected({ syncDeletes: event.target.checked })} />
                Propagate deletions both ways
                <small>Every sync previews deletions and asks for confirmation. Recovery copies are made first. Scheduled syncs pause when deletion confirmation is needed.</small>
              </label>
              <div className="desktop-sync-actions">
                <Button className="primary-button" disabled={busy || !connected[selected.id] || !selected.localFolder}
                  onClick={() => void syncNow()}>
                  <RefreshCw size={15} className={busy ? "refresh-spinning" : ""} /> {busy ? "Syncing…" : "Sync now"}
                </Button>
                <Button variant="outline" className="outline-button" disabled={busy} onClick={() => void openRecovery()}>
                  Open recovery copies
                </Button>
              </div>
            </section>
            {lastResult && <section className="panel desktop-sync-panel">
              <div className="panel-heading"><div><h2>Last sync plan</h2><p>{lastResult.actions.length} operation(s)</p></div></div>
              {lastResult.actions.length ? <ul className="desktop-sync-result">
                {lastResult.actions.slice(0, 30).map((action, index) =>
                  <li key={`${action.type}-${action.path}-${index}`}><code>{action.type}</code><span>{action.path}</span><small>{action.reason}</small></li>)}
              </ul> : <p className="management-empty">Everything is up to date.</p>}
            </section>}
          </>}
        </main>
      </div>
      <footer className="desktop-sync-footer"><span>ASNS Sync 0.1.0</span><span>Bidirectional · NAS-preferred · Recovery copies enabled</span></footer>
    </div>
  );
}

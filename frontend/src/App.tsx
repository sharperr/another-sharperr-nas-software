import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Activity, Bell, ChevronDown, ChevronRight, CircleHelp, Cpu, Database,
  HardDrive, LayoutDashboard, LogOut, Menu, Moon, Network, RefreshCw, Search, Settings, Sun,
  ShieldCheck, SlidersHorizontal, UserRound, X,
} from "lucide-react";
import { api } from "./services/api";
import { Button } from "./components/ui/button";
import type { Disk, Identity, SystemSnapshot } from "./types/system";

type Page = "Overview" | "Storage" | "File manager" | "Sharing" | "Applications" | "Monitoring" | "Users" | "Backups";

const navItems: { label: Page; icon: typeof LayoutDashboard }[] = [
  { label: "Overview", icon: LayoutDashboard },
  { label: "Storage", icon: HardDrive },
  { label: "File manager", icon: Database },
  { label: "Sharing", icon: Network },
  { label: "Applications", icon: Activity },
];
const manageItems: { label: Page; icon: typeof LayoutDashboard }[] = [
  { label: "Monitoring", icon: SlidersHorizontal },
  { label: "Users", icon: UserRound },
  { label: "Backups", icon: RefreshCw },
];

function bytes(value: number): string {
  if (!Number.isFinite(value)) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(unit > 1 ? 1 : 0)} ${units[unit]}`;
}

function duration(seconds: number): string {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return `${days}d ${hours}h ${minutes}m`;
}

function Login({ onLogin }: { onLogin: (identity: Identity) => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(event: React.FormEvent) {
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
        <div className="brand-mark">A</div>
        <p className="eyebrow">ANOTHER SHARPERR NAS SOFTWARE</p>
        <h1>Welcome back</h1>
        <p className="muted">Sign in to manage your storage server.</p>
        <label>Username<input autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required /></label>
        <label>Password<input autoComplete="current-password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required /></label>
        {error && <div className="form-error">{error}</div>}
        <Button className="primary-button wide" disabled={busy}>{busy ? "Signing in…" : "Sign in"}</Button>
        <p className="login-foot"><ShieldCheck size={14} /> Sessions expire automatically after 12 hours</p>
      </form>
    </main>
  );
}

function App() {
  const [identity, setIdentity] = useState<Identity | null>(null);
  const [authReady, setAuthReady] = useState(false);
  const [authError, setAuthError] = useState("");
  const [snapshot, setSnapshot] = useState<SystemSnapshot | null>(null);
  const [disks, setDisks] = useState<Disk[]>([]);
  const [systemError, setSystemError] = useState("");
  const [page, setPage] = useState<Page>("Overview");
  const [simpleMode, setSimpleMode] = useState(true);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [notice, setNotice] = useState("");
  const [lightMode, setLightMode] = useState(false);
  const [cpuHistory, setCpuHistory] = useState<number[]>([]);
  const [memoryHistory, setMemoryHistory] = useState<number[]>([]);

  useEffect(() => {
    api.me().then(setIdentity).catch((error: unknown) => {
      setIdentity(null);
      if (!(error instanceof Error) || error.message !== "authentication required") {
        setAuthError(error instanceof Error ? error.message : "Unable to connect to the ASNS API");
      }
    }).finally(() => setAuthReady(true));
  }, []);

  const refresh = useCallback(async () => {
    const [systemResult, diskResult] = await Promise.allSettled([api.system(), api.disks()]);
    const errors: string[] = [];
    if (systemResult.status === "fulfilled") {
      const current = systemResult.value;
      setSnapshot(current);
      if (current.cpuPercent !== null) setCpuHistory((values) => [...values.slice(-19), current.cpuPercent!]);
      setMemoryHistory((values) => [...values.slice(-19), current.memory.percent]);
    } else {
      errors.push(systemResult.reason instanceof Error ? systemResult.reason.message : "System information unavailable");
    }
    if (diskResult.status === "fulfilled") {
      setDisks(diskResult.value.disks);
    } else {
      errors.push(diskResult.reason instanceof Error ? diskResult.reason.message : "Disk information unavailable");
    }
    if (systemResult.status === "rejected" && systemResult.reason instanceof Error && systemResult.reason.message === "authentication required") {
      setIdentity(null);
    }
    setSystemError(errors.join("; "));
  }, []);

  useEffect(() => {
    if (!identity) return;
    void refresh();
    const timer = window.setInterval(() => void refresh(), 3000);
    return () => window.clearInterval(timer);
  }, [identity, refresh]);

  const totalStorage = useMemo(() => disks.reduce((sum, disk) => sum + disk.capacityBytes, 0), [disks]);

  async function signOut() {
    if (!identity) return;
    try {
      await api.logout(identity.csrfToken);
      setIdentity(null);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not end session");
    }
  }

  if (!authReady) return <div className="loading-screen"><div className="spinner" />Connecting to ASNS…</div>;
  if (authError) return <main className="login-screen"><section className="login-card"><div className="brand-mark">A</div><p className="eyebrow">ASNS CONNECTION</p><h1>Server unavailable</h1><p className="muted">{authError}</p><button className="primary-button wide" onClick={() => window.location.reload()}>Retry connection</button></section></main>;
  if (!identity) return <Login onLogin={setIdentity} />;

  return (
    <div className={`app-shell ${lightMode ? "theme-light" : ""}`}>
      {sidebarOpen && <button className="sidebar-backdrop" onClick={() => setSidebarOpen(false)} aria-label="Close navigation" />}
      <aside className={`sidebar ${sidebarOpen ? "sidebar-open" : ""}`}>
        <div className="brand"><div className="brand-mark small">A</div><div><strong>ASNS</strong><span>STORAGE OS</span></div><button className="icon-button mobile-close" onClick={() => setSidebarOpen(false)}><X size={17} /></button></div>
        <div className="server-switch"><div className="server-avatar"><Database size={16} /></div><div className="server-label"><strong>NAS server</strong><span><i className={`status-dot ${systemError ? "status-dot-error" : ""}`} /> {systemError ? "System data unavailable" : snapshot ? "System online" : "Connecting…"}</span></div><ChevronDown size={15} className="subtle-icon" /></div>
        <div className="nav-caption">WORKSPACE</div>
        <nav>{navItems.map(({ label, icon: Icon }) => <button key={label} className={`nav-item ${page === label ? "selected" : ""}`} onClick={() => { setPage(label); setSidebarOpen(false); }}><Icon size={17} /><span>{label}</span>{label === "Overview" && <span className="nav-active-dot" />}</button>)}</nav>
        <div className="nav-caption manage-caption">MANAGE</div>
        <nav>{manageItems.map(({ label, icon: Icon }) => <button key={label} className={`nav-item ${page === label ? "selected" : ""}`} onClick={() => { setPage(label); setSidebarOpen(false); }}><Icon size={17} /><span>{label}</span></button>)}</nav>
        <div className="sidebar-bottom">
          <div className="help-card"><div className="help-icon"><CircleHelp size={17} /></div><strong>Need a hand?</strong><p>Learn about your NAS and get the most out of ASNS.</p><button onClick={() => setNotice("Setup and development guidance is in the project README.")}>Explore guides <ChevronRight size={14} /></button></div>
          <button className="nav-item settings-button" onClick={() => setNotice("Settings are not available in this foundation release.")}><Settings size={17} /><span>Settings</span></button>
          <div className="sidebar-version">ASNS <span>v0.1.0</span><span className="version-badge">BETA</span></div>
        </div>
      </aside>

      <main className="main-area">
        <header className="topbar">
          <button className="icon-button menu-button md:hidden" onClick={() => setSidebarOpen(true)} aria-label="Open navigation"><Menu size={19} /></button>
          <div className="breadcrumb"><span>Workspace</span><ChevronRight size={14} /><strong>{page}</strong></div>
          <div className="topbar-right">
            <label className="mode-toggle"><span className={simpleMode ? "toggle-active" : ""}>Simple</span><button aria-label="Toggle interface mode" onClick={() => setSimpleMode(!simpleMode)} className={`toggle ${simpleMode ? "" : "toggle-on"}`}><i /></button><span className={!simpleMode ? "toggle-active" : ""}>Advanced</span></label>
            <span className="topbar-divider" />
            <button className="icon-button search-button" aria-label="Search" onClick={() => setNotice("Search is not available in this release.")}><Search size={18} /></button>
            <button className="icon-button notification-button" aria-label="Notifications" onClick={() => setNotice("Notifications are not implemented in ASNS 0.1.0.")}><Bell size={18} /></button>
            <button className="icon-button theme-button" aria-label={lightMode ? "Switch to dark mode" : "Switch to light mode"} title={lightMode ? "Switch to dark mode" : "Switch to light mode"} onClick={() => setLightMode(!lightMode)}>{lightMode ? <Moon size={17} /> : <Sun size={17} />}</button>
            <span className="topbar-divider" />
            <button className="profile-button" onClick={() => void signOut()} title="Sign out"><span className="profile-avatar">{identity.username.slice(0, 1).toUpperCase()}</span><span className="profile-name">{identity.username}<small>Administrator</small></span><LogOut size={15} className="logout-icon" /></button>
          </div>
        </header>
        <div className="content">
          <div className="page-heading"><div><div className="eyebrow">{page === "Overview" ? "YOUR SERVER AT A GLANCE" : "ASNS WORKSPACE"}</div><h1>{page === "Overview" ? "Good to see you." : page}</h1><p>{page === "Overview" ? "Here's what's happening with your NAS right now." : page === "Storage" ? "Physical block devices discovered on this Linux host." : `${page} tools will become available as ASNS grows.`}</p></div><button className="outline-button refresh-button" onClick={() => void refresh()}><RefreshCw size={15} /> Refresh</button></div>
          {systemError && <div className="error-banner"><span>{systemError}</span><button onClick={() => void refresh()}>Try again</button></div>}
          {page === "Storage" && <section className="panel full-storage-panel"><div className="panel-heading"><div><h2>Physical disks</h2><p>Live discovery from Linux sysfs. Capacity is device-reported, not filesystem free space.</p></div><span className="live-tag"><i /> LIVE</span></div>{disks.length ? <div className="full-disk-list">{disks.map((disk) => <div className="full-disk-row" key={disk.name}><div className="disk-icon"><HardDrive size={17} /></div><div className="full-disk-info"><strong>{disk.model || disk.name}</strong><span>{disk.name} · {disk.serial ? `Serial ${disk.serial}` : "Serial unavailable"} · {disk.removable ? "Removable" : "Fixed device"}</span></div><b>{bytes(disk.capacityBytes)}</b></div>)}</div> : <div className="empty-state"><HardDrive size={22} /><span>{systemError ? "Disk information is unavailable" : "No physical disks were detected"}</span><small>Virtual machines may not expose physical drive details.</small></div>}</section>}
          {page !== "Overview" && page !== "Storage" && <div className="coming-soon"><div className="coming-icon"><SlidersHorizontal size={22} /></div><h2>{page} is not implemented yet</h2><p>This area is intentionally unavailable in ASNS 0.1.0. No simulated data is shown.</p></div>}
          {page === "Overview" && <>
            <section className="status-banner"><div className="status-symbol"><ShieldCheck size={19} /></div><div><strong>{systemError ? "Some system data is unavailable" : snapshot ? "System metrics are live" : "Collecting system metrics"}</strong><p>ASNS reads live information directly from your Linux server.</p></div><div className="system-meta"><span><i className={`status-dot ${systemError ? "status-dot-error" : ""}`} /> {systemError ? "CHECK SYSTEM DATA" : snapshot ? "METRICS AVAILABLE" : "INITIALIZING"}</span><span>UPTIME <b>{snapshot ? duration(snapshot.uptimeSeconds) : "—"}</b></span></div></section>
            <section className="metric-grid">
              <MetricCard title="Processor" icon={<Cpu size={17} />} value={snapshot?.cpuPercent == null ? "—" : `${snapshot.cpuPercent.toFixed(1)}%`} detail={snapshot?.cpuPercent == null ? "Collecting live readings…" : "Current CPU utilization"} percent={snapshot?.cpuPercent ?? 0} tone="blue" history={cpuHistory} />
              <MetricCard title="Memory" icon={<Activity size={17} />} value={snapshot ? `${snapshot.memory.percent.toFixed(1)}%` : "—"} detail={snapshot ? `${bytes(snapshot.memory.usedBytes)} of ${bytes(snapshot.memory.totalBytes)} used` : "Waiting for system data"} percent={snapshot?.memory.percent ?? 0} tone="purple" history={memoryHistory} />
              <MetricCard title="Storage" icon={<HardDrive size={17} />} value={disks.length ? bytes(totalStorage) : "—"} detail={disks.length ? `${disks.length} physical ${disks.length === 1 ? "disk" : "disks"} · capacity` : "No physical disks detected"} percent={0} tone="green" />
              <MetricCard title="Network" icon={<Network size={17} />} value={snapshot ? `${bytes(snapshot.network.receiveBytesPerSecond)}/s` : "—"} detail={snapshot ? `↑ ${bytes(snapshot.network.sendBytesPerSecond)}/s outbound` : "Waiting for system data"} percent={0} tone="amber" />
            </section>
            <section className="lower-grid">
              <div className="panel performance-panel"><div className="panel-heading"><div><h2>System performance</h2><p>Live resource utilization</p></div><span className="live-tag"><i /> LIVE</span></div><div className="chart-legend"><span><i className="legend-cpu" /> Processor</span><span><i className="legend-memory" /> Memory</span><span className="chart-window">Last minute</span></div><div className="chart-wrap"><PerformanceChart cpu={cpuHistory} memory={memoryHistory} /></div><div className="chart-axis"><span>60s ago</span><span>45s</span><span>30s</span><span>15s</span><span>Now</span></div></div>
              <div className="panel storage-panel"><div className="panel-heading"><div><h2>Physical disks</h2><p>Discovered storage devices</p></div><button className="panel-action" onClick={() => setPage("Storage")}>View all <ChevronRight size={14} /></button></div>{disks.length ? <div className="disk-list">{disks.slice(0, 3).map((disk) => <div className="disk-row" key={disk.name}><div className="disk-icon"><HardDrive size={17} /></div><div className="disk-info"><strong>{disk.model || disk.name}</strong><span>{disk.name} {disk.serial ? `· ${disk.serial}` : "· Serial unavailable"}</span></div><b>{bytes(disk.capacityBytes)}</b></div>)}</div> : <div className="empty-state"><HardDrive size={22} /><span>{systemError ? "Disk information is unavailable" : "No physical disks were detected"}</span><small>Virtual machines may not expose physical drive details.</small></div>}<div className="storage-foot"><span><span className="health-dot" /> Devices discovered from Linux sysfs</span><span>{disks.length} {disks.length === 1 ? "device" : "devices"}</span></div></div>
            </section>
            <section className="panel activity-panel"><div className="panel-heading"><div><h2>System status</h2><p>Current host information</p></div><span className="subtle-caption">REFRESHES EVERY 3 SECONDS</span></div><div className="activity-row"><div className={`activity-icon ${snapshot ? "" : "activity-unavailable"}`}><ShieldCheck size={16} /></div><div className="activity-copy"><strong>{snapshot ? "Host metrics are live" : "Host metrics unavailable"}</strong><span>{snapshot ? "CPU, memory, uptime and network are read directly from Linux; disk discovery is reported separately." : systemError || "Waiting for the first system sample."}</span></div><time>{snapshot ? new Date(snapshot.collectedAt).toLocaleTimeString() : "Waiting…"}</time></div></section>
            {!simpleMode && snapshot && <section className="panel advanced-panel"><div className="panel-heading"><div><h2>Advanced system details</h2><p>Raw host metrics for troubleshooting</p></div><span className="subtle-caption">LIVE PROCFS / SYSFS DATA</span></div><div className="advanced-grid"><div><span>CPU utilization</span><strong>{snapshot.cpuPercent === null ? "Sampling…" : `${snapshot.cpuPercent.toFixed(2)}%`}</strong></div><div><span>Memory available</span><strong>{bytes(snapshot.memory.availableBytes)} / {bytes(snapshot.memory.totalBytes)}</strong></div><div><span>Network receive</span><strong>{bytes(snapshot.network.receiveBytesPerSecond)}/s</strong></div><div><span>Network transmit</span><strong>{bytes(snapshot.network.sendBytesPerSecond)}/s</strong></div><div><span>System uptime</span><strong>{duration(snapshot.uptimeSeconds)}</strong></div><div><span>Last collected</span><strong>{new Date(snapshot.collectedAt).toLocaleString()}</strong></div></div></section>}
            <footer className="page-footer"><span>ASNS <b>0.1.0</b> · Self-hosted NAS management</span><span><span className="health-dot" /> Connected to your server</span></footer>
          </>}
        </div>
      </main>
      {notice && <div className="toast"><span>{notice}</span><button onClick={() => setNotice("")} aria-label="Dismiss"><X size={15} /></button></div>}
    </div>
  );
}

function MetricCard({ title, icon, value, detail, percent, tone, history }: { title: string; icon: React.ReactNode; value: string; detail: string; percent: number; tone: string; history?: number[] }) {
  return <article className={`metric-card tone-${tone}`}><div className="metric-top"><span className="metric-title">{title}</span><span className="metric-icon">{icon}</span></div><div className="metric-value">{value}</div><div className="metric-detail">{detail}</div><div className="metric-bottom">{history && history.length > 1 ? <MiniChart values={history} /> : <div className="progress-track"><span style={{ width: `${Math.min(100, percent)}%` }} /></div>}<span>{history && history.length > 1 ? "LIVE" : title === "Storage" || title === "Network" ? "LIVE" : `${Math.round(percent)}%`}</span></div></article>;
}

function MiniChart({ values }: { values: number[] }) {
  const points = values.map((value, index) => `${(index / Math.max(1, values.length - 1)) * 100},${30 - Math.max(0, Math.min(100, value)) * 0.28}`).join(" ");
  return <svg className="mini-chart" viewBox="0 0 100 32" preserveAspectRatio="none"><polyline points={points} fill="none" stroke="currentColor" strokeWidth="2" vectorEffect="non-scaling-stroke" /></svg>;
}

function PerformanceChart({ cpu, memory }: { cpu: number[]; memory: number[] }) {
  function points(values: number[]) {
    if (!values.length) return "";
    return values.map((value, index) => `${(index / Math.max(1, values.length - 1)) * 600},${190 - Math.max(0, Math.min(100, value)) * 1.7}`).join(" ");
  }
  return <svg className="performance-chart" viewBox="0 0 600 200" preserveAspectRatio="none" role="img" aria-label="Live processor and memory utilization chart"><line x1="0" x2="600" y1="20" y2="20" /><line x1="0" x2="600" y1="70" y2="70" /><line x1="0" x2="600" y1="120" y2="120" /><line x1="0" x2="600" y1="170" y2="170" />{cpu.length > 1 && <polyline className="chart-cpu" points={points(cpu)} />}{memory.length > 1 && <polyline className="chart-memory" points={points(memory)} />}</svg>;
}

export default App;

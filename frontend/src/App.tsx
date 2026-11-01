import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Activity, Bell, Check, ChevronDown, ChevronRight, CircleHelp, Cpu, Database,
  HardDrive, LayoutDashboard, LogOut, Menu, Moon, Network, RefreshCw, Search,
  Settings, ShieldCheck, SlidersHorizontal, Sun, UserRound, X,
} from "lucide-react";
import { Button } from "./components/ui/button";
import { api } from "./services/api";
import type { Disk, Identity, SystemSnapshot } from "./types/system";

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

function App() {
  const [identity, setIdentity] = useState<Identity | null>(null);
  const [authReady, setAuthReady] = useState(false);
  const [authError, setAuthError] = useState("");
  const [snapshot, setSnapshot] = useState<SystemSnapshot | null>(null);
  const [disks, setDisks] = useState<Disk[]>([]);
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
      const [systemResult, diskResult] = await Promise.allSettled([api.system(), api.disks()]);
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
    return [...workspacePages, ...managementPages].filter((item) =>
      item.label.toLowerCase().includes(term),
    );
  }, [search]);

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
          {workspacePages.map(({ label, icon: Icon }) => (
            <NavButton key={label} label={label} icon={<Icon size={17} />} active={page === label}
              onClick={() => navigate(label)} />
          ))}
        </nav>
        <div className="nav-caption manage-caption">MANAGE</div>
        <nav aria-label="Management">
          {managementPages.map(({ label, icon: Icon }) => (
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
                <span className="profile-name">{identity.username}<small>Administrator</small></span>
                <ChevronDown size={14} className="profile-chevron" />
              </button>
              {popover === "profile" && (
                <div className="header-popover profile-popover">
                  <div className="profile-summary"><span className="profile-avatar">{identity.username.slice(0, 1).toUpperCase()}</span>
                    <span><strong>{identity.username}</strong><small>Administrator account</small></span>
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
            <StoragePage disks={disks} error={systemError} onRefresh={() => void refresh()} />
          )}
          {page !== "Overview" && page !== "Storage" && <ComingSoonPage page={page} />}

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

function StoragePage({ disks, error, onRefresh }: { disks: Disk[]; error: string; onRefresh: () => void }) {
  const capacity = disks.reduce((total, disk) => total + disk.capacityBytes, 0);
  return (
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
        <div><span>HEALTH</span><strong className="unknown-health">Not checked</strong></div>
      </div>
      {disks.length ? (
        <div className="full-disk-list">
          {disks.map((disk) => (
            <article className="full-disk-row" key={disk.name}>
              <div className="disk-icon"><HardDrive size={17} /></div>
              <div className="full-disk-info"><strong>{disk.model || disk.name}</strong>
                <span>{disk.name} · {disk.serial ? `Serial ${disk.serial}` : "Serial not exposed"} · {disk.removable ? "Removable device" : "Fixed device"}</span>
              </div>
              <span className="disk-capacity"><b>{formatBytes(disk.capacityBytes)}</b><small>raw capacity</small></span>
            </article>
          ))}
        </div>
      ) : (
        <div className="empty-state"><HardDrive size={22} />
          <span>{error ? "Disk information is unavailable" : "No physical disks were detected"}</span>
          <small>{error || "Virtual machines may not expose underlying physical drives."}</small>
        </div>
      )}
      <div className="storage-disclaimer">
        <ShieldCheck size={15} />
        SMART health, temperature, filesystem use, and mount management are not available yet.
      </div>
    </section>
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
    Applications: "Inspect Docker applications and manage their lifecycle.",
    Monitoring: "Review system history, SMART status, temperatures, and logs.",
    Users: "Manage local ASNS accounts, roles, and active sessions.",
    Backups: "Configure destinations, scheduled jobs, and restore points.",
  };
  return (
    <section className="coming-soon">
      <div className="coming-icon"><SlidersHorizontal size={22} /></div>
      <span className="eyebrow">PLANNED FOR A FUTURE RELEASE</span>
      <h2>{page} is not implemented yet</h2>
      <p>{descriptions[page]}</p>
      <p className="coming-note">This section does not display mock data or issue system commands.</p>
    </section>
  );
}

export default App;

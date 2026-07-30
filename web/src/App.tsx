import { useEffect, useState, useCallback } from "react";
import { api, StatusResponse, SessionState, HostSnapshot } from "./api";
import { Sparkline } from "./Sparkline";
import { usePrefs } from "./usePrefs";
import { useEventStream, LiveEvent } from "./useEventStream";
import { useServerState } from "./useServerState";
import { CustomizeMenu } from "./CustomizeMenu";
import { Players } from "./Players";
import { OfflineNotice } from "./OfflineNotice";
import { ServerAdmin } from "./ServerAdmin";
import { Backups } from "./Backups";
import { Settings } from "./Settings";

export function App() {
  const [state, setState] = useState<SessionState | "loading">("loading");

  const refresh = useCallback(() => {
    api.session().then((s) => setState(s.state)).catch(() => setState("needs_login"));
  }, []);
  useEffect(refresh, [refresh]);

  if (state === "loading") return <div className="loading">Loading…</div>;
  if (state === "needs_setup") return <AuthScreen mode="setup" onDone={refresh} />;
  if (state === "needs_login") return <AuthScreen mode="login" onDone={refresh} />;
  return <Shell onLogout={refresh} />;
}

// ---- auth ----

function AuthScreen({ mode, onDone }: { mode: "setup" | "login"; onDone: () => void }) {
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  // Single-admin model (§6.6): there is no user administration, so the
  // password is the only credential. The backend uses a fixed "admin"
  // username internally; no reason to show it.
  const submit = async () => {
    setBusy(true);
    setErr("");
    try {
      if (mode === "setup") await api.setup(password);
      else await api.login(password);
      onDone();
    } catch (e) {
      setErr((e as Error).message);
      setBusy(false);
    }
  };

  return (
    <div className="auth-wrap">
      <div className="auth-card">
        <div className="auth-logo">Pal<span>adin</span></div>
        <div className="auth-sub">
          {mode === "setup"
            ? "First run — set a password to protect this panel."
            : "Enter your password to continue."}
        </div>
        <div className="field">
          <label>{mode === "setup" ? "New password" : "Password"}</label>
          <input
            type="password"
            value={password}
            autoFocus
            onChange={(e) => setPassword(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && submit()}
            autoComplete={mode === "setup" ? "new-password" : "current-password"}
          />
        </div>
        <div className="auth-err">{err}</div>
        <button className="btn" onClick={submit} disabled={busy || password.length < 1}>
          {mode === "setup" ? "Set password & enter" : "Sign in"}
        </button>
      </div>
    </div>
  );
}

// ---- app shell ----

type Section = "dashboard" | "players" | "map" | "settings" | "backups" | "console" | "metrics";

const NAV: { id: Section; label: string; icon: string; ready?: boolean }[] = [
  { id: "dashboard", label: "Dashboard", icon: "▮", ready: true },
  { id: "players", label: "Players", icon: "◆", ready: true },
  { id: "map", label: "World Map", icon: "◉" },
  { id: "settings", label: "Server Settings", icon: "⚙", ready: true },
  { id: "backups", label: "Backups", icon: "❒", ready: true },
  { id: "console", label: "Server Admin", icon: "❯", ready: true },
  { id: "metrics", label: "Metrics", icon: "◔" },
];

function Shell({ onLogout }: { onLogout: () => void }) {
  const [section, setSection] = useState<Section>("dashboard");
  const logout = async () => { await api.logout(); onLogout(); };
  const { online, players } = useServerState(10000);

  return (
    <div className="shell">
      <nav className="nav">
        <div className="nav-logo">Pal<span>adin</span></div>
        {NAV.map((n) => (
          <div
            key={n.id}
            className={"nav-item" + (section === n.id ? " active" : "")}
            onClick={() => n.ready && setSection(n.id)}
            style={n.ready ? undefined : { opacity: 0.5, cursor: "default" }}
          >
            <span className="ico">{n.icon}</span>
            {n.label}
            {n.id === "players" && online && players !== null && (
              <span className={"nav-count" + (players > 0 ? " lit" : "")}>{players}</span>
            )}
            {!n.ready && <span className="soon">soon</span>}
          </div>
        ))}
        <div className="nav-spacer" />
        <div className="nav-logout" onClick={logout}>Sign out</div>
        <div className="nav-foot">Paladin v0.1 · trial</div>
      </nav>
      <main className="main">
        {section === "dashboard" ? <Dashboard /> : section === "players" ? <Players /> : section === "console" ? <ServerAdmin /> : section === "backups" ? <Backups /> : section === "settings" ? <Settings /> : <ComingSoon section={section} />}
      </main>
    </div>
  );
}

function ComingSoon({ section }: { section: Section }) {
  const label = NAV.find((n) => n.id === section)?.label ?? section;
  return (
    <>
      <div className="page-head"><div className="page-title">{label}</div></div>
      <div className="placeholder">This section is coming in a later slice.</div>
    </>
  );
}

// ---- dashboard ----

// How many samples to keep in the client-side rolling window. At a 5s
// poll, 60 samples ≈ 5 minutes (the Proxmox/pfSense live-graph pattern —
// history lives in the tab, nothing stored server-side).
const HISTORY = 60;

function pushCapped(arr: number[], v: number): number[] {
  const next = arr.length >= HISTORY ? arr.slice(1) : arr.slice();
  next.push(v);
  return next;
}

function Dashboard() {
  const [st, setSt] = useState<StatusResponse | null>(null);
  const [host, setHost] = useState<HostSnapshot | null>(null);
  const [err, setErr] = useState("");
  const [fpsHist, setFpsHist] = useState<number[]>([]);
  const [rxHist, setRxHist] = useState<number[]>([]);
  const [txHist, setTxHist] = useState<number[]>([]);
  const { isVisible, toggle, reset } = usePrefs();

  useEffect(() => {
    let alive = true;
    const tick = () => {
      api.status()
        .then((s) => {
          if (!alive) return;
          setSt(s); setErr("");
          if (s.online) setFpsHist((h) => pushCapped(h, s.fps));
        })
        .catch((e) => alive && setErr((e as Error).message));
      api.host()
        .then((h) => {
          if (!alive || h.available === false) return;
          setHost(h);
          if (h.net_available) {
            setRxHist((a) => pushCapped(a, h.net_rx_bps));
            setTxHist((a) => pushCapped(a, h.net_tx_bps));
          }
        })
        .catch(() => {});
    };
    tick();
    const id = setInterval(tick, 5000);
    return () => { alive = false; clearInterval(id); };
  }, []);

  if (err) return <div className="offline-banner">Could not load status: {err}</div>;
  if (!st) return <div className="loading">Loading status…</div>;

  // Conditional cards are only "available" when the host provides them.
  const available = (id: string) => {
    if (id === "temp") return !!host?.temp_available;
    if (id === "network") return !!host?.net_available;
    return true;
  };

  return (
    <>
      <div className="page-head">
        <div>
          <div className="page-title">Dashboard</div>
          <div className="page-sub">Live server overview · refreshes every 5s</div>
        </div>
        <div className="head-actions">
          <CustomizeMenu isVisible={isVisible} toggle={toggle} reset={reset} available={available} />
          <StatusPill online={st.online} />
        </div>
      </div>

      {!st.online && (
        <OfflineNotice what="game server metrics" />
      )}

      <div className="grid">
        {st.online && isVisible("fps") && (
        <div className="card hero">
          <FpsDial fps={st.fps} />
          <div className="dial-caption">avg {st.fps_average.toFixed(1)} · frame {st.frame_time_ms.toFixed(1)} ms</div>
          <div style={{ width: "100%", marginTop: 12 }}>
            <Sparkline data={fpsHist} min={0} max={70}
              color={st.fps >= 45 ? "var(--good)" : st.fps >= 25 ? "var(--warn)" : "var(--bad)"} />
          </div>
        </div>
        )}

        {st.online && isVisible("players") && <StatCard className="span4" label="Players online" value={`${st.players}`}
          unit={`/ ${st.max_players}`} sub={`${st.bases} bases · day ${st.days}`} />}
        {st.online && isVisible("uptime") && <StatCard className="span4" label="Uptime" value={formatUptime(st.uptime_sec)} sub="since last start" />}

        {st.online && isVisible("server") && (
        <div className="card span4">
          <div className="card-label">Server</div>
          <div className="server-name">{st.server_name || "—"}</div>
          <div className="server-desc clamp3">{st.description || "No description set."}</div>
          <div className="server-meta">
            <Meta k="Version" v={st.version || "—"} />
            <Meta k="World GUID" v={st.world_guid ? st.world_guid.slice(0, 12) + "…" : "—"} />
            <Meta k="Backups" v={`${st.backup_count}`} />
          </div>
        </div>
        )}

        {host && <HostCards host={host} rxHist={rxHist} txHist={txHist} isVisible={isVisible} />}
      </div>
    </>
  );
}

// Host-metric cards. Temp card hides itself when the host exposes no
// sensors (VMs) — by design, not failure (§6.5).
function HostCards({ host, rxHist, txHist, isVisible }: { host: HostSnapshot; rxHist: number[]; txHist: number[]; isVisible: (id: string) => boolean }) {
  const memPct = host.mem_total ? (host.mem_used / host.mem_total) * 100 : 0;
  return (
    <>
      {isVisible("activity") && <ActivityCard />}

      {isVisible("cpu") && (
      <div className="card span4">
        <div className="card-label">CPU</div>
        <div><span className="stat-big" style={{ color: cpuColor(host.cpu_usage) }}>{host.cpu_usage.toFixed(0)}</span><span className="stat-unit">%</span></div>
        <div className="stat-sub">busiest core {host.cpu_hottest_core.toFixed(0)}%{host.cpu_steal > 1 ? ` · steal ${host.cpu_steal.toFixed(0)}%` : ""}</div>
        <div className="host-ident">{host.cpu_model} · {host.cpu_cores} core{host.cpu_cores === 1 ? "" : "s"} · {(host.cpu_mhz / 1000).toFixed(2)} GHz</div>
      </div>
      )}

      {isVisible("memory") && (
      <div className="card span4">
        <div className="card-label">Memory</div>
        <div><span className="stat-big" style={{ color: memPct > 90 ? "var(--bad)" : memPct > 75 ? "var(--warn)" : "var(--text)" }}>{fmtBytes(host.mem_used)}</span><span className="stat-unit">/ {fmtBytes(host.mem_total)}</span></div>
        <div className="stat-sub">{memPct.toFixed(0)}% used{host.swap_used > 0 ? ` · swap ${fmtBytes(host.swap_used)}` : ""}</div>
      </div>
      )}

      {host.temp_available && isVisible("temp") && (
        <div className="card span4">
          <div className="card-label">CPU Temp</div>
          <div><span className="stat-big" style={{ color: host.cpu_temp > 85 ? "var(--bad)" : host.cpu_temp > 70 ? "var(--warn)" : "var(--good)" }}>{host.cpu_temp.toFixed(0)}</span><span className="stat-unit">°C</span></div>
          <div className="stat-sub">package temperature</div>
        </div>
      )}

      {host.net_available && isVisible("network") && (
        <div className="card span8">
          <div className="card-label">Network · {host.net_interface}</div>
          <div className="net-row">
            <div className="net-fig"><span className="net-arrow" style={{ color: "var(--good)" }}>↓</span> {fmtRate(host.net_rx_bps)}<span className="net-lbl">rx</span></div>
            <div className="net-fig"><span className="net-arrow" style={{ color: "var(--accent)" }}>↑</span> {fmtRate(host.net_tx_bps)}<span className="net-lbl">tx</span></div>
          </div>
          <div style={{ marginTop: 10 }}><Sparkline data={rxHist.map((r, i) => r + (txHist[i] ?? 0))} color="var(--accent)" height={38} /></div>
        </div>
      )}
    </>
  );
}

function StatusPill({ online }: { online: boolean }) {
  return (
    <span className={"pill " + (online ? "online" : "offline")}>
      <span className="dot" />{online ? "Online" : "Offline"}
    </span>
  );
}

function StatCard({ className, label, value, unit, sub }: {
  className?: string; label: string; value: string; unit?: string; sub?: string;
}) {
  return (
    <div className={"card " + (className ?? "")}>
      <div className="card-label">{label}</div>
      <div><span className="stat-big">{value}</span>{unit && <span className="stat-unit">{unit}</span>}</div>
      {sub && <div className="stat-sub">{sub}</div>}
    </div>
  );
}

function Meta({ k, v }: { k: string; v: string }) {
  return <div className="meta-item"><div className="k">{k}</div><div className="v">{v}</div></div>;
}

// FPS dial: an SVG arc gauge. Palworld servers target 60; we scale 0–70
// so a healthy server sits near the top of the sweep.
function FpsDial({ fps }: { fps: number }) {
  const max = 70;
  const pct = Math.max(0, Math.min(1, fps / max));
  const start = -220; // degrees
  const sweep = 260;
  const angle = start + sweep * pct;
  const r = 84;
  const cx = 100, cy = 100;
  const rad = (deg: number) => (deg * Math.PI) / 180;
  const point = (deg: number) => [cx + r * Math.cos(rad(deg)), cy + r * Math.sin(rad(deg))];
  const [sx, sy] = point(start);
  const [ex, ey] = point(angle);
  const [fx, fy] = point(start + sweep);
  const large = sweep * pct > 180 ? 1 : 0;
  const color = fps >= 45 ? "var(--good)" : fps >= 25 ? "var(--warn)" : "var(--bad)";

  return (
    <div className="dial-wrap">
      <svg viewBox="0 0 200 200" width="200" height="200">
        {/* track */}
        <path d={`M ${sx} ${sy} A ${r} ${r} 0 1 1 ${fx} ${fy}`}
          fill="none" stroke="var(--border-lit)" strokeWidth="12" strokeLinecap="round" />
        {/* value arc */}
        <path d={`M ${sx} ${sy} A ${r} ${r} 0 ${large} 1 ${ex} ${ey}`}
          fill="none" stroke={color} strokeWidth="12" strokeLinecap="round"
          style={{ transition: "all 0.6s ease" }} />
      </svg>
      <div className="dial-num">
        <div className="v" style={{ color }}>{Math.round(fps)}</div>
        <div className="l">FPS</div>
      </div>
    </div>
  );
}

function cpuColor(pct: number): string {
  return pct > 90 ? "var(--bad)" : pct > 70 ? "var(--warn)" : "var(--text)";
}

function fmtBytes(b: number): string {
  if (b >= 1 << 30) return (b / (1 << 30)).toFixed(1) + " GB";
  if (b >= 1 << 20) return (b / (1 << 20)).toFixed(0) + " MB";
  if (b >= 1 << 10) return (b / (1 << 10)).toFixed(0) + " KB";
  return b + " B";
}

function fmtRate(bps: number): string {
  const bits = bps * 8;
  if (bits >= 1e9) return (bits / 1e9).toFixed(2) + " Gb/s";
  if (bits >= 1e6) return (bits / 1e6).toFixed(1) + " Mb/s";
  if (bits >= 1e3) return (bits / 1e3).toFixed(0) + " Kb/s";
  return bits.toFixed(0) + " b/s";
}

function formatUptime(sec: number): string {
  if (sec < 60) return `${sec}s`;
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

// ActivityCard: the dashboard's recent-activity feed — player joins/leaves
// (roster differ), Paladin actions, and log lines, seeded with history on
// load (tail-on-connect) and streaming live. Read-only compact view; the
// full viewer with controls lives on Server Admin and Backups.
function ActivityCard() {
  const { events } = useEventStream(120);
  const recent = events.slice(-8);
  return (
    <div className="card span6">
      <div className="card-label">Recent activity</div>
      {recent.length === 0 && <div className="act-empty">Nothing yet — activity appears as it happens.</div>}
      <div className="act-list">
        {recent.map((e, i) => <ActivityRow key={e.seq ?? `s${i}`} e={e} />)}
      </div>
    </div>
  );
}

function ActivityRow({ e }: { e: LiveEvent }) {
  const t = e.time ? new Date(e.time).toLocaleTimeString() : "";
  return (
    <div className={"act-row k-" + e.kind}>
      <span className="act-time">{t}</span>
      <span className="act-msg">{e.msg}</span>
    </div>
  );
}

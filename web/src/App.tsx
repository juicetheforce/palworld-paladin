import { useEffect, useState, useCallback } from "react";
import { api, StatusResponse, SessionState } from "./api";

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
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    setErr("");
    try {
      if (mode === "setup") await api.setup(username, password);
      else await api.login(username, password);
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
            ? "First run — create your admin password."
            : "Sign in to manage your server."}
        </div>
        <div className="field">
          <label>Username</label>
          <input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" />
        </div>
        <div className="field">
          <label>Password</label>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && submit()}
            autoComplete={mode === "setup" ? "new-password" : "current-password"}
          />
        </div>
        <div className="auth-err">{err}</div>
        <button className="btn" onClick={submit} disabled={busy || password.length < 1}>
          {mode === "setup" ? "Create & enter" : "Sign in"}
        </button>
      </div>
    </div>
  );
}

// ---- app shell ----

type Section = "dashboard" | "players" | "map" | "settings" | "backups" | "console" | "metrics";

const NAV: { id: Section; label: string; icon: string; ready?: boolean }[] = [
  { id: "dashboard", label: "Dashboard", icon: "▮", ready: true },
  { id: "players", label: "Players", icon: "◆" },
  { id: "map", label: "World Map", icon: "◉" },
  { id: "settings", label: "Settings", icon: "⚙" },
  { id: "backups", label: "Backups", icon: "❒" },
  { id: "console", label: "Console", icon: "❯" },
  { id: "metrics", label: "Metrics", icon: "◔" },
];

function Shell({ onLogout }: { onLogout: () => void }) {
  const [section, setSection] = useState<Section>("dashboard");
  const logout = async () => { await api.logout(); onLogout(); };

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
            {!n.ready && <span className="soon">soon</span>}
          </div>
        ))}
        <div className="nav-spacer" />
        <div className="nav-logout" onClick={logout}>Sign out</div>
        <div className="nav-foot">Paladin v0.1 · trial</div>
      </nav>
      <main className="main">
        {section === "dashboard" ? <Dashboard /> : <ComingSoon section={section} />}
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

function Dashboard() {
  const [st, setSt] = useState<StatusResponse | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    let alive = true;
    const tick = () =>
      api.status()
        .then((s) => alive && (setSt(s), setErr("")))
        .catch((e) => alive && setErr((e as Error).message));
    tick();
    const id = setInterval(tick, 5000);
    return () => { alive = false; clearInterval(id); };
  }, []);

  if (err) return <div className="offline-banner">Could not load status: {err}</div>;
  if (!st) return <div className="loading">Loading status…</div>;

  return (
    <>
      <div className="page-head">
        <div>
          <div className="page-title">Dashboard</div>
          <div className="page-sub">Live server overview · refreshes every 5s</div>
        </div>
        <StatusPill online={st.online} />
      </div>

      {!st.online && (
        <div className="offline-banner">
          Server is not responding. {st.error}
        </div>
      )}

      <div className="grid">
        <div className="card hero">
          <FpsDial fps={st.fps} />
          <div className="dial-caption">avg {st.fps_average.toFixed(1)} · frame {st.frame_time_ms.toFixed(1)} ms</div>
        </div>

        <StatCard className="span4" label="Players online" value={`${st.players}`}
          unit={`/ ${st.max_players}`} sub={`${st.bases} bases · day ${st.days}`} />
        <StatCard className="span4" label="Uptime" value={formatUptime(st.uptime_sec)} sub="since last start" />

        <div className="card span8">
          <div className="card-label">Server</div>
          <div className="server-name">{st.server_name || "—"}</div>
          <div className="server-desc">{st.description || "No description set."}</div>
          <div className="server-meta">
            <Meta k="Version" v={st.version || "—"} />
            <Meta k="World GUID" v={st.world_guid ? st.world_guid.slice(0, 12) + "…" : "—"} />
            <Meta k="Backups" v={`${st.backup_count}`} />
          </div>
        </div>
      </div>
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

function formatUptime(sec: number): string {
  if (sec < 60) return `${sec}s`;
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

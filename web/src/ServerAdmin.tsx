import { useEffect, useState, useCallback } from "react";
import { api, HistoryEntry, UpdateCheckResponse } from "./api";
import { useServerState } from "./useServerState";
import { useEventStream } from "./useEventStream";
import { LiveLog } from "./LiveLog";

export function ServerAdmin() {
  const [history, setHistory] = useState<HistoryEntry[]>([]);
  const [broadcastMsg, setBroadcastMsg] = useState("");
  const [warnMsg, setWarnMsg] = useState("Server maintenance shortly — please reach a safe spot.");
  const [delay, setDelay] = useState(30);
  const [useWarn, setUseWarn] = useState(true);
  const [busy, setBusy] = useState("");
  const [note, setNote] = useState("");
  const { online, version } = useServerState();
  const { events, connected, clear } = useEventStream();
  const [updWarn, setUpdWarn] = useState("Server updating shortly — you will be disconnected briefly.");
  const [updDelay, setUpdDelay] = useState(60);
  const [updRunning, setUpdRunning] = useState(false);
  const [check, setCheck] = useState<UpdateCheckResponse | null>(null);

  const load = useCallback(() => {
    api.history().then((r) => setHistory(r.history ?? [])).catch(() => {});
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [load]);

  // Update-availability: fetch on page load (the backend lazily refreshes
  // a stale cache); poll while a check is in flight so the card updates
  // when the ~30-90s steamcmd query completes.
  useEffect(() => {
    let alive = true;
    const tick = () => api.updateCheck().then((r) => alive && setCheck(r)).catch(() => {});
    tick();
    const id = setInterval(() => {
      if (check?.checking || updRunning) tick();
    }, 8000);
    return () => { alive = false; clearInterval(id); };
  }, [check?.checking, updRunning]);

  const checkNow = async () => {
    try {
      await api.updateCheckRefresh();
      setCheck((c) => (c ? { ...c, checking: true } : c));
    } catch { /* surfaced by next poll */ }
  };

  const flash = (m: string) => { setNote(m); setTimeout(() => setNote(""), 4000); };

  // The update runs in the background; its end is signalled by a terminal
  // event (done/error, op "update") on the live stream.
  useEffect(() => {
    const last = events[events.length - 1];
    if (last && last.op === "update" && (last.kind === "done" || last.kind === "error")) {
      setUpdRunning(false);
      api.updateCheck().then(setCheck).catch(() => {});
    }
  }, [events]);

  const doUpdate = async () => {
    if (!confirm(`Check Steam for a server update and install it if one exists?${updWarn ? ` Players will be warned${updDelay > 0 ? ` with ${updDelay}s notice` : ""}.` : " No player warning is configured."}\n\nProgress streams into Live activity below.`)) return;
    setUpdRunning(true);
    try {
      await api.update(updWarn, updWarn ? updDelay : 0);
      flash("Update started — follow it in Live activity.");
    } catch (e) {
      setUpdRunning(false);
      flash(`Failed to start update: ${(e as Error).message}`);
    }
  };

  const lifecycle = async (action: "start" | "stop" | "restart") => {
    const destructive = action !== "start";
    if (destructive && !confirm(`${action === "stop" ? "Stop" : "Restart"} the server?${useWarn ? ` Players will be warned and it will happen in ${delay}s.` : " This happens immediately."}`)) return;
    setBusy(action);
    try {
      await api.lifecycle(action, useWarn && destructive ? warnMsg : "", useWarn && destructive ? delay : 0);
      flash(`Server ${action} issued.`);
      load();
    } catch (e) {
      flash(`Failed: ${(e as Error).message}`);
    } finally { setBusy(""); }
  };

  const doBroadcast = async () => {
    if (!broadcastMsg.trim()) return;
    setBusy("broadcast");
    try { await api.broadcast(broadcastMsg); flash("Broadcast sent."); setBroadcastMsg(""); load(); }
    catch (e) { flash(`Failed: ${(e as Error).message}`); }
    finally { setBusy(""); }
  };

  const doSave = async () => {
    setBusy("save");
    try { await api.save(); flash("World flushed to disk (game save files updated)."); load(); }
    catch (e) { flash(`Failed: ${(e as Error).message}`); }
    finally { setBusy(""); }
  };

  return (
    <>
      <div className="page-head">
        <div>
          <div className="page-title">Server Admin</div>
          <div className="page-sub">Lifecycle, broadcast, and backups</div>
        </div>
        {note && <span className="admin-note">{note}</span>}
      </div>

      <div className="grid">
        {/* Lifecycle */}
        <div className="card span6">
          <div className="card-label">Server control</div>
          <div className="server-state-line">
            <span className={"pill " + (online === null ? "" : online ? "online" : "offline")}>
              <span className="dot" />
              {online === null ? "Checking…" : online ? "Server running" : "Server stopped"}
            </span>
          </div>
          <div className="admin-btn-row">
            <button className={"admin-btn start" + (online ? " lit-good" : "")}
              disabled={busy === "start" || online === true} onClick={() => lifecycle("start")}>Start</button>
            <button className="admin-btn restart"
              disabled={busy === "restart" || online === false} onClick={() => lifecycle("restart")}>Restart</button>
            <button className={"admin-btn stop" + (online === false ? " lit-bad" : "")}
              disabled={busy === "stop" || online === false} onClick={() => lifecycle("stop")}>Stop</button>
            <button className="admin-btn" disabled={busy === "save" || online === false} onClick={doSave}>Force save</button>
          </div>
          <label className="admin-check">
            <input type="checkbox" checked={useWarn} onChange={(e) => setUseWarn(e.target.checked)} />
            Warn players before stop/restart
          </label>
          {useWarn && (
            <div className="admin-warn-opts">
              <input className="admin-input" value={warnMsg} onChange={(e) => setWarnMsg(e.target.value)} placeholder="Warning message" />
              <div className="admin-delay">
                <label>Delay</label>
                <input type="number" min={0} max={600} value={delay} onChange={(e) => setDelay(Math.max(0, +e.target.value))} />
                <span>seconds</span>
              </div>
            </div>
          )}
        </div>

        {/* Broadcast */}
        <div className="card span6">
          <div className="card-label">Broadcast message</div>
          <input className="admin-input" value={broadcastMsg} onChange={(e) => setBroadcastMsg(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && doBroadcast()} placeholder="Message to all players" />
          <button className="admin-btn" style={{ marginTop: 12 }} disabled={busy === "broadcast" || !broadcastMsg.trim()} onClick={doBroadcast}>Send broadcast</button>
        </div>

        {/* Server update */}
        <div className="card span6">
          <div className="card-label">Server update</div>
          <div className="upd-status">
            <div className="upd-current">
              <span className="upd-k">Current</span>
              <span className="upd-v">{version || "—"}{check?.local_buildid ? ` · build ${check.local_buildid}` : ""}</span>
            </div>
            <UpdateBadge check={check} onCheckNow={checkNow} />
          </div>
          <div className="upd-desc">
            If a new build exists: warn players, save the world, stop, back
            up, update via SteamCMD, restart, and verify. If already up to
            date, nothing is touched.
          </div>
          <input className="admin-input" value={updWarn} onChange={(e) => setUpdWarn(e.target.value)}
            placeholder="Warning message (empty = no warning)" />
          <div className="admin-delay" style={{ marginTop: 10 }}>
            <label>Delay</label>
            <input type="number" min={0} max={600} value={updDelay} onChange={(e) => setUpdDelay(Math.max(0, +e.target.value))} />
            <span>seconds</span>
          </div>
          <button className={"admin-btn" + (check?.update_available ? " lit-warn" : "")} style={{ marginTop: 14 }} disabled={updRunning || online === false} onClick={doUpdate}>
            {updRunning ? "Update running…" : check?.update_available ? "Install update" : "Check & update"}
          </button>
          {updRunning && <div className="upd-running">Follow progress in Live activity below.</div>}
        </div>

        {/* History */}
        <div className="card span6">
          <div className="card-label">Recent actions</div>
          <div className="admin-history">
            {history.length === 0 && <div className="pempty">No actions yet this session.</div>}
            {history.map((h, i) => (
              <div key={i} className="history-row">
                <span className={"history-dot " + (h.ok ? "ok" : "bad")} />
                <span className="history-action">{h.action}</span>
                {h.detail && <span className="history-detail">{h.detail}</span>}
                <span className="history-time">{new Date(h.time).toLocaleTimeString()}</span>
              </div>
            ))}
          </div>
        </div>

        {/* Live activity (SSE) */}
        <div className="livelog-wrap">
          <LiveLog events={events} connected={connected} onClear={clear} />
        </div>


      </div>
    </>
  );
}



function UpdateBadge({ check, onCheckNow }: { check: UpdateCheckResponse | null; onCheckNow: () => void }) {
  if (!check) return null;
  if (check.checking) {
    return <span className="upd-badge checking">Checking Steam…</span>;
  }
  if (check.error) {
    return (
      <span className="upd-badge err" title={check.error}>
        Check failed · <a onClick={onCheckNow}>retry</a>
      </span>
    );
  }
  if (!check.checked_at) {
    return <span className="upd-badge idle"><a onClick={onCheckNow}>Check for updates</a></span>;
  }
  if (check.update_available) {
    return (
      <span className="upd-badge avail">
        Update available: {check.local_buildid} → {check.remote_buildid}
      </span>
    );
  }
  return (
    <span className="upd-badge current">
      Up to date · checked {ago(check.checked_at)} · <a onClick={onCheckNow}>check now</a>
    </span>
  );
}

function ago(iso: string): string {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)}m ago`;
  if (s < 129600) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

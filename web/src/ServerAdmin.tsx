import { useEffect, useState, useCallback } from "react";
import { api, HistoryEntry } from "./api";
import { useServerState } from "./useServerState";

export function ServerAdmin() {
  const [history, setHistory] = useState<HistoryEntry[]>([]);
  const [broadcastMsg, setBroadcastMsg] = useState("");
  const [warnMsg, setWarnMsg] = useState("Server maintenance shortly — please reach a safe spot.");
  const [delay, setDelay] = useState(30);
  const [useWarn, setUseWarn] = useState(true);
  const [busy, setBusy] = useState("");
  const [note, setNote] = useState("");
  const { online } = useServerState();

  const load = useCallback(() => {
    api.history().then((r) => setHistory(r.history ?? [])).catch(() => {});
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [load]);

  const flash = (m: string) => { setNote(m); setTimeout(() => setNote(""), 4000); };

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
          </div>        {/* History */}
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
      </div>
    </>
  );
}



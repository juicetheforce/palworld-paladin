import { useEffect, useState, useCallback } from "react";
import { api, BackupInfo, HistoryEntry } from "./api";

export function ServerAdmin() {
  const [backups, setBackups] = useState<BackupInfo[]>([]);
  const [history, setHistory] = useState<HistoryEntry[]>([]);
  const [broadcastMsg, setBroadcastMsg] = useState("");
  const [warnMsg, setWarnMsg] = useState("Server maintenance shortly — please reach a safe spot.");
  const [delay, setDelay] = useState(30);
  const [useWarn, setUseWarn] = useState(true);
  const [busy, setBusy] = useState("");
  const [note, setNote] = useState("");

  const load = useCallback(() => {
    api.adminBackups().then((r) => setBackups(r.backups ?? [])).catch(() => {});
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
    try { await api.save(); flash("World saved."); load(); }
    catch (e) { flash(`Failed: ${(e as Error).message}`); }
    finally { setBusy(""); }
  };

  const delBackup = async (id: string) => {
    if (!confirm(`Delete backup ${id}? This cannot be undone.`)) return;
    setBusy("del" + id);
    try { await api.deleteBackup(id); flash("Backup deleted."); load(); }
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
          <div className="admin-btn-row">
            <button className="admin-btn start" disabled={busy === "start"} onClick={() => lifecycle("start")}>Start</button>
            <button className="admin-btn restart" disabled={busy === "restart"} onClick={() => lifecycle("restart")}>Restart</button>
            <button className="admin-btn stop" disabled={busy === "stop"} onClick={() => lifecycle("stop")}>Stop</button>
            <button className="admin-btn" disabled={busy === "save"} onClick={doSave}>Force save</button>
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

        {/* Backups */}
        <div className="card span6">
          <div className="card-label">Backups ({backups.length})</div>
          <div className="admin-backups">
            {backups.length === 0 && <div className="pempty">No backups yet.</div>}
            {backups.map((b) => (
              <div key={b.id} className="backup-row">
                <div>
                  <div className="backup-id">{b.id}</div>
                  <div className="backup-meta">{b.trigger} · {fmtSize(b.size_bytes)} · {new Date(b.created).toLocaleString()}</div>
                </div>
                <button className="act ban" disabled={busy === "del" + b.id} onClick={() => delBackup(b.id)}>Delete</button>
              </div>
            ))}
          </div>
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
      </div>
    </>
  );
}

function fmtSize(b: number): string {
  if (b >= 1 << 30) return (b / (1 << 30)).toFixed(1) + " GB";
  if (b >= 1 << 20) return (b / (1 << 20)).toFixed(1) + " MB";
  if (b >= 1 << 10) return (b / (1 << 10)).toFixed(0) + " KB";
  return b + " B";
}

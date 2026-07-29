import { useEffect, useState, useCallback } from "react";
import { api, BackupInfo } from "./api";
import { useServerState } from "./useServerState";
import { useEventStream } from "./useEventStream";
import { LiveLog } from "./LiveLog";

export function Backups() {
  const [backups, setBackups] = useState<BackupInfo[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState<"" | "create" | "restore" | "delete">("");
  const [note, setNote] = useState("");
  const [warnMsg, setWarnMsg] = useState("Restoring a world backup — you will be disconnected briefly.");
  const [delay, setDelay] = useState(30);
  const [useWarn, setUseWarn] = useState(true);
  const { online } = useServerState();
  const { events, connected, clear } = useEventStream();

  const load = useCallback(() => {
    api.adminBackups().then((r) => setBackups(r.backups ?? [])).catch(() => {});
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [load]);

  // Create/restore run in the background; their terminal events on the
  // live stream clear the busy state and refresh the list.
  useEffect(() => {
    const last = events[events.length - 1];
    if (last && (last.op === "backup" || last.op === "restore") && (last.kind === "done" || last.kind === "error")) {
      setBusy("");
      load();
    }
  }, [events, load]);

  const flash = (m: string) => { setNote(m); setTimeout(() => setNote(""), 4000); };

  const toggle = (id: string) =>
    setSelected((s) => { const n = new Set(s); n.has(id) ? n.delete(id) : n.add(id); return n; });
  const allSelected = backups.length > 0 && selected.size === backups.length;
  const toggleAll = () =>
    setSelected(allSelected ? new Set() : new Set(backups.map((b) => b.id)));

  const doCreate = async () => {
    setBusy("create");
    try { await api.createBackup(); flash("Backup started — follow it in Live activity."); }
    catch (e) { setBusy(""); flash(`Failed: ${(e as Error).message}`); }
  };

  const doDeleteSelected = async () => {
    if (!confirm(`Delete ${selected.size} backup(s)? This cannot be undone.`)) return;
    setBusy("delete");
    try {
      const r = await api.deleteBackups([...selected]);
      const failures = Object.keys(r.failed).length;
      flash(failures ? `Deleted ${r.deleted}, ${failures} failed.` : `Deleted ${r.deleted} backup(s).`);
      setSelected(new Set());
      load();
    } catch (e) { flash(`Failed: ${(e as Error).message}`); }
    finally { setBusy(""); }
  };

  const doRestore = async (b: BackupInfo) => {
    const liveNote = online === false
      ? "The server is stopped — it will be started after the restore."
      : useWarn ? `Players get ${delay}s warning.` : "No player warning.";
    if (!confirm(`Restore backup ${b.id}?\n\nThis REPLACES the current world (a pre-restore safety copy is kept). ${liveNote}`)) return;
    setBusy("restore");
    try {
      await api.restoreBackup(b.id, useWarn ? warnMsg : "", useWarn ? delay : 0);
      flash("Restore started — follow it in Live activity.");
    } catch (e) { setBusy(""); flash(`Failed: ${(e as Error).message}`); }
  };

  return (
    <>
      <div className="page-head">
        <div>
          <div className="page-title">Backups</div>
          <div className="page-sub">Paladin's event-anchored world backups</div>
        </div>
        <div className="head-actions">
          {note && <span className="admin-note">{note}</span>}
          <button className="admin-btn" disabled={busy !== ""} onClick={doCreate}>
            {busy === "create" ? "Backing up…" : "Create backup"}
          </button>
        </div>
      </div>

      {/* Restore warning options */}
      <div className="card" style={{ marginBottom: 16 }}>
        <label className="admin-check">
          <input type="checkbox" checked={useWarn} onChange={(e) => setUseWarn(e.target.checked)} />
          Warn players before a restore
        </label>
        {useWarn && (
          <div className="admin-warn-opts" style={{ marginTop: 10 }}>
            <input className="admin-input" value={warnMsg} onChange={(e) => setWarnMsg(e.target.value)} />
            <div className="admin-delay">
              <label>Delay</label>
              <input type="number" min={0} max={600} value={delay} onChange={(e) => setDelay(Math.max(0, +e.target.value))} />
              <span>seconds</span>
            </div>
          </div>
        )}
      </div>

      {/* Selection toolbar */}
      {selected.size > 0 && (
        <div className="sel-bar">
          <span>{selected.size} selected</span>
          <button className="act ban" disabled={busy !== ""} onClick={doDeleteSelected}>Delete selected</button>
        </div>
      )}

      <div className="card" style={{ padding: 0, overflow: "hidden" }}>
        <table className="ptable">
          <thead>
            <tr>
              <th style={{ width: 40 }}>
                <input type="checkbox" className="bk-check" checked={allSelected} onChange={toggleAll} />
              </th>
              <th>Backup</th>
              <th>Trigger</th>
              <th>Size</th>
              <th>Created</th>
              <th style={{ textAlign: "right" }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {backups.length === 0 && (
              <tr><td colSpan={6} className="pempty">No backups yet. Create one, or they appear automatically before commits, restores, and updates.</td></tr>
            )}
            {backups.map((b) => (
              <tr key={b.id}>
                <td><input type="checkbox" className="bk-check" checked={selected.has(b.id)} onChange={() => toggle(b.id)} /></td>
                <td><div className="pid" style={{ fontSize: 13 }}>{b.id}</div></td>
                <td><span className={"trigger-badge t-" + b.trigger}>{b.trigger}</span></td>
                <td>{fmtSize(b.size_bytes)}</td>
                <td className="bk-date">{new Date(b.created).toLocaleString()}</td>
                <td style={{ textAlign: "right" }}>
                  <button className="act unban" disabled={busy !== ""}
                    title={online === false ? "Server is stopped — it will be started after the restore" : ""}
                    onClick={() => doRestore(b)}>Restore</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div style={{ marginTop: 18 }}>
        <LiveLog events={events} connected={connected} onClear={clear} />
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

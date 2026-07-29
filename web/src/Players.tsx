import { useEffect, useState, useCallback } from "react";
import { api, RosterPlayer, BanEntry } from "./api";
import { OfflineNotice } from "./OfflineNotice";

export function Players() {
  const [players, setPlayers] = useState<RosterPlayer[]>([]);
  const [bans, setBans] = useState<BanEntry[]>([]);
  const [online, setOnline] = useState(true);
  const [historyTier, setHistoryTier] = useState(false);
  const [busy, setBusy] = useState<string>("");

  const load = useCallback(() => {
    api.players()
      .then((r) => { setPlayers(r.players ?? []); setOnline(r.online); setHistoryTier(r.history_tier); })
      .catch(() => setOnline(false));
    api.bans().then((r) => setBans(r.bans ?? [])).catch(() => {});
  }, []);

  useEffect(() => {
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [load]);

  const act = async (action: "kick" | "ban" | "unban", userId: string, name: string) => {
    const verb = action === "unban" ? "unban" : action;
    if (action !== "kick" && !confirm(`${verb.charAt(0).toUpperCase() + verb.slice(1)} ${name || userId}?`)) return;
    setBusy(userId + action);
    try {
      await api.playerAction(action, userId);
      load();
    } catch (e) {
      alert(`Failed to ${verb}: ${(e as Error).message}`);
    } finally {
      setBusy("");
    }
  };

  const onlinePlayers = (players ?? []).filter((p) => p.online);

  return (
    <>
      <div className="page-head">
        <div>
          <div className="page-title">Players</div>
          <div className="page-sub">Live roster &amp; moderation · refreshes every 5s</div>
        </div>
        <span className={"pill " + (online ? "online" : "offline")}>
          <span className="dot" />{online ? `${onlinePlayers.length} online` : "Server offline"}
        </span>
      </div>

      {!online ? <OfflineNotice what="player data" /> : (
      <>
      {/* Roster table */}
      <div className="card" style={{ padding: 0, overflow: "hidden" }}>
        <table className="ptable">
          <thead>
            <tr>
              <th style={{ width: 34 }}></th>
              <th>Player</th>
              <th>Level</th>
              <th>Guild</th>
              <th>Bases</th>
              <th>Ping</th>
              <th style={{ textAlign: "right" }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {onlinePlayers.map((p) => (
              <tr key={p.user_id}>
                <td><span className="pdot online" title="Online" /></td>
                <td>
                  <div className="pname">{p.name || "(unknown)"}</div>
                  <div className="pid">{p.user_id}</div>
                </td>
                <td>{p.level || "—"}</td>
                <td className="reserved">{p.guild ?? "—"}</td>
                <td className="reserved">{p.bases ?? "—"}</td>
                <td>{p.ping ? `${p.ping.toFixed(0)} ms` : "—"}</td>
                <td style={{ textAlign: "right" }}>
                  <button className="act kick" disabled={busy === p.user_id + "kick"} onClick={() => act("kick", p.user_id, p.name)}>Kick</button>
                  <button className="act ban" disabled={busy === p.user_id + "ban"} onClick={() => act("ban", p.user_id, p.name)}>Ban</button>
                </td>
              </tr>
            ))}
            {online && onlinePlayers.length === 0 && (
              <tr><td colSpan={7} className="pempty">No players connected.</td></tr>
            )}
          </tbody>
        </table>

        {/* Offline / history tier — reserved slot until save parsing exists */}
        {!historyTier && (
          <div className="history-reserved">
            <span className="pdot offline" /> Offline players, guild names, and base
            counts appear here once world-save parsing is enabled.
          </div>
        )}
      </div>

      {/* Ban list */}
      <div className="page-head" style={{ marginTop: 28 }}>
        <div className="page-title" style={{ fontSize: 17 }}>Ban list</div>
      </div>
      <div className="card" style={{ padding: 0, overflow: "hidden" }}>
        <table className="ptable">
          <tbody>
            {bans.length === 0 && <tr><td className="pempty">No bans.</td></tr>}
            {bans.map((b) => (
              <tr key={b.user_id}>
                <td style={{ width: 34 }}><span className="pdot banned" /></td>
                <td><div className="pid" style={{ fontSize: 13 }}>{b.user_id}</div></td>
                <td style={{ textAlign: "right" }}>
                  <button className="act unban" disabled={busy === b.user_id + "unban"} onClick={() => act("unban", b.user_id, b.user_id)}>Unban</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      </>
      )}
    </>
  );
}

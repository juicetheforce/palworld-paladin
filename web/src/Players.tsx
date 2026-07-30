import { useEffect, useState, useCallback } from "react";
import { api, RosterPlayer, BanEntry, WorldResponse } from "./api";
import { OfflineNotice } from "./OfflineNotice";

export function Players() {
  const [players, setPlayers] = useState<RosterPlayer[]>([]);
  const [bans, setBans] = useState<BanEntry[]>([]);
  const [online, setOnline] = useState(true);
  const [world, setWorld] = useState<WorldResponse | null>(null);
  const [busy, setBusy] = useState<string>("");

  const load = useCallback(() => {
    api.players()
      .then((r) => { setPlayers(r.players ?? []); setOnline(r.online); })
      .catch(() => setOnline(false));
    api.bans().then((r) => setBans(r.bans ?? [])).catch(() => {});
  }, []);

  useEffect(() => {
    load();
    const id = setInterval(load, 5000);
    const widRef = setInterval(() => api.world().then(setWorld).catch(() => {}), 60000);
    api.world().then(setWorld).catch(() => {});
    return () => { clearInterval(id); clearInterval(widRef); };
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

  const guildByNick = new Map<string, { guild: string; bases: number; lastOnline: string }>();
  (world?.guilds ?? []).forEach((g) =>
    g.players.forEach((m) =>
      guildByNick.set(m.nickname, { guild: g.name, bases: g.base_ids.length, lastOnline: m.last_online })));
  const palsByNick = new Map<string, number>();
  (world?.players ?? []).forEach((p) => palsByNick.set(p.nickname, p.pals?.length ?? 0));
  const onlineNicks = new Set(onlinePlayers.map((p) => p.name));
  const knownOffline = (world?.players ?? []).filter((p) => !onlineNicks.has(p.nickname));

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
              <th>Pals</th>
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
                <td>{guildByNick.get(p.name)?.guild || "—"}</td>
                <td>{guildByNick.get(p.name)?.bases ?? "—"}</td>
                <td>{palsByNick.has(p.name) ? palsByNick.get(p.name) : "—"}</td>
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

      {/* Historical tier (save parsing): all known players + guilds — the
          save is on disk, so this renders even when the server is down. */}
      <div className="page-head" style={{ marginTop: 28 }}>
        <div>
          <div className="page-title" style={{ fontSize: 17 }}>Known players</div>
          <div className="page-sub">
            {world?.available
              ? `from the world save · parsed ${world.parsed_at ? new Date(world.parsed_at).toLocaleTimeString() : ""}`
              : "from the world save"}
          </div>
        </div>
      </div>

      {!world || !world.available ? (
        <div className="card history-reserved" style={{ display: "block" }}>
          {world?.reason === "sidecar" ? (
            <>Save parsing needs the <b>sav_cli</b> sidecar. Place it at{" "}
              <code>/home/palworld/paladin-tools/sav_cli</code> (from a
              palworld-server-tool release) and this section fills in
              automatically — no restart needed.</>
          ) : world?.reason === "parse" ? (
            <>Save parsing failed: <span className="perr">{world.error}</span></>
          ) : (
            <>Loading world data…</>
          )}
        </div>
      ) : (
        <>
          <div className="card" style={{ padding: 0, overflow: "hidden" }}>
            <table className="ptable">
              <thead>
                <tr>
                  <th style={{ width: 34 }}></th>
                  <th>Player</th>
                  <th>Level</th>
                  <th>Guild</th>
                  <th>Pals</th>
                  <th>Last online</th>
                </tr>
              </thead>
              <tbody>
                {knownOffline.length === 0 && (
                  <tr><td colSpan={6} className="pempty">Every known player is currently online.</td></tr>
                )}
                {knownOffline.map((p) => (
                  <tr key={p.player_uid}>
                    <td><span className="pdot offline" title="Offline" /></td>
                    <td>
                      <div className="pname">{p.nickname || "(unknown)"}</div>
                      <div className="pid">uid {p.player_uid}</div>
                    </td>
                    <td>{p.level || "—"}</td>
                    <td>{guildByNick.get(p.nickname)?.guild || "—"}</td>
                    <td>{p.pals?.length ?? 0}</td>
                    <td className="bk-date">{guildByNick.get(p.nickname)?.lastOnline || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {(world.guilds ?? []).length > 0 && (
            <>
              <div className="page-head" style={{ marginTop: 28 }}>
                <div className="page-title" style={{ fontSize: 17 }}>Guilds</div>
              </div>
              <div className="card" style={{ padding: 0, overflow: "hidden" }}>
                <table className="ptable">
                  <thead>
                    <tr><th>Guild</th><th>Camp level</th><th>Members</th><th>Bases</th></tr>
                  </thead>
                  <tbody>
                    {world.guilds!.map((g, i) => (
                      <tr key={i}>
                        <td><div className="pname">{g.name || "(unnamed)"}</div></td>
                        <td>{g.base_camp_level}</td>
                        <td>{g.players.map((m) => m.nickname).join(", ")}</td>
                        <td>{g.base_ids.length}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )}
        </>
      )}
    </>
  );
}

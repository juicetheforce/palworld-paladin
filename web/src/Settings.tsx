import { useEffect, useMemo, useState, useCallback } from "react";
import { api, SettingsKey } from "./api";
import { useServerState } from "./useServerState";
import { useEventStream } from "./useEventStream";
import { LiveLog } from "./LiveLog";
import { OfflineNotice } from "./OfflineNotice";

const CAT_LABELS: Record<string, string> = {
  server: "Server", balance: "Balance", features: "Features",
  performance: "Performance", pvp: "PvP",
};

// Display form of a raw ini value, per type: floats lose their trailing
// zeros, strings lose their quotes, everything else passes through.
function displayValue(def: SettingsKey, raw: string | undefined): string {
  if (raw === undefined) return def.default === null ? "" : String(def.default);
  if (def.type === "float" || def.type === "int") {
    const n = parseFloat(raw);
    return isNaN(n) ? raw : String(n);
  }
  if ((def.type === "string" || def.type === "list") && raw.length >= 2 && raw.startsWith('"') && raw.endsWith('"')) {
    return raw.slice(1, -1);
  }
  if (def.type === "bool") return raw.toLowerCase() === "true" ? "True" : "False";
  return raw;
}

export function Settings() {
  const [keys, setKeys] = useState<SettingsKey[]>([]);
  const [values, setValues] = useState<Record<string, string>>({});
  const [staged, setStaged] = useState<Record<string, string>>({});
  const [cat, setCat] = useState("all");
  const [q, setQ] = useState("");
  const [committing, setCommitting] = useState(false);
  const [errNote, setErrNote] = useState("");
  const [reviewOpen, setReviewOpen] = useState(false);
  const [warnMsg, setWarnMsg] = useState("Server settings changing — restarting shortly.");
  const [delay, setDelay] = useState(60);
  const [note, setNote] = useState("");
  const { online } = useServerState();
  const { events, connected, clear } = useEventStream();

  const [loadErr, setLoadErr] = useState("");
  const load = useCallback(() => {
    api.settings().then((r) => {
      setKeys(r.keys ?? []);
      setValues(r.values ?? {});
      setLoadErr("");
    }).catch((e: Error) => setLoadErr(e.message || "settings load failed"));
  }, []);
  useEffect(load, [load]);

  // Commit end: clear busy, reload current values, drop the stage.
  useEffect(() => {
    const last = events[events.length - 1];
    if (last && last.op === "commit" && (last.kind === "done" || last.kind === "error")) {
      setCommitting(false);
      if (last.kind === "done") setStaged({});
      load();
    }
  }, [events, load]);

  const current = useCallback(
    (def: SettingsKey) => displayValue(def, values[def.key]),
    [values]
  );

  const edit = (def: SettingsKey, v: string) => {
    setStaged((s) => {
      const n = { ...s };
      if (v === current(def)) delete n[def.key];
      else n[def.key] = v;
      return n;
    });
  };

  const revert = (key: string) =>
    setStaged((s) => { const n = { ...s }; delete n[key]; return n; });

  const defaultString = (def: SettingsKey): string => {
    const d = def.default;
    if (typeof d === "boolean") return d ? "True" : "False";
    if (d === null || d === undefined) return "";
    return String(d);
  };
  const resetAllToDefaults = () => {
    const diff: Record<string, string> = {};
    for (const k of keys) {
      if (k.protected) continue; // never bulk-reset protected keys (ports, passwords)
      const dv = defaultString(k);
      if (dv !== "" && current(k) !== dv) diff[k.key] = dv;
    }
    const n = Object.keys(diff).length;
    if (n === 0) { setNote("Everything already matches defaults."); setTimeout(() => setNote(""), 4000); return; }
    if (!confirm(`Stage ${n} change(s) resetting every non-protected setting to its install default?\n\nNothing applies until you hit Apply & restart — review the list first.`)) return;
    setStaged(diff);
    setReviewOpen(true);
  };

  const cats = useMemo(() => {
    const c: Record<string, number> = {};
    keys.forEach((k) => { c[k.category] = (c[k.category] ?? 0) + 1; });
    return c;
  }, [keys]);

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return keys.filter((k) =>
      (cat === "all" || k.category === cat) &&
      (!needle || k.key.toLowerCase().includes(needle) || k.tooltip.toLowerCase().includes(needle)));
  }, [keys, cat, q]);

  const stagedCount = Object.keys(staged).length;

  // Out-of-range staged values, validated against the key list's min/max —
  // the server pre-check would reject these anyway; catching them at the
  // field means the rejection can't be silent (the 51-instead-of-5 lesson).
  const rangeError = useCallback((def: SettingsKey, v: string): string | null => {
    if (def.type !== "int" && def.type !== "float") return null;
    const n = Number(v);
    if (v.trim() === "" || Number.isNaN(n)) return "not a number";
    if (def.min != null && n < def.min) return `min ${def.min}`;
    if (def.max != null && n > def.max) return `max ${def.max}`;
    return null;
  }, []);
  const invalidStaged = useMemo(() => {
    const bad: Record<string, string> = {};
    for (const [key, v] of Object.entries(staged)) {
      const def = keys.find((k) => k.key === key);
      if (!def) continue;
      const err = rangeError(def, v);
      if (err) bad[key] = err;
    }
    return bad;
  }, [staged, keys, rangeError]);
  const invalidCount = Object.keys(invalidStaged).length;

  const doCommit = async () => {
    if (!confirm(`Apply ${stagedCount} setting change(s)?\n\nThis runs the full cycle: warn → save → stop → backup → apply → restart → verify. ${warnMsg ? `Players get ${delay}s warning.` : "No player warning."}`)) return;
    setCommitting(true);
    try {
      await api.commitSettings(staged, warnMsg, warnMsg ? delay : 0);
      setNote("Commit started — follow it in Live activity below.");
      setTimeout(() => setNote(""), 4000);
    } catch (e) {
      setCommitting(false);
      // Persistent, not a 6-second note: a rejected commit must be unmissable.
      setErrNote(`Commit rejected: ${(e as Error).message}`);
    }
  };

  return (
    <>
      <div className="page-head">
        <div>
          <div className="page-title">Server Settings</div>
          <div className="page-sub">Staged changes apply together in one restart — nothing is live until you hit Apply</div>
        </div>
        {note && <span className="admin-note">{note}</span>}
        {errNote && <span className="admin-note set-err">{errNote} <a onClick={() => setErrNote("")}>dismiss</a></span>}
      </div>
      {online === false && <OfflineNotice what="Settings can be browsed and staged, but Apply needs a running server" />}

      <div className="set-layout">
        <aside className="set-side">
          <input className="admin-input set-search" placeholder="Search settings…" value={q} onChange={(e) => setQ(e.target.value)} />
          <button className="admin-btn set-reset" title="Stage every non-protected setting back to its install default (review before applying)" onClick={resetAllToDefaults}>Reset all to defaults</button>
          <button className={"set-cat" + (cat === "all" ? " on" : "")} onClick={() => setCat("all")}>
            All <span className="set-count">{keys.length}</span>
          </button>
          {Object.entries(CAT_LABELS).filter(([c]) => cats[c]).map(([c, label]) => (
            <button key={c} className={"set-cat" + (cat === c ? " on" : "")} onClick={() => setCat(c)}>
              {label} <span className="set-count">{cats[c]}</span>
            </button>
          ))}
        </aside>

        <div className="set-main card" style={{ padding: 0 }}>
          {loadErr && (
            <div className="pempty" style={{ color: "var(--bad)" }}>
              Could not load settings: {loadErr}
            </div>
          )}
          {!loadErr && shown.length === 0 && <div className="pempty">No settings match.</div>}
          {shown.map((k) => {
            const isStaged = k.key in staged;
            const val = isStaged ? staged[k.key] : current(k);
            const locked = !!k.protected;
            const err = isStaged ? invalidStaged[k.key] : null;
            return (
              <div key={k.key} className={"set-row" + (isStaged ? " staged" : "") + (locked ? " locked" : "") + (err ? " invalid" : "")}>
                <div className="set-name">
                  <span className="set-key" title={k.tooltip}>{k.key}</span>
                  {k.gotcha && <span className="set-gotcha" title={k.gotcha ?? undefined}>⚠</span>}
                  {locked && <span className="set-lock" title={k.protected ?? undefined}>🔒</span>}
                  {isStaged && <span className="set-orig">was {current(k)}</span>}
                </div>
                <div className="set-ctl">
                  {err && <span className="set-err">{err} (allowed: {k.min ?? "…"}–{k.max ?? "…"})</span>}
                  {isStaged && <button className="set-revert" title="Revert this change" onClick={() => revert(k.key)}>↺</button>}
                  {k.type === "bool" ? (
                    <select disabled={locked} value={val} onChange={(e) => edit(k, e.target.value)}>
                      <option>True</option><option>False</option>
                    </select>
                  ) : k.type === "enum" ? (
                    <select disabled={locked} value={val} onChange={(e) => edit(k, e.target.value)}>
                      {(k.enum ?? []).map((o) => <option key={o}>{o}</option>)}
                    </select>
                  ) : k.type === "int" || k.type === "float" ? (
                    <input type="number" disabled={locked} value={val}
                      step={k.type === "int" ? 1 : 0.1}
                      onChange={(e) => edit(k, e.target.value)} />
                  ) : (
                    <input type="text" disabled={locked} value={val} onChange={(e) => edit(k, e.target.value)} />
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </div>

      {stagedCount > 0 && (
        <div className="commit-bar">
          <div className="commit-info">
            <b>{stagedCount} change{stagedCount > 1 ? "s" : ""} staged</b>
            <a onClick={() => setReviewOpen(!reviewOpen)}>{reviewOpen ? "hide" : "review"}</a>
            <a onClick={() => setStaged({})}>revert all</a>
          </div>
          {reviewOpen && (
            <div className="commit-review">
              {Object.entries(staged).map(([key, v]) => {
                const def = keys.find((k) => k.key === key)!;
                return <div key={key} className="commit-diff"><span className="ck">{key}</span> <span className="cold">{current(def)}</span> → <span className="cnew">{v}</span></div>;
              })}
            </div>
          )}
          <div className="commit-controls">
            <input className="admin-input" value={warnMsg} onChange={(e) => setWarnMsg(e.target.value)} placeholder="Warning message (empty = no warning)" />
            {warnMsg && (
              <div className="admin-delay">
                <label>Delay</label>
                <input type="number" min={0} max={600} value={delay} onChange={(e) => setDelay(Math.max(0, +e.target.value))} />
                <span>s</span>
              </div>
            )}
            {invalidCount > 0 && (
              <span className="commit-blocked">
                {invalidCount} value{invalidCount > 1 ? "s" : ""} out of range — fix or revert to apply
              </span>
            )}
            <button type="button" className="admin-btn lit-warn" disabled={committing || online === false || invalidCount > 0} onClick={doCommit}>
              {committing ? "Applying…" : "Apply & restart"}
            </button>
          </div>
        </div>
      )}

      <div style={{ marginTop: 18 }}>
        <LiveLog events={events} connected={connected} onClear={clear} />
      </div>
    </>
  );
}

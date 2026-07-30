import { useEffect, useRef } from "react";
import { LiveEvent } from "./useEventStream";

// The live activity viewer: streams server-log lines and operation
// progress together, newest at the bottom, auto-scrolling. Progress/done/
// error events are styled distinctly from plain log lines so a running
// operation stands out from the log noise.
export function LiveLog({ events, connected, onClear }: {
  events: LiveEvent[];
  connected: boolean;
  onClear: () => void;
}) {
  const endRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const pinnedRef = useRef(true);

  // Auto-scroll to bottom only if the user is already near the bottom, so
  // scrolling up to read history isn't yanked back down by new lines.
  useEffect(() => {
    if (pinnedRef.current && endRef.current) {
      endRef.current.scrollIntoView({ behavior: "smooth", block: "end" });
    }
  }, [events]);

  const onScroll = () => {
    const el = bodyRef.current;
    if (!el) return;
    pinnedRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 60;
  };

  return (
    <div className="card livelog-card">
      <div className="livelog-head">
        <div className="card-label" style={{ margin: 0 }}>Live activity</div>
        <div className="livelog-tools">
          <span className={"livelog-conn " + (connected ? "on" : "off")}>
            <span className="dot" />{connected ? "streaming" : "reconnecting…"}
          </span>
          <button className="livelog-clear" onClick={onClear}>Clear</button>
        </div>
      </div>
      <div className="livelog-body" ref={bodyRef} onScroll={onScroll}>
        {events.length === 0 && (
          <div className="livelog-empty">
            Waiting for activity. Server log lines and operation progress appear here live.
          </div>
        )}
        {events.map((e, i) => (
          <div key={i} className={"livelog-line k-" + e.kind}>
            <span className="ll-time">{new Date(e.time).toLocaleTimeString()}</span>
            {e.kind !== "log" && <span className={"ll-badge b-" + e.kind}>{badge(e)}</span>}
            <span className="ll-msg">{e.msg}</span>
          </div>
        ))}
        <div ref={endRef} />
      </div>
    </div>
  );
}

function badge(e: LiveEvent): string {
  if (e.kind === "done") return e.ok ? "✓ done" : "✕ failed";
  if (e.kind === "error") return "✕ error";
  if (e.kind === "progress") return (e.op || "step").toUpperCase();
  if (e.kind === "lifecycle") return "lifecycle";
  if (e.kind === "player") return "player";
  return "";
}

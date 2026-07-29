import { useState, useEffect } from "react";
import { api } from "./api";

// Shared server-online state, polled from /api/status. Pages use this to
// (a) reflect running/stopped in their controls and (b) show a friendly
// "server is stopped" notice instead of raw palapi errors. One poll per
// consumer is fine (status is cheap); each page that needs it calls this.
export function useServerState(intervalMs = 5000) {
  const [online, setOnline] = useState<boolean | null>(null); // null = not yet known
  const [checking, setChecking] = useState(true);

  useEffect(() => {
    let alive = true;
    const tick = () => {
      api.status()
        .then((s) => { if (alive) { setOnline(s.online); setChecking(false); } })
        .catch(() => { if (alive) { setOnline(false); setChecking(false); } });
    };
    tick();
    const id = setInterval(tick, intervalMs);
    return () => { alive = false; clearInterval(id); };
  }, [intervalMs]);

  return { online, checking };
}

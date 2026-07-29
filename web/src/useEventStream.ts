import { useEffect, useRef, useState, useCallback } from "react";

// A live event from Paladin's SSE stream (/api/events).
export interface LiveEvent {
  kind: "log" | "progress" | "lifecycle" | "error" | "done";
  time: string;
  op?: string;
  msg: string;
  step?: string;
  ok?: boolean;
  extra?: string;
}

// useEventStream subscribes to /api/events via EventSource and keeps a
// rolling buffer of recent events. Uses the generic `onmessage` as well as
// per-kind listeners so events are captured regardless of whether the
// server names them. Tracks connection state from open/error.
export function useEventStream(maxEvents = 500) {
  const [events, setEvents] = useState<LiveEvent[]>([]);
  const [connected, setConnected] = useState(false);
  const esRef = useRef<EventSource | null>(null);

  useEffect(() => {
    let closed = false;

    const push = (raw: string) => {
      try {
        const data = JSON.parse(raw) as LiveEvent;
        setEvents((prev) => {
          const next = prev.length >= maxEvents ? prev.slice(prev.length - maxEvents + 1) : prev.slice();
          next.push(data);
          return next;
        });
      } catch {
        // ignore malformed frames
      }
    };

    const connect = () => {
      if (closed) return;
      // withCredentials ensures the session cookie rides along on the
      // EventSource request (same-origin, but explicit is safer across
      // browsers).
      const es = new EventSource("/api/events", { withCredentials: true });
      esRef.current = es;

      es.onopen = () => setConnected(true);

      // Generic handler — catches events whether or not they carry a
      // named "event:" field.
      es.onmessage = (e: MessageEvent) => push(e.data);

      // Named handlers for our typed events.
      for (const kind of ["log", "progress", "lifecycle", "error", "done"]) {
        es.addEventListener(kind, (e) => push((e as MessageEvent).data));
      }

      es.onerror = () => {
        setConnected(false);
        // EventSource auto-reconnects on its own, but if it enters the
        // CLOSED state (e.g. after an auth failure) we won't recover
        // without recreating it. Recreate after a short delay.
        if (es.readyState === EventSource.CLOSED && !closed) {
          es.close();
          setTimeout(connect, 3000);
        }
      };
    };

    connect();

    return () => {
      closed = true;
      esRef.current?.close();
      esRef.current = null;
    };
  }, [maxEvents]);

  const clear = useCallback(() => setEvents([]), []);

  return { events, connected, clear };
}

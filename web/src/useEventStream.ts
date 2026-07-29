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
// rolling buffer of recent events. The browser's EventSource auto-
// reconnects if the stream drops, so this stays live across brief blips.
export function useEventStream(maxEvents = 500) {
  const [events, setEvents] = useState<LiveEvent[]>([]);
  const [connected, setConnected] = useState(false);
  const esRef = useRef<EventSource | null>(null);

  useEffect(() => {
    const es = new EventSource("/api/events");
    esRef.current = es;

    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);

    const onEvent = (e: MessageEvent) => {
      try {
        const data = JSON.parse(e.data) as LiveEvent;
        setEvents((prev) => {
          const next = prev.length >= maxEvents ? prev.slice(prev.length - maxEvents + 1) : prev.slice();
          next.push(data);
          return next;
        });
      } catch {
        // ignore malformed frames
      }
    };

    // Each event kind is dispatched under its own event name.
    for (const kind of ["log", "progress", "lifecycle", "error", "done"]) {
      es.addEventListener(kind, onEvent as EventListener);
    }

    return () => {
      es.close();
      esRef.current = null;
    };
  }, [maxEvents]);

  const clear = useCallback(() => setEvents([]), []);

  return { events, connected, clear };
}

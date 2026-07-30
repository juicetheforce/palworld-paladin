import { useState, useCallback, useEffect } from "react";

// Client-side dashboard preferences (§6.6a): which cards are visible.
// Stored in localStorage — per-viewer, no backend, fits the single-admin
// model. A card the operator hides simply isn't rendered.

const STORAGE_KEY = "paladin.dashboard.hiddenCards";

// Every toggleable card. `conditional` cards only appear in the checklist
// when the host actually provides them (e.g. temps on bare metal only).
export interface CardDef {
  id: string;
  label: string;
  conditional?: boolean;
}

export const DASHBOARD_CARDS: CardDef[] = [
  { id: "fps", label: "Server FPS" },
  { id: "players", label: "Players online" },
  { id: "server", label: "Server info" },
  { id: "cpu", label: "CPU" },
  { id: "memory", label: "Memory" },
  { id: "temp", label: "CPU temperature", conditional: true },
  { id: "network", label: "Network", conditional: true },
  { id: "activity", label: "Recent activity" },
];

function loadHidden(): Set<string> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return new Set();
    return new Set(JSON.parse(raw) as string[]);
  } catch {
    return new Set();
  }
}

export function usePrefs() {
  const [hidden, setHidden] = useState<Set<string>>(loadHidden);

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify([...hidden]));
    } catch {
      // localStorage unavailable (private mode etc.) — preferences just
      // won't persist; the dashboard still works this session.
    }
  }, [hidden]);

  const isVisible = useCallback((id: string) => !hidden.has(id), [hidden]);

  const toggle = useCallback((id: string) => {
    setHidden((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const reset = useCallback(() => setHidden(new Set()), []);

  return { isVisible, toggle, reset, hidden };
}

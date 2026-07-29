import { useState, useRef, useEffect } from "react";
import { DASHBOARD_CARDS } from "./usePrefs";

// A small dropdown of card show/hide toggles. `available` filters the
// conditional cards down to what this host actually provides, so the
// operator isn't offered a "show temperature" toggle on a box with no
// thermal sensors.
export function CustomizeMenu({
  isVisible,
  toggle,
  reset,
  available,
}: {
  isVisible: (id: string) => boolean;
  toggle: (id: string) => void;
  reset: () => void;
  available: (id: string) => boolean;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, [open]);

  const cards = DASHBOARD_CARDS.filter((c) => !c.conditional || available(c.id));

  return (
    <div className="customize" ref={ref}>
      <button className="customize-btn" onClick={() => setOpen((o) => !o)}>
        <span className="ico">☰</span> Customize
      </button>
      {open && (
        <div className="customize-menu">
          <div className="customize-head">Cards on dashboard</div>
          {cards.map((c) => (
            <label key={c.id} className="customize-row">
              <input type="checkbox" checked={isVisible(c.id)} onChange={() => toggle(c.id)} />
              <span>{c.label}</span>
            </label>
          ))}
          <div className="customize-foot" onClick={reset}>Reset to all</div>
        </div>
      )}
    </div>
  );
}

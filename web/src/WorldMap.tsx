import { useEffect, useRef, useState, useCallback } from "react";

interface MapActor {
  kind: "player" | "pal";
  name: string;
  level: number;
  hp: number;
  max_hp: number;
  species?: string;
  map_x: number;
  map_y: number;
  rot: number;
}

// Paldex coordinate extent rendered edge-to-edge (the in-game map spans
// roughly ±1000 in both axes; margin included).
const EXTENT = 1100;

export function WorldMap() {
  const [actors, setActors] = useState<MapActor[]>([]);
  const [available, setAvailable] = useState<boolean | null>(null);
  const [showPals, setShowPals] = useState(true);
  const [hasImage, setHasImage] = useState(false);
  const [view, setView] = useState({ scale: 1, tx: 0, ty: 0 });
  const drag = useRef<{ x: number; y: number } | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);

  const load = useCallback(() => {
    fetch("/api/admin/map-actors")
      .then((r) => r.json())
      .then((r: { available: boolean; actors?: MapActor[] }) => {
        setAvailable(r.available);
        if (r.available) setActors(r.actors ?? []);
      })
      .catch(() => {});
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, 5000);
    return () => clearInterval(id);
  }, [load]);

  // pct positions: x east→right, y north→up (screen top).
  const px = (a: MapActor) => ((a.map_x + EXTENT) / (2 * EXTENT)) * 100;
  const py = (a: MapActor) => ((EXTENT - a.map_y) / (2 * EXTENT)) * 100;

  const onWheel = (e: React.WheelEvent) => {
    e.preventDefault();
    setView((v) => {
      const scale = Math.min(8, Math.max(1, v.scale * (e.deltaY < 0 ? 1.15 : 0.87)));
      return scale === 1 ? { scale: 1, tx: 0, ty: 0 } : { ...v, scale };
    });
  };
  const onDown = (e: React.MouseEvent) => { drag.current = { x: e.clientX - view.tx, y: e.clientY - view.ty }; };
  const onMove = (e: React.MouseEvent) => {
    if (!drag.current) return;
    setView((v) => ({ ...v, tx: e.clientX - drag.current!.x, ty: e.clientY - drag.current!.y }));
  };
  const onUp = () => { drag.current = null; };

  const players = actors.filter((a) => a.kind === "player");
  const pals = actors.filter((a) => a.kind === "pal");

  return (
    <>
      <div className="page-head">
        <div>
          <div className="page-title">World Map</div>
          <div className="page-sub">
            Live actor positions — {players.length} player{players.length === 1 ? "" : "s"}, {pals.length} wild Pal{pals.length === 1 ? "" : "s"} in loaded areas
          </div>
        </div>
        <label className="admin-check">
          <input type="checkbox" checked={showPals} onChange={(e) => setShowPals(e.target.checked)} />
          Show wild Pals
        </label>
      </div>

      {available === false && (
        <div className="offline-note">
          Live map data is unavailable. It requires the server to be running with the
          <code> -enable-gamedata-api</code> launch flag, and reachable.
        </div>
      )}

      <div
        ref={boxRef}
        className="map-box card"
        onWheel={onWheel} onMouseDown={onDown} onMouseMove={onMove} onMouseUp={onUp} onMouseLeave={onUp}
      >
        <div
          className="map-world"
          style={{ transform: `translate(${view.tx}px, ${view.ty}px) scale(${view.scale})` }}
        >
          {hasImage && (
            <img className="map-img" src="/api/map-image" alt="" draggable={false} />
          )}
          <img src="/api/map-image" style={{ display: "none" }} alt=""
            onLoad={() => setHasImage(true)} onError={() => setHasImage(false)} />
          {!hasImage && <MapGrid />}

          {showPals && pals.map((a, i) => (
            <div key={"p" + i} className="map-dot pal" style={{ left: `${px(a)}%`, top: `${py(a)}%` }}
              title={`${a.species} · lvl ${a.level} · ${Math.round(a.hp)}/${Math.round(a.max_hp)} HP · (${Math.round(a.map_x)}, ${Math.round(a.map_y)})`} />
          ))}
          {players.map((a, i) => (
            <div key={"pl" + i} className="map-dot player" style={{ left: `${px(a)}%`, top: `${py(a)}%` }}
              title={`${a.name} · lvl ${a.level} · (${Math.round(a.map_x)}, ${Math.round(a.map_y)})`}>
              <span className="map-name">{a.name}</span>
            </div>
          ))}
        </div>
        <div className="map-legend">
          <span><i className="lg player" /> Player</span>
          <span><i className="lg pal" /> Wild Pal</span>
          <span className="map-hint">scroll to zoom · drag to pan</span>
        </div>
      </div>

      {!hasImage && (
        <div className="map-note">
          Showing coordinate grid — Paladin bundles no game artwork. To underlay the world
          map, place any Palworld map image at
          <code> /home/palworld/paladin-config/worldmap.png</code> and reload.
        </div>
      )}
    </>
  );
}

function MapGrid() {
  const lines = [];
  for (let c = -1000; c <= 1000; c += 250) {
    const p = ((c + EXTENT) / (2 * EXTENT)) * 100;
    lines.push(<div key={"v" + c} className="grid-line v" style={{ left: `${p}%` }} />);
    lines.push(<div key={"h" + c} className="grid-line h" style={{ top: `${p}%` }} />);
    lines.push(<span key={"lx" + c} className="grid-label" style={{ left: `${p}%`, bottom: 4 }}>{c}</span>);
    lines.push(<span key={"ly" + c} className="grid-label" style={{ top: `${p}%`, left: 4 }}>{-c}</span>);
  }
  return <div className="map-grid">{lines}</div>;
}

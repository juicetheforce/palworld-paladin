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

// Paldex coordinate bounds of the bundled artwork, solved EXACTLY from
// two-point calibration (2048px image; in-game (242,-510)=px(1404,999)
// and (245,-309)=px(1406,932)): scale = 3.000 paldex units/pixel (the
// stitched leaflet tile world is a power-of-two canvas ~3x larger than
// the island — why every eyeball estimate failed), north edge solved
// identically from both points (2487). If the artwork is ever replaced,
// recalibrate with two (in-game coord ↔ pixel) pairs the same way.
const XMIN = -3971, XMAX = 2173, YMIN = -3657, YMAX = 2487;

export function WorldMap() {
  const [actors, setActors] = useState<MapActor[]>([]);
  const [available, setAvailable] = useState<boolean | null>(null);
  const [showPals, setShowPals] = useState(true);
  // Artwork source chain: operator-supplied override, then the bundled
  // asset, then the coordinate grid. Whichever loads first wins.
  const [imgSrc, setImgSrc] = useState<string | null>("/api/map-image");
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
  const px = (a: MapActor) => ((a.map_x - XMIN) / (XMAX - XMIN)) * 100;
  const py = (a: MapActor) => ((YMAX - a.map_y) / (YMAX - YMIN)) * 100;

  const onWheel = (e: React.WheelEvent) => {
    e.preventDefault();
    setView((v) => {
      const scale = Math.min(8, Math.max(1, v.scale * (e.deltaY < 0 ? 1.15 : 0.87)));
      return scale === 1 ? { scale: 1, tx: 0, ty: 0 } : { ...v, scale };
    });
  };
  const onDown = (e: React.MouseEvent) => { drag.current = { x: e.clientX - view.tx, y: e.clientY - view.ty }; };
  const onMove = (e: React.MouseEvent) => {
    // Read the ref BEFORE the state updater: React may run the updater
    // after mouse-up has already cleared the ref (the black-screen race).
    const d = drag.current;
    if (!d) return;
    const tx = e.clientX - d.x, ty = e.clientY - d.y;
    setView((v) => ({ ...v, tx, ty }));
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
          {imgSrc && (
            <img className="map-img" src={imgSrc} alt="" draggable={false}
              onError={() => setImgSrc(imgSrc === "/api/map-image" ? "/worldmap.jpg" : null)} />
          )}
          {!imgSrc && <MapGrid />}

          {showPals && pals.map((a, i) => (
            <div key={"p" + i} className="map-dot pal"
              style={{ left: `${px(a)}%`, top: `${py(a)}%`, transform: `translate(-50%,-50%) scale(${1 / view.scale})` }}
              title={`${a.species} · lvl ${a.level} · ${Math.round(a.hp)}/${Math.round(a.max_hp)} HP · (${Math.round(a.map_x)}, ${Math.round(a.map_y)})`} />
          ))}
          {players.map((a, i) => (
            <div key={"pl" + i} className="map-dot player"
              style={{ left: `${px(a)}%`, top: `${py(a)}%`, transform: `translate(-50%,-50%) scale(${1 / view.scale})` }}
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

      <div className="map-note">
        Only actors in loaded areas appear — the server simulates the world
        around connected players, so wild Pals show up near people, not
        across the whole island.
        {!imgSrc && <> No map artwork found: place an image at <code>/home/palworld/paladin-config/worldmap.png</code> to underlay the radar.</>}
      </div>
    </>
  );
}

function MapGrid() {
  const lines = [];
  for (let c = -3500; c <= 2000; c += 500) {
    const pxl = ((c - XMIN) / (XMAX - XMIN)) * 100;
    const pyl = ((YMAX - c) / (YMAX - YMIN)) * 100;
    if (pxl >= 0 && pxl <= 100) {
      lines.push(<div key={"v" + c} className="grid-line v" style={{ left: `${pxl}%` }} />);
      lines.push(<span key={"lx" + c} className="grid-label" style={{ left: `${pxl}%`, bottom: 4 }}>{c}</span>);
    }
    if (pyl >= 0 && pyl <= 100) {
      lines.push(<div key={"h" + c} className="grid-line h" style={{ top: `${pyl}%` }} />);
      lines.push(<span key={"ly" + c} className="grid-label" style={{ top: `${pyl}%`, left: 4 }}>{c}</span>);
    }
  }
  return <div className="map-grid">{lines}</div>;
}

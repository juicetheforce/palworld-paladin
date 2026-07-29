// A tiny dependency-free SVG sparkline. Renders a series of numbers as a
// filled line chart scaled to its own min/max. Used for the dashboard's
// client-buffered history (FPS, network) — the Proxmox/pfSense pattern:
// history lives in the open tab, no server-side store.

interface SparklineProps {
  data: number[];
  color?: string;
  height?: number;
  // Fixed y-axis bounds; if omitted, auto-scales to the data.
  min?: number;
  max?: number;
}

export function Sparkline({ data, color = "var(--accent)", height = 44, min, max }: SparklineProps) {
  const w = 100; // viewBox width (percentage-based, stretches to container)
  const h = height;
  if (data.length < 2) {
    return <div style={{ height, display: "flex", alignItems: "center", justifyContent: "center", color: "var(--text-faint)", fontSize: 11 }}>collecting…</div>;
  }
  const lo = min ?? Math.min(...data);
  const hi = max ?? Math.max(...data);
  const range = hi - lo || 1;
  const stepX = w / (data.length - 1);
  const y = (v: number) => h - ((v - lo) / range) * (h - 4) - 2;
  const pts = data.map((v, i) => `${(i * stepX).toFixed(2)},${y(v).toFixed(2)}`);
  const line = "M " + pts.join(" L ");
  const area = `${line} L ${w},${h} L 0,${h} Z`;
  const gradId = `spark-${color.replace(/[^a-z]/gi, "")}`;

  return (
    <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" width="100%" height={h} style={{ display: "block" }}>
      <defs>
        <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.28" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill={`url(#${gradId})`} />
      <path d={line} fill="none" stroke={color} strokeWidth="1.5" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

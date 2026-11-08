import { useEffect, useMemo, useRef, useState } from "react";
import { ms, serviceColor, type TraceSummary } from "../lib/traces";

const H = 150;
const PAD = { l: 52, r: 12, t: 10, b: 22 };

function niceTicks(lo: number, hi: number, log: boolean): number[] {
  if (log) {
    const out: number[] = [];
    for (let p = Math.floor(Math.log10(lo)); p <= Math.ceil(Math.log10(hi)); p++) out.push(10 ** p);
    return out.filter((v) => v >= lo * 0.999 && v <= hi * 1.001);
  }
  const step = 10 ** Math.floor(Math.log10((hi - lo) / 3 || 1));
  const n = [1, 2, 5, 10].map((m) => m * step).find((s) => (hi - lo) / s <= 4) ?? step * 10;
  const out = [];
  for (let v = Math.ceil(lo / n) * n; v <= hi; v += n) out.push(v);
  return out;
}

/** TraceScatter plots traces by start time and duration. Failed traces are ringed in red; a click opens one. */
export function TraceScatter({
  traces,
  start,
  end,
  onOpen,
}: {
  traces: TraceSummary[];
  /** Window, in seconds. */
  start: number;
  end: number;
  onOpen: (id: string) => void;
}) {
  const el = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(600);
  const [hover, setHover] = useState<TraceSummary | null>(null);
  useEffect(() => {
    const host = el.current;
    if (!host) return;
    const ro = new ResizeObserver(() => setWidth(host.clientWidth));
    ro.observe(host);
    return () => ro.disconnect();
  }, []);

  const scale = useMemo(() => {
    const d = traces.map((t) => Math.max(t.duration, 0.001));
    const lo = Math.min(...d, 1);
    const hi = Math.max(...d, 1);
    // A log axis when durations span orders of magnitude, so fast ones don't sit in a line.
    const log = hi / lo > 50;
    const y0 = log ? 10 ** Math.floor(Math.log10(lo)) : 0;
    const y1 = log ? 10 ** Math.ceil(Math.log10(hi)) : hi * 1.15;
    const plotH = H - PAD.t - PAD.b;
    const y = (v: number) =>
      PAD.t + plotH - (log ? (Math.log10(Math.max(v, y0)) - Math.log10(y0)) / (Math.log10(y1) - Math.log10(y0)) : (v - y0) / (y1 - y0 || 1)) * plotH;
    const x = (ms: number) => PAD.l + ((ms / 1000 - start) / (end - start || 1)) * (width - PAD.l - PAD.r);
    return { x, y, ticks: niceTicks(log ? y0 : 0, y1, log) };
  }, [traces, start, end, width]);

  const xTicks = useMemo(() => {
    const n = Math.max(2, Math.floor(width / 110));
    return Array.from({ length: n }, (_, i) => start + ((end - start) * (i + 0.5)) / n);
  }, [start, end, width]);
  const withDate = end - start > 86400;

  return (
    <div ref={el} className="relative select-none">
      <svg width={width} height={H} className="block">
        {scale.ticks.map((v) => (
          <g key={v}>
            <line x1={PAD.l} x2={width - PAD.r} y1={scale.y(v)} y2={scale.y(v)} stroke="var(--grid)" />
            <text x={PAD.l - 6} y={scale.y(v) + 3.5} textAnchor="end" className="fill-muted text-[10px]">
              {ms(v)}
            </text>
          </g>
        ))}
        {xTicks.map((t) => (
          <text key={t} x={scale.x(t * 1000)} y={H - 6} textAnchor="middle" className="fill-muted text-[10px]">
            {new Date(t * 1000).toLocaleString([], withDate ? { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false } : { hour: "2-digit", minute: "2-digit", hour12: false })}
          </text>
        ))}
        {traces.map((t) => (
          <circle
            key={t.id}
            cx={scale.x(t.start)}
            cy={scale.y(Math.max(t.duration, 0.001))}
            r={hover?.id === t.id ? 6 : 4}
            fill={serviceColor(t.rootService)}
            fillOpacity={0.75}
            stroke={t.errors ? "var(--bad)" : "transparent"}
            strokeWidth={2}
            className="cursor-pointer"
            onMouseEnter={() => setHover(t)}
            onMouseLeave={() => setHover(null)}
            onClick={() => onOpen(t.id)}
          />
        ))}
      </svg>
      {hover && (
        <div
          className="pointer-events-none absolute z-10 rounded-lg border border-line bg-panel/95 px-3 py-2 text-xs shadow-lg backdrop-blur"
          style={{ left: Math.min(scale.x(hover.start) + 10, width - 240), top: 4 }}
        >
          <div className="font-medium">
            {hover.rootService}: {hover.rootName}
          </div>
          <div className="text-muted">
            {ms(hover.duration)} · {hover.spans} spans{hover.errors ? ` · ${hover.errors} failed` : ""}
          </div>
        </div>
      )}
    </div>
  );
}

import { useEffect, useMemo, useRef } from "react";
import uPlot from "uplot";
import { formatValue } from "../lib/format";

// Light lines need to be darker to stand out on white.
export const PALETTE = ["#38bdf8", "#a78bfa", "#34d399", "#fbbf24", "#f472b6", "#fb923c", "#60a5fa", "#4ade80", "#f87171", "#2dd4bf"];
export const LIGHT_PALETTE = ["#0284c7", "#7c3aed", "#059669", "#b45309", "#db2777", "#ea580c", "#2563eb", "#15803d", "#dc2626", "#0d9488"];

export interface ChartSeries {
  label: string;
  values: (number | null)[];
}

interface Props {
  times: number[];
  series: ChartSeries[];
  unit?: string;
  stack?: boolean;
  height?: number;
  theme: string;
  /** The window asked for, in Unix seconds, so a series with few points isn't stretched across it. */
  window?: [number, number];
}

function cssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

/** stacked turns series into running totals, so filled areas sit on each other. */
function stacked(series: ChartSeries[]): ChartSeries[] {
  const sums: number[] = [];
  return series.map((s) => ({
    label: s.label,
    values: s.values.map((v, i) => {
      sums[i] = (sums[i] ?? 0) + (v ?? 0);
      return v == null ? null : sums[i];
    }),
  }));
}

export function TimeChart({ times, series, unit, stack, height = 220, theme, window: span }: Props) {
  const palette = theme === "light" ? LIGHT_PALETTE : PALETTE;
  const el = useRef<HTMLDivElement>(null);
  const plot = useRef<uPlot | null>(null);
  const tip = useRef<HTMLDivElement>(null);
  const shown = useMemo(() => (stack ? stacked(series) : series), [series, stack]);
  // The tooltip shows real values, not stacked totals, from the latest data.
  const latest = useRef(series);
  latest.current = series;
  const windowRef = useRef(span);
  windowRef.current = span;

  useEffect(() => {
    const host = el.current;
    if (!host) return;
    const grid = cssVar("--grid");
    const muted = cssVar("--muted");
    const axis = { stroke: muted, grid: { stroke: grid, width: 1 }, ticks: { stroke: grid, width: 1 }, font: "11px Inter, system-ui" };
    const opts: uPlot.Options = {
      width: host.clientWidth,
      height,
      padding: [8, 8, 0, 0],
      cursor: { points: { size: 6 }, drag: { x: false, y: false } },
      legend: { show: false },
      scales: { x: { time: true, range: (_u, min, max) => windowRef.current ?? [min, max] }, y: { range: (_u, min, max) => [Math.min(0, min), max > 0 ? max * 1.1 : 1] } },
      axes: [
        axis,
        {
          ...axis,
          values: (_u, vals) => vals.map((v) => formatValue(v, unit)),
          // Wide enough for the longest label, e.g. "1.5 MiB/s".
          size: (_u, vals) => Math.max(40, 14 + 7 * Math.max(0, ...(vals ?? []).map((v) => String(v).length))),
        },
      ],
      series: [
        {},
        ...shown.map((s, i) => ({
          label: s.label,
          stroke: palette[i % palette.length],
          width: 1.5,
          // Fills only help a few lines; with many they turn to mud.
          fill: stack ? palette[i % palette.length] + "33" : shown.length <= 3 ? palette[i % palette.length] + "14" : undefined,
          points: { show: false },
        })),
      ],
      hooks: {
        setCursor: [
          (u) => {
            const t = tip.current;
            const idx = u.cursor.idx;
            if (!t) return;
            if (idx == null || u.cursor.left == null || u.cursor.left < 0) {
              t.style.display = "none";
              return;
            }
            const rows = u.series
              .slice(1)
              .map((s, i) => ({ label: String(s.label), v: latest.current[i]?.values[idx] ?? null, color: palette[i % palette.length] }))
              .filter((r) => r.v != null)
              .sort((a, b) => (b.v ?? 0) - (a.v ?? 0))
              .slice(0, 8);
            const when = new Date(u.data[0][idx] * 1000).toLocaleString();
            t.innerHTML =
              `<div class="mb-1 text-muted">${when}</div>` +
              rows
                .map(
                  (r) =>
                    `<div class="flex items-center gap-2"><span style="background:${r.color}" class="inline-block size-2 rounded-full"></span><span class="truncate max-w-56">${escapeHtml(r.label)}</span><span class="ml-auto pl-3 font-mono">${formatValue(r.v, unit)}</span></div>`,
                )
                .join("");
            t.style.display = rows.length ? "block" : "none";
            const left = u.cursor.left + 70;
            t.style.left = `${Math.min(left, host.clientWidth - t.offsetWidth - 4)}px`;
            t.style.top = "8px";
          },
        ],
      },
    };
    const data = [times, ...shown.map((s) => s.values)] as uPlot.AlignedData;
    plot.current = new uPlot(opts, data, host);
    const ro = new ResizeObserver(() => plot.current?.setSize({ width: host.clientWidth, height }));
    ro.observe(host);
    return () => {
      ro.disconnect();
      plot.current?.destroy();
      plot.current = null;
    };
    // Rebuild when the shape changes; plain data updates go through setData below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [series.length, series.map((s) => s.label).join("\n"), unit, stack, height, theme]);

  useEffect(() => {
    plot.current?.setData([times, ...shown.map((s) => s.values)] as uPlot.AlignedData);
  }, [times, shown]);

  return (
    <div className="relative">
      <div ref={el} />
      <div
        ref={tip}
        className="pointer-events-none absolute z-10 hidden rounded-lg border border-line bg-panel/95 px-3 py-2 text-xs shadow-lg backdrop-blur"
      />
    </div>
  );
}

function escapeHtml(s: string) {
  return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
}

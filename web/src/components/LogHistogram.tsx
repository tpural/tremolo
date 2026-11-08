import { useContext, useEffect, useRef } from "react";
import uPlot from "uplot";
import { formatValue } from "../lib/format";
import { ThemeContext } from "./theme";

function cssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

// Empty intervals draw nothing rather than a zero-height bar.
function gaps(values: number[]) {
  return values.map((v) => (v ? v : null));
}

/** LogHistogram shows lines per interval, errors on top. Dragging across it zooms to that window. */
export function LogHistogram({
  times,
  total,
  errors,
  onZoom,
}: {
  times: number[];
  total: number[];
  errors: number[];
  onZoom: (from: number, to: number) => void;
}) {
  const el = useRef<HTMLDivElement>(null);
  const plot = useRef<uPlot | null>(null);
  const zoom = useRef(onZoom);
  zoom.current = onZoom;
  const theme = useContext(ThemeContext);

  useEffect(() => {
    const host = el.current;
    if (!host) return;
    const muted = cssVar("--muted");
    const grid = cssVar("--grid");
    const bars = uPlot.paths.bars!({ size: [0.8, 24], gap: 1 });
    const axis = { stroke: muted, grid: { stroke: grid, width: 1 }, ticks: { show: false }, font: "11px Inter, system-ui" };
    plot.current = new uPlot(
      {
        width: host.clientWidth,
        height: 96,
        padding: [4, 4, 0, 0],
        legend: { show: false },
        cursor: { drag: { x: true, y: false, setScale: false }, points: { show: false } },
        select: { show: true, left: 0, top: 0, width: 0, height: 0 },
        scales: { x: { time: true }, y: { range: (_u, _min, max) => [0, Math.max(1, max)] } },
        axes: [
          {
            ...axis,
            size: 22,
            values: (_u, vals) => vals.map((v) => new Date(v * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false })),
          },
          { ...axis, size: 44, ticks: { show: false }, values: (_u, v) => v.map((n) => formatValue(n)) , splits: (_u, _i, _min, max) => [0, max] },
        ],
        series: [
          {},
          { label: "lines", fill: cssVar("--accent") + "55", stroke: "transparent", paths: bars },
          { label: "errors", fill: cssVar("--bad") + "cc", stroke: "transparent", paths: bars },
        ],
        hooks: {
          setSelect: [
            (u) => {
              if (u.select.width < 4) return;
              const from = u.posToVal(u.select.left, "x");
              const to = u.posToVal(u.select.left + u.select.width, "x");
              u.setSelect({ left: 0, top: 0, width: 0, height: 0 }, false);
              zoom.current(Math.floor(from), Math.ceil(to));
            },
          ],
        },
      },
      [times, gaps(total), gaps(errors)],
      host,
    );
    const ro = new ResizeObserver(() => plot.current?.setSize({ width: host.clientWidth, height: 96 }));
    ro.observe(host);
    return () => {
      ro.disconnect();
      plot.current?.destroy();
      plot.current = null;
    };
  }, [theme]);

  useEffect(() => {
    plot.current?.setData([times, gaps(total), gaps(errors)]);
  }, [times, total, errors]);

  return <div ref={el} className="cursor-crosshair" title="Drag to zoom in" />;
}

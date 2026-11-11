import { useQuery } from "@tanstack/react-query";
import { useContext } from "react";
import { api, type Panel as PanelSpec } from "../lib/api";
import { formatValue, legend, substitute } from "../lib/format";
import { step } from "../lib/time";
import { ThemeContext } from "./theme";
import { Narrower } from "./ui";
import { LogList } from "./LogList";
import { TimeChart } from "./TimeChart";

interface Props {
  panel: PanelSpec;
  vars: Record<string, string>;
  rangeSeconds: number;
}

const REFRESH = 30_000;

export function Panel({ panel, vars, rangeSeconds }: Props) {
  const query = substitute(panel.query, vars);
  return (
    <section
      className="flex min-w-0 flex-col rounded-xl border border-line bg-panel p-4"
      style={{ gridColumn: `span ${panel.width} / span ${panel.width}` }}
    >
      <h3 className="mb-2 truncate text-sm font-medium text-muted" title={query}>
        {panel.title}
      </h3>
      {panel.type === "timeseries" && <TimeSeriesPanel panel={panel} query={query} rangeSeconds={rangeSeconds} />}
      {panel.type === "stat" && <StatPanel panel={panel} query={query} />}
      {panel.type === "table" && <TablePanel panel={panel} query={query} />}
      {panel.type === "logs" && <LogsPanel query={query} rangeSeconds={rangeSeconds} />}
    </section>
  );
}

function Problem({ error }: { error: unknown }) {
  return (
    <p className="py-6 text-center text-xs text-bad">
      {error instanceof Error ? error.message : String(error)} <Narrower error={error} />
    </p>
  );
}

function Empty() {
  return <p className="py-10 text-center text-xs text-muted">No data</p>;
}

function TimeSeriesPanel({ panel, query, rangeSeconds }: { panel: PanelSpec; query: string; rangeSeconds: number }) {
  const theme = useContext(ThemeContext);
  const st = step(rangeSeconds);
  const q = useQuery({
    queryKey: ["range", query, rangeSeconds],
    queryFn: ({ signal }) => {
      const end = Math.floor(Date.now() / 1000 / st) * st;
      return api.range(query, end - rangeSeconds, end, st, signal);
    },
    refetchInterval: REFRESH,
    placeholderData: (prev) => prev,
  });
  if (q.error) return <Problem error={q.error} />;
  if (!q.data) return <div className="h-[220px] animate-pulse rounded-lg bg-panel-2" />;
  if (!q.data.length) return <Empty />;
  const end = Math.floor(Date.now() / 1000 / st) * st;
  // Align every series on one time axis.
  const times = [...new Set(q.data.flatMap((s) => s.values.map(([t]) => t)))].sort((a, b) => a - b);
  const index = new Map(times.map((t, i) => [t, i]));
  const series = q.data.slice(0, 30).map((s) => {
    const values: (number | null)[] = new Array(times.length).fill(null);
    for (const [t, v] of s.values) values[index.get(t)!] = Number(v);
    return { label: legend(panel.legend, s.metric), values };
  });
  return (
    <TimeChart times={times} series={series} unit={panel.unit} stack={panel.stack} theme={theme} window={[end - rangeSeconds, end]} />
  );
}

function statColor(v: number, panel: PanelSpec) {
  if (panel.bad != null && v >= panel.bad) return "text-bad";
  if (panel.warn != null && v >= panel.warn) return "text-warn";
  return "text-fg";
}

function StatPanel({ panel, query }: { panel: PanelSpec; query: string }) {
  const q = useQuery({
    queryKey: ["instant", query],
    queryFn: ({ signal }) => api.instant(query, undefined, signal),
    refetchInterval: REFRESH,
    placeholderData: (prev) => prev,
  });
  if (q.error) return <Problem error={q.error} />;
  if (!q.data) return <div className="h-12 animate-pulse rounded-lg bg-panel-2" />;
  if (!q.data.length) return <p className="text-3xl font-semibold text-muted">–</p>;
  return (
    <div className="flex flex-wrap items-end gap-x-6 gap-y-2">
      {q.data.slice(0, 6).map((s, i) => {
        const v = Number(s.value[1]);
        return (
          <div key={i}>
            <p className={`text-3xl font-semibold tabular-nums ${statColor(v, panel)}`}>{formatValue(v, panel.unit)}</p>
            {q.data.length > 1 && <p className="text-xs text-muted">{legend(panel.legend, s.metric)}</p>}
          </div>
        );
      })}
    </div>
  );
}

function TablePanel({ panel, query }: { panel: PanelSpec; query: string }) {
  const q = useQuery({
    queryKey: ["instant", query],
    queryFn: ({ signal }) => api.instant(query, undefined, signal),
    refetchInterval: REFRESH,
    placeholderData: (prev) => prev,
  });
  if (q.error) return <Problem error={q.error} />;
  if (!q.data) return <div className="h-24 animate-pulse rounded-lg bg-panel-2" />;
  if (!q.data.length) return <Empty />;
  const rows = [...q.data].sort((a, b) => Number(b.value[1]) - Number(a.value[1]));
  return (
    <div className="max-h-80 overflow-auto">
      <table className="w-full text-sm">
        <tbody>
          {rows.map((s, i) => {
            const v = Number(s.value[1]);
            return (
              <tr key={i} className="border-b border-line last:border-0">
                <td className="max-w-0 truncate py-1.5 pr-3" title={legend(panel.legend, s.metric)}>
                  {legend(panel.legend, s.metric)}
                </td>
                <td className={`py-1.5 text-right font-mono tabular-nums ${statColor(v, panel)}`}>{formatValue(v, panel.unit)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function LogsPanel({ query, rangeSeconds }: { query: string; rangeSeconds: number }) {
  const q = useQuery({
    queryKey: ["logs", query, rangeSeconds],
    queryFn: ({ signal }) => {
      const end = Math.floor(Date.now() / 1000);
      return api.logs(query, end - rangeSeconds, end, 200, signal);
    },
    refetchInterval: REFRESH,
    placeholderData: (prev) => prev,
  });
  if (q.error) return <Problem error={q.error} />;
  if (!q.data) return <div className="h-40 animate-pulse rounded-lg bg-panel-2" />;
  if (!q.data.length) return <Empty />;
  return <LogList entries={q.data} maxHeight="20rem" />;
}

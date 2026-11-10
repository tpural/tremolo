import { useQuery } from "@tanstack/react-query";
import { Database } from "lucide-react";
import { PanelGrid } from "../components/PanelGrid";
import { card, ErrorNote, PageHeader, SectionHeader, skeleton } from "../components/ui";
import { api, type Panel, type StorageReport } from "../lib/api";
import { ago, formatValue } from "../lib/format";
import { useRange } from "../lib/hooks";

const PANELS: Panel[] = [
  { title: "Data on disk", type: "timeseries", query: "max by (store) (tremolo_storage_data_bytes)", legend: "{{store}}", unit: "bytes", width: 6 },
  {
    title: "Received, by sender",
    type: "timeseries",
    query: "sum by (source, signal) (rate(tremolo_ingest_bytes_total[5m]))",
    legend: "{{source}} {{signal}}",
    unit: "bytes/s",
    width: 6,
    stack: true,
  },
  {
    title: "Rows stored per second",
    type: "timeseries",
    query: "sum by (store) (rate(tremolo_storage_rows_total[5m]))",
    legend: "{{store}}",
    width: 6,
  },
  {
    title: "Growth per day",
    type: "timeseries",
    query: "max by (store) (deriv(tremolo_storage_data_bytes[6h])) * 86400",
    legend: "{{store}}",
    unit: "bytes",
    width: 6,
  },
];

/** StoragePage shows how full tremolo's stores are, and who fills them. */
export function StoragePage() {
  const range = useRange();
  const report = useQuery({ queryKey: ["storage"], queryFn: ({ signal }) => api.storage(signal), refetchInterval: 60_000 });
  const r = report.data;
  return (
    <div className="space-y-6">
      <PageHeader title="Storage" sub="How much tremolo's stores hold, how much room is left, and who sends the most" />
      {report.error && <ErrorNote error={report.error} />}
      {r?.errors?.map((e) => (
        <ErrorNote key={e} error={e} />
      ))}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {r ? r.stores.map((s) => <StoreCard key={s.store} store={s} />) : [0, 1, 2].map((i) => <div key={i} className={`${skeleton} h-28`} />)}
      </div>
      <PanelGrid panels={PANELS} vars={{}} rangeSeconds={range.seconds} />
      {r && (
        <div className="grid gap-4 lg:grid-cols-2">
          <Top title={`Metric names with the most series (${formatValue(r.totalSeries)} in all)`} rows={r.topSeries} unit="series" />
          <Top title="Log streams with the most lines today" rows={r.topStreams} unit="lines" mono />
        </div>
      )}
    </div>
  );
}

function StoreCard({ store: s }: { store: StorageReport["stores"][number] }) {
  const capBytes = parseSize(s.maxDisk);
  const usedOfCap = capBytes ? s.dataBytes / capBytes : undefined;
  return (
    <section className={`${card} space-y-2 p-4`}>
      <h2 className="flex items-center gap-2 font-semibold capitalize">
        <Database className="size-4 text-muted" /> {s.store}
      </h2>
      {s.error ? (
        <p className="text-sm text-bad">{s.error}</p>
      ) : (
        <>
          <p className="text-2xl font-semibold tabular-nums">{formatValue(s.dataBytes, "bytes")}</p>
          <p className="text-xs text-muted">
            {[
              s.retention && `kept ${s.retention}`,
              s.maxDisk && `capped at ${s.maxDisk}${usedOfCap !== undefined ? ` (${Math.round(usedOfCap * 100)}% used)` : ""}`,
              `${formatValue(s.freeBytes, "bytes")} free on its disk`,
              `sampled ${ago(s.sampled)} ago`,
            ]
              .filter(Boolean)
              .join(" · ")}
          </p>
          {usedOfCap !== undefined && (
            <div className="h-1.5 overflow-hidden rounded-full bg-panel-2" aria-hidden>
              <div
                className={`h-full ${usedOfCap > 0.9 ? "bg-bad" : usedOfCap > 0.7 ? "bg-warn" : "bg-good"}`}
                style={{ width: `${Math.min(100, usedOfCap * 100)}%` }}
              />
            </div>
          )}
        </>
      )}
    </section>
  );
}

function Top({ title, rows, unit, mono }: { title: string; rows: { name: string; count: number }[]; unit: string; mono?: boolean }) {
  const most = Math.max(1, ...rows.map((r) => r.count));
  return (
    <section className="min-w-0 space-y-2">
      <SectionHeader title={title} />
      <ul className={`${card} divide-y divide-line/60 text-sm`}>
        {rows.map((r) => (
          <li key={r.name} className="relative px-3 py-1.5">
            <span className="absolute inset-y-0 left-0 bg-accent/10" style={{ width: `${(r.count / most) * 100}%` }} aria-hidden />
            <span className="relative flex items-center gap-3">
              <span className={`min-w-0 flex-1 truncate ${mono ? "font-mono text-xs" : ""}`} title={r.name}>
                {r.name}
              </span>
              <span className="tabular-nums text-muted">
                {formatValue(r.count)} {unit}
              </span>
            </span>
          </li>
        ))}
        {!rows.length && <li className="px-3 py-3 text-muted">Nothing yet.</li>}
      </ul>
    </section>
  );
}

/** parseSize reads a size flag such as 15GiB. */
export function parseSize(s?: string): number | undefined {
  const m = s?.match(/^([\d.]+)\s*(TiB|GiB|MiB|KiB|TB|GB|MB|KB|B)?$/);
  if (!m) return undefined;
  const mult: Record<string, number> = { TiB: 2 ** 40, GiB: 2 ** 30, MiB: 2 ** 20, KiB: 2 ** 10, TB: 1e12, GB: 1e9, MB: 1e6, KB: 1e3, B: 1 };
  return Number(m[1]) * (mult[m[2] ?? "B"] ?? 1);
}

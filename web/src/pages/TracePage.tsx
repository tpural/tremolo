import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Check, CircleAlert, Copy, ScrollText, Waypoints } from "lucide-react";
import { useMemo, useState } from "react";
import { Link, useParams } from "react-router";
import { LogList } from "../components/LogList";
import { Waterfall } from "../components/Waterfall";
import { api, traces } from "../lib/api";
import { useRange } from "../lib/hooks";
import { bounds, colorServices, ms, serviceColor, tree, type SpanRow } from "../lib/traces";

function CopyId({ id }: { id: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      title="Copy trace ID"
      onClick={() =>
        navigator.clipboard?.writeText(id).then(() => {
          setDone(true);
          setTimeout(() => setDone(false), 1200);
        })
      }
      className="inline-flex items-center gap-1 font-mono text-xs text-muted hover:text-fg"
    >
      {id} {done ? <Check className="size-3" /> : <Copy className="size-3" />}
    </button>
  );
}

const linkClass = "inline-flex items-center gap-1 rounded-md border border-line px-2 py-0.5 text-xs text-muted hover:text-fg";

export function TracePage() {
  const { id = "" } = useParams();
  const range = useRange();
  const keep = (to: string, extra = "") => {
    const q = [range.label !== "1h" ? `range=${range.label}` : "", extra].filter(Boolean).join("&");
    return q ? `${to}?${q}` : to;
  };
  const trace = useQuery({
    queryKey: ["trace", id],
    queryFn: ({ signal }) => traces.get(id, signal),
    // A trace still arriving fills in.
    refetchInterval: (q) => (q.state.data ? false : 10_000),
  });
  const t = trace.data;
  const rows = useMemo(() => (t ? tree(t) : []), [t]);
  const { start, end } = useMemo(() => (t ? bounds(t) : { start: 0, end: 0 }), [t]);
  const total = end - start;
  const root = rows[0];
  const services = useMemo(() => {
    const m = new Map<string, number>();
    for (const r of rows) m.set(r.service, (m.get(r.service) ?? 0) + 1);
    return [...m].sort((a, b) => b[1] - a[1]);
  }, [rows]);
  const errors = rows.filter((r) => r.error).length;
  colorServices(services.map(([s]) => s));

  // Logs written while handling this trace, a little either side of it.
  const from = Math.floor(start / 1e6) - 60;
  const to = Math.ceil(end / 1e6) + 60;
  const logs = useQuery({
    queryKey: ["trace-logs", id, from, to],
    queryFn: ({ signal }) => api.logs(`trace_id:="${id}"`, from, to, 200, signal),
    enabled: !!t,
  });
  const logsHref = (field: string, value: string) => `/logs?f=${encodeURIComponent(`${field}=${value}`)}&from=${from}&to=${to}`;

  const actions = (r: SpanRow) => (
    <>
      <Link to={logsHref("span_id", r.span.spanID)} className={linkClass}>
        <ScrollText className="size-3" /> Logs for this span
      </Link>
      <Link to={keep("/traces", `service=${encodeURIComponent(r.service)}&op=${encodeURIComponent(r.span.operationName)}`)} className={linkClass}>
        <Waypoints className="size-3" /> Other traces of this operation
      </Link>
    </>
  );

  if (trace.isLoading) return <p className="py-16 text-center text-sm text-muted">Loading trace…</p>;
  if (trace.error) return <p className="py-16 text-center text-sm text-bad">{String(trace.error)}</p>;
  if (!t || !root) {
    return (
      <div className="mx-auto max-w-lg space-y-3 py-16 text-center">
        <img src="/loon-quiet.png" alt="" className="mx-auto w-20 opacity-70" />
        <h1 className="text-xl font-semibold">Trace not found</h1>
        <p className="text-sm text-muted">
          <span className="font-mono">{id}</span> isn't stored, or arrived in the last half minute; new traces take about 30 seconds to show up. This page checks again
          every 10 seconds.
        </p>
        <Link to={keep("/traces")} className="text-sm text-accent">
          Back to traces
        </Link>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <Link to={keep("/traces")} className="inline-flex items-center gap-1 text-sm text-muted hover:text-fg">
          <ArrowLeft className="size-4" /> Traces
        </Link>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span className="h-6 w-1.5 rounded-full" style={{ background: serviceColor(root.service) }} />
          <h1 className="text-2xl font-semibold tracking-tight">
            <span className="text-muted">{root.service}:</span> {root.span.operationName}
          </h1>
          {errors > 0 && (
            <span className="inline-flex items-center gap-1 rounded-full bg-bad/15 px-2 py-0.5 text-xs font-medium text-bad">
              <CircleAlert className="size-3.5" /> {errors} failed span{errors === 1 ? "" : "s"}
            </span>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-x-5 gap-y-1 text-sm">
          <span>
            <span className="text-muted">Duration</span> <span className="font-mono">{ms(total / 1000)}</span>
          </span>
          <span>
            <span className="text-muted">Spans</span> <span className="font-mono">{rows.length}</span>
          </span>
          <span>
            <span className="text-muted">Started</span> <span className="font-mono">{new Date(start / 1000).toLocaleString()}</span>
          </span>
          <CopyId id={t.traceID} />
        </div>
        <div className="flex flex-wrap gap-1.5">
          {services.map(([s, n]) => (
            <span key={s} className="inline-flex items-center gap-1.5 rounded-full border border-line bg-panel px-2 py-0.5 text-xs">
              <span className="size-2 rounded-full" style={{ background: serviceColor(s) }} />
              {s}
              <span className="text-muted">{n}</span>
            </span>
          ))}
        </div>
      </div>

      <div className="rounded-xl border border-line bg-panel">
        <Waterfall trace={t} rows={rows} t0={start} total={total} actions={actions} />
      </div>

      <section className="space-y-2">
        <div className="flex items-center gap-3">
          <h2 className="font-semibold">Logs</h2>
          <span className="text-sm text-muted">{logs.data ? `${logs.data.length} line${logs.data.length === 1 ? "" : "s"} with this trace ID` : ""}</span>
          <Link to={logsHref("trace_id", id)} className={`ml-auto ${linkClass}`}>
            <ScrollText className="size-3" /> Open in Logs
          </Link>
        </div>
        <div className="rounded-xl border border-line bg-panel px-2 py-1">
          {logs.data?.length ? (
            <LogList entries={[...logs.data].reverse()} maxHeight="28rem" />
          ) : (
            <p className="py-6 text-center text-sm text-muted">
              {logs.isLoading ? "Looking for logs…" : "No logs carry this trace ID. Services that log through OpenTelemetry attach it automatically."}
            </p>
          )}
        </div>
      </section>
    </div>
  );
}

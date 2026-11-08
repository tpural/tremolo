import { useQuery } from "@tanstack/react-query";
import { CircleAlert, Code, Search, X } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { TraceScatter } from "../components/TraceScatter";
import { traces } from "../lib/api";
import { formatValue } from "../lib/format";
import { useRange } from "../lib/hooks";
import { colorServices, ms, parseMs, searchQuery, SERVICE, serviceColor, type ServiceStats, type TraceSearch, type TraceSummary } from "../lib/traces";
import { quote } from "../lib/logs";

const LIMITS = [50, 100, 250];

function useNow(frozen: boolean) {
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));
  useEffect(() => {
    if (frozen) return;
    const t = setInterval(() => setNow(Math.floor(Date.now() / 1000)), 30_000);
    return () => clearInterval(t);
  }, [frozen]);
  return [now, () => setNow(Math.floor(Date.now() / 1000))] as const;
}

function ServiceCard({ s, active, onClick }: { s: ServiceStats; active: boolean; onClick: () => void }) {
  const errRate = s.spans ? s.errors / s.spans : 0;
  return (
    <button
      type="button"
      onClick={onClick}
      className={`min-w-44 flex-1 rounded-xl border bg-panel p-3 text-left transition hover:border-accent/60 ${active ? "border-accent ring-1 ring-accent" : "border-line"}`}
    >
      <div className="flex items-center gap-2">
        <span className="size-2.5 shrink-0 rounded-full" style={{ background: serviceColor(s.service) }} />
        <span className="truncate font-medium">{s.service}</span>
      </div>
      <div className="mt-2 grid grid-cols-3 gap-2 text-xs">
        <div>
          <div className="text-muted">{s.requests ? "Requests" : "Spans"}</div>
          <div className="font-mono">{formatValue(s.requests || s.spans)}</div>
        </div>
        <div>
          <div className="text-muted">Errors</div>
          <div className={`font-mono ${errRate > 0.05 ? "text-bad" : errRate > 0 ? "text-warn" : ""}`}>{errRate ? `${(errRate * 100).toFixed(errRate < 0.1 ? 1 : 0)}%` : "0"}</div>
        </div>
        <div>
          <div className="text-muted">p95</div>
          <div className="font-mono">{s.p95 != null ? ms(s.p95) : "–"}</div>
        </div>
      </div>
    </button>
  );
}

function ServiceChips({ services }: { services: string[] }) {
  const shown = services.slice(0, 4);
  return (
    <span className="flex flex-wrap justify-end gap-1">
      {shown.map((s) => (
        <span key={s} className="inline-flex items-center gap-1 rounded-full bg-panel-2 px-1.5 py-px text-[11px] text-muted">
          <span className="size-1.5 rounded-full" style={{ background: serviceColor(s) }} />
          {s}
        </span>
      ))}
      {services.length > shown.length && <span className="text-[11px] text-muted">+{services.length - shown.length}</span>}
    </span>
  );
}

function ResultRow({ t, max, href }: { t: TraceSummary; max: number; href: string }) {
  const when = new Date(t.start);
  return (
    <Link to={href} className="grid grid-cols-[5.5rem_minmax(0,1fr)_auto] items-center gap-3 border-b border-line px-3 py-2 last:border-0 hover:bg-panel-2 md:grid-cols-[5.5rem_minmax(0,1fr)_20rem_9rem]">
      <span className="font-mono text-xs text-muted tabular-nums" title={when.toLocaleString()}>
        {when.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false })}
      </span>
      <span className="min-w-0">
        <span className="flex items-center gap-1.5">
          <span className="h-3.5 w-1 shrink-0 rounded-full" style={{ background: serviceColor(t.rootService) }} />
          <span className="shrink-0 text-xs text-muted">{t.rootService}</span>
          <span className={`truncate text-sm font-medium ${t.rootError ? "text-bad" : ""}`}>{t.rootName || "(unnamed)"}</span>
          {t.errors > 0 && (
            <span className="inline-flex shrink-0 items-center gap-0.5 rounded bg-bad/15 px-1 text-[11px] text-bad">
              <CircleAlert className="size-3" /> {t.errors}
            </span>
          )}
        </span>
        <span className="mt-0.5 flex items-center gap-2 text-[11px] text-muted">
          {t.spans} span{t.spans === 1 ? "" : "s"}
          <span className="md:hidden">· {ms(t.duration)}</span>
        </span>
      </span>
      <span className="hidden md:block">
        <ServiceChips services={t.services} />
      </span>
      <span className="hidden items-center gap-2 md:flex">
        <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-panel-2">
          <span className={`block h-full rounded-full ${t.errors ? "bg-bad" : "bg-accent"}`} style={{ width: `${Math.max(2, (t.duration / (max || 1)) * 100)}%` }} />
        </span>
        <span className="w-14 text-right font-mono text-xs tabular-nums">{ms(t.duration)}</span>
      </span>
    </Link>
  );
}

export function TracesPage() {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const range = useRange();
  const service = params.get("service") ?? "";
  const operation = params.get("op") ?? "";
  const errorsOnly = params.get("errors") === "1";
  const minText = params.get("min") ?? "";
  const filter = params.get("q") ?? "";
  const limit = Number(params.get("limit")) || LIMITS[0];
  const [now, refresh] = useNow(false);
  const start = now - range.seconds;
  const end = now;

  const [draftMin, setDraftMin] = useState(minText);
  const [draftFilter, setDraftFilter] = useState(filter);
  const [showQuery, setShowQuery] = useState(false);
  const [jump, setJump] = useState("");
  useEffect(() => setDraftMin(minText), [minText]);
  useEffect(() => setDraftFilter(filter), [filter]);

  const set = (k: string, v: string) =>
    setParams(
      (p) => {
        if (v) p.set(k, v);
        else p.delete(k);
        if (k === "service") p.delete("op");
        return p;
      },
      { replace: true },
    );

  const search: TraceSearch = { service, operation, errorsOnly, minMs: parseMs(minText), filter };
  const results = useQuery({
    queryKey: ["traces", search, start, end, limit],
    queryFn: ({ signal }) => traces.search(search, start, end, limit, signal),
    placeholderData: (prev) => prev,
  });
  const services = useQuery({
    queryKey: ["trace-services", start, end],
    queryFn: ({ signal }) => traces.services(start, end, signal),
    placeholderData: (prev) => prev,
  });
  const operations = useQuery({
    queryKey: ["trace-ops", service, start, end],
    queryFn: ({ signal }) => traces.values(service ? `span_id:* "${SERVICE}":=${quote(service)}` : "span_id:*", "name", start, end, signal),
    staleTime: 60_000,
  });

  const list = results.data ?? [];
  const max = Math.max(0, ...list.map((t) => t.duration));
  const keep = (to: string) => `${to}${range.label !== "1h" ? `?range=${range.label}` : ""}`;
  const svcList = services.data ?? [];
  colorServices(svcList.map((s) => s.service));
  const minBad = minText !== "" && parseMs(minText) == null;
  const notEnabled = results.error && String(results.error).includes("not enabled");

  if (notEnabled) {
    return (
      <div className="mx-auto max-w-lg space-y-3 py-16 text-center">
        <img src="/loon-quiet.png" alt="" className="mx-auto w-20 opacity-70" />
        <h1 className="text-xl font-semibold">Traces are off</h1>
        <p className="text-sm text-muted">
          This tremolo has no trace storage. Install it with <code className="font-mono">traces.enabled: true</code> in the chart, or start the server with{" "}
          <code className="font-mono">--traces-url</code> pointing at VictoriaTraces.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-baseline gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">Traces</h1>
        <p className="text-sm text-muted">
          {results.isFetching ? "Searching…" : `${list.length}${list.length >= limit ? "+" : ""} trace${list.length === 1 ? "" : "s"}`}
        </p>
        <form
          className="ml-auto flex items-center gap-2 rounded-lg border border-line bg-panel px-2 focus-within:border-accent"
          onSubmit={(e) => {
            e.preventDefault();
            const id = jump.trim().toLowerCase();
            if (/^[0-9a-f]{1,32}$/.test(id)) navigate(keep(`/traces/${id}`));
          }}
        >
          <Search className="size-3.5 text-muted" />
          <input value={jump} onChange={(e) => setJump(e.target.value)} placeholder="Open trace ID" className="w-44 bg-transparent py-1 font-mono text-xs outline-none" />
        </form>
      </div>

      {svcList.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {svcList.map((s) => (
            <ServiceCard key={s.service} s={s} active={service === s.service} onClick={() => set("service", service === s.service ? "" : s.service)} />
          ))}
        </div>
      )}

      <form
        className="flex flex-wrap items-center gap-2 text-sm"
        onSubmit={(e) => {
          e.preventDefault();
          setParams(
            (p) => {
              for (const [k, v] of [
                ["min", draftMin.trim()],
                ["q", draftFilter.trim()],
              ]) {
                if (v) p.set(k, v);
                else p.delete(k);
              }
              return p;
            },
            { replace: true },
          );
          refresh();
        }}
      >
        <select value={service} onChange={(e) => set("service", e.target.value)} className="rounded-lg border border-line bg-panel px-2 py-1.5">
          <option value="">All services</option>
          {svcList.map((s) => (
            <option key={s.service} value={s.service}>
              {s.service}
            </option>
          ))}
          {service && !svcList.some((s) => s.service === service) && <option value={service}>{service}</option>}
        </select>
        <select value={operation} onChange={(e) => set("op", e.target.value)} className="max-w-64 rounded-lg border border-line bg-panel px-2 py-1.5">
          <option value="">All operations</option>
          {(operations.data ?? [])
            .sort((a, b) => b.hits - a.hits)
            .map((o) => (
              <option key={o.value} value={o.value}>
                {o.value}
              </option>
            ))}
          {operation && !(operations.data ?? []).some((o) => o.value === operation) && <option value={operation}>{operation}</option>}
        </select>
        <label className={`flex items-center gap-1.5 rounded-lg border bg-panel px-2 ${minBad ? "border-bad" : "border-line focus-within:border-accent"}`} title="Only traces at least this long, e.g. 250ms or 2s">
          <span className="text-muted">≥</span>
          <input value={draftMin} onChange={(e) => setDraftMin(e.target.value)} placeholder="duration" className="w-20 bg-transparent py-1.5 font-mono text-xs outline-none" />
        </label>
        <button
          type="button"
          onClick={() => set("errors", errorsOnly ? "" : "1")}
          className={`flex items-center gap-1 rounded-lg border px-2.5 py-1.5 ${errorsOnly ? "border-bad bg-bad text-white" : "border-line bg-panel text-muted hover:text-fg"}`}
        >
          <CircleAlert className="size-3.5" /> Failed only
        </button>
        <div className="flex min-w-64 flex-1 items-center gap-2 rounded-lg border border-line bg-panel px-2 focus-within:border-accent">
          <Search className="size-3.5 text-muted" />
          <input
            value={draftFilter}
            onChange={(e) => setDraftFilter(e.target.value)}
            placeholder='Any span matching LogsQL, e.g. span_attr:http.response.status_code:>=500'
            className="w-full bg-transparent py-1.5 font-mono text-xs outline-none"
          />
          {draftFilter && (
            <button
              type="button"
              onClick={() => {
                setDraftFilter("");
                set("q", "");
              }}
              className="text-muted hover:text-fg"
            >
              <X className="size-3.5" />
            </button>
          )}
        </div>
        <select value={limit} onChange={(e) => set("limit", e.target.value === String(LIMITS[0]) ? "" : e.target.value)} className="rounded-lg border border-line bg-panel px-2 py-1.5" title="How many traces">
          {LIMITS.map((l) => (
            <option key={l} value={l}>
              {l} newest
            </option>
          ))}
        </select>
        <button type="submit" className="rounded-lg bg-accent px-3 py-1.5 font-medium text-white dark:text-bg">
          Search
        </button>
        <button
          type="button"
          onClick={() => setShowQuery(!showQuery)}
          className={`rounded-lg border p-1.5 ${showQuery ? "border-accent text-accent" : "border-line bg-panel text-muted hover:text-fg"}`}
          title="Show the LogsQL behind this search"
        >
          <Code className="size-4" />
        </button>
      </form>
      {showQuery && <p className="rounded-lg border border-line bg-panel-2 p-2 font-mono text-xs break-all">{searchQuery(search, limit)}</p>}

      {list.length > 0 && (
        <div className="rounded-xl border border-line bg-panel px-2 pt-2">
          <TraceScatter traces={list} start={start} end={end} onOpen={(id) => navigate(keep(`/traces/${id}`))} />
        </div>
      )}

      <div className="rounded-xl border border-line bg-panel">
        {results.error ? (
          <p className="py-10 text-center text-sm text-bad">{String(results.error)}</p>
        ) : list.length ? (
          list.map((t) => <ResultRow key={t.id} t={t} max={max} href={keep(`/traces/${t.id}`)} />)
        ) : (
          <div className="flex flex-col items-center gap-2 py-12 text-center text-sm text-muted">
            <img src="/loon-quiet.png" alt="" className="w-16 opacity-60" />
            {results.isFetching
              ? "Searching…"
              : service || operation || errorsOnly || minText || filter
                ? "No traces match. Try a wider time range or fewer filters."
                : "No traces yet. Point an OpenTelemetry SDK at tremolo's ingest port (4318) to send some."}
          </div>
        )}
      </div>
    </div>
  );
}

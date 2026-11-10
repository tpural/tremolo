import { useQuery } from "@tanstack/react-query";
import { message } from "../components/ui";
import { ChevronDown, Code, Copy, Pause, Play, Search, SlidersHorizontal, WrapText, X, ZoomOut } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { facetLabel, FacetPanel } from "../components/FacetPanel";
import { LogHistogram } from "../components/LogHistogram";
import { LogList } from "../components/LogList";
import { api, tailLogs, type LogEntry } from "../lib/api";
import { formatValue } from "../lib/format";
import { useRange } from "../lib/hooks";
import { buildQuery, decodeFilter, encodeFilter, facetCandidates, PREFERRED_FACETS, type FieldFilter } from "../lib/logs";

const PAGE = 300;
const MAX_TAIL = 2000;
const LEVELS = [
  { id: "", label: "All levels" },
  { id: "warn", label: "Warnings +" },
  { id: "error", label: "Errors" },
] as const;

function readWrap() {
  try {
    return localStorage.getItem("tremolo-logs-wrap") === "1";
  } catch {
    return false;
  }
}

/** Free-text words to highlight: plain terms, without LogsQL syntax. */
function terms(text: string): string[] {
  return text
    .split(/\s+/)
    .filter((t) => t && !t.includes(":") && !/^(AND|OR|NOT|\*)$/i.test(t) && !t.startsWith("-") && !t.startsWith("("))
    .map((t) => t.replace(/^"|"$/g, "").replace(/\*$/, ""))
    .filter((t) => t.length > 1);
}

function dedupe(list: LogEntry[]) {
  const seen = new Set<string>();
  return list.filter((e) => {
    const k = `${e._time}|${e._stream_id}|${e._msg}`;
    if (seen.has(k)) return false;
    seen.add(k);
    return true;
  });
}

export function LogsPage() {
  const [params, setParams] = useSearchParams();
  const range = useRange();
  const text = params.get("q") ?? "";
  const minLevel = (params.get("level") ?? "") as "" | "warn" | "error";
  const filters = useMemo(
    () =>
      params
        .getAll("f")
        .map(decodeFilter)
        .filter((f): f is FieldFilter => f != null),
    [params],
  );
  const custom = useMemo(() => params.getAll("facet"), [params]);
  const zoomFrom = Number(params.get("from")) || 0;
  const zoomTo = Number(params.get("to")) || 0;

  const [draft, setDraft] = useState(text);
  const [live, setLive] = useState(false);
  const [tail, setTail] = useState<LogEntry[]>([]);
  const [older, setOlder] = useState<LogEntry[]>([]);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [wrap, setWrap] = useState(readWrap);
  const [showFacets, setShowFacets] = useState(false);
  const [showQuery, setShowQuery] = useState(false);
  const [context, setContext] = useState<LogEntry | null>(null);
  const search = useRef<HTMLInputElement>(null);

  useEffect(() => setDraft(text), [text]);

  // The window being looked at: a zoomed range, or the last N of the page's range, refreshed each minute.
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));
  useEffect(() => {
    if (zoomFrom) return;
    const t = setInterval(() => setNow(Math.floor(Date.now() / 1000)), 60_000);
    return () => clearInterval(t);
  }, [zoomFrom]);
  const end = zoomTo || now;
  const start = zoomFrom || now - range.seconds;

  const query = buildQuery(text, filters, minLevel);
  // Each facet counts values with the other fields' filters applied, so siblings stay visible.
  const queryFor = useCallback(
    (field: string) =>
      buildQuery(
        text,
        filters.filter((f) => f.field !== field || f.negate),
        minLevel,
      ),
    [text, filters, minLevel],
  );

  const update = useCallback(
    (fn: (p: URLSearchParams) => void) =>
      setParams(
        (p) => {
          fn(p);
          return p;
        },
        { replace: true },
      ),
    [setParams],
  );

  const addFilter = useCallback(
    (f: FieldFilter) =>
      update((p) => {
        const rest = p
          .getAll("f")
          .map(decodeFilter)
          .filter((x): x is FieldFilter => x != null && !(x.field === f.field && x.value === f.value));
        // Clicking the same include filter again removes it.
        const same = filters.find((x) => x.field === f.field && x.value === f.value && !!x.negate === !!f.negate);
        p.delete("f");
        for (const x of same ? rest : [...rest, f]) p.append("f", encodeFilter(x));
      }),
    [update, filters],
  );
  const removeFilter = (f: FieldFilter) =>
    update((p) => {
      const rest = p.getAll("f").filter((s) => s !== encodeFilter(f));
      p.delete("f");
      for (const s of rest) p.append("f", s);
    });

  const results = useQuery({
    queryKey: ["logs", query, start, end],
    queryFn: ({ signal }) => api.logs(query, start, end, PAGE, signal),
    enabled: !live,
    placeholderData: (prev) => prev,
  });
  useEffect(() => setOlder([]), [query, start, end]);

  const step = Math.max(1, Math.round((end - start) / 120));
  const hits = useQuery({
    queryKey: ["hits", query, start, end, step],
    queryFn: async ({ signal }) => {
      const errQuery = buildQuery(text, filters, "error");
      const [all, errs] = await Promise.all([
        api.hits(query, start, end, step, signal),
        minLevel === "error" ? Promise.resolve(null) : api.hits(errQuery, start, end, step, signal),
      ]);
      // Put both on one time axis, filling gaps with zero.
      const times: number[] = [];
      for (let t = Math.floor(start / step) * step; t <= end; t += step) times.push(t);
      const index = new Map(times.map((t, i) => [t, i]));
      const total = times.map(() => 0);
      const errors = times.map(() => 0);
      const at = (t: number) => index.get(Math.floor(t / step) * step);
      for (const h of all) {
        const i = at(h.t);
        if (i != null) total[i] += h.n;
      }
      for (const h of errs ?? all) {
        const i = at(h.t);
        if (i != null) errors[i] += h.n;
      }
      return { times, total, errors, count: total.reduce((a, b) => a + b, 0), errorCount: errors.reduce((a, b) => a + b, 0) };
    },
    enabled: !live,
    placeholderData: (prev) => prev,
  });

  const fieldNames = useQuery({
    queryKey: ["fieldNames", start, end],
    queryFn: ({ signal }) => api.fieldNames("*", start, end, signal),
    staleTime: 5 * 60_000,
  });
  const available = facetCandidates(fieldNames.data ?? []);
  const facets = [...PREFERRED_FACETS.filter((f) => available.includes(f)), ...custom.filter((f) => !PREFERRED_FACETS.includes(f))];
  for (const f of filters) if (!facets.includes(f.field)) facets.push(f.field);

  useEffect(() => {
    if (!live) return;
    const ctl = new AbortController();
    setTail([]);
    tailLogs(query, (e) => setTail((t) => [e, ...t].slice(0, MAX_TAIL)), ctl.signal).catch(() => {});
    return () => ctl.abort();
  }, [live, query]);

  // "/" jumps to the search box, Escape closes the context view.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLInputElement || e.target instanceof HTMLSelectElement || e.target instanceof HTMLTextAreaElement;
      if (e.key === "/" && !typing) {
        e.preventDefault();
        search.current?.focus();
      }
      if (e.key === "Escape") setContext(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const loadOlder = async () => {
    const list = [...(results.data ?? []), ...older];
    const oldest = list[list.length - 1];
    if (!oldest) return;
    setLoadingOlder(true);
    try {
      const more = await api.logsBetween(query, new Date(start * 1000).toISOString(), oldest._time, PAGE);
      setOlder((o) => dedupe([...o, ...more.sort((a, b) => (a._time < b._time ? 1 : -1))]));
    } finally {
      setLoadingOlder(false);
    }
  };

  const entries = live ? tail : dedupe([...(results.data ?? []), ...older]);
  const total = hits.data?.count;
  const canLoadOlder = !live && entries.length >= PAGE && (total == null || entries.length < total);
  const zoomed = zoomFrom > 0;
  const fmt = (t: number) =>
    new Date(t * 1000).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit" });

  const facetPanel = (
    <FacetPanel
      fields={facets}
      available={available}
      queryFor={queryFor}
      start={start}
      end={end}
      filters={filters}
      onFilter={addFilter}
      custom={custom}
      onAddField={(f) => update((p) => p.append("facet", f))}
      onRemoveField={(f) =>
        update((p) => {
          const rest = p.getAll("facet").filter((x) => x !== f);
          p.delete("facet");
          for (const x of rest) p.append("facet", x);
        })
      }
    />
  );

  return (
    <div className="space-y-4">
      <div className="flex items-baseline gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">Logs</h1>
        <p className="text-sm text-muted">
          {live
            ? `Live · ${tail.length} new line${tail.length === 1 ? "" : "s"}`
            : total != null
              ? `${formatValue(total)} lines${hits.data?.errorCount ? ` · ${formatValue(hits.data.errorCount)} errors` : ""}`
              : ""}
        </p>
      </div>

      <form
        className="flex flex-wrap gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          update((p) => (draft ? p.set("q", draft) : p.delete("q")));
        }}
      >
        <div className="flex min-w-64 flex-1 items-center gap-2 rounded-lg border border-line-strong/70 bg-panel px-3 focus-within:border-accent">
          <Search className="size-4 text-muted" />
          <input
            ref={search}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="Search logs   ( / to focus )"
            title='Words, "exact phrase", field:value, error OR timeout'
            className="w-full bg-transparent py-2 font-mono text-sm outline-none"
          />
          {draft && (
            <button
              type="button"
              onClick={() => {
                setDraft("");
                update((p) => p.delete("q"));
              }}
              className="text-muted hover:text-fg"
              title="Clear"
            >
              <X className="size-4" />
            </button>
          )}
        </div>
        <div className="flex rounded-lg border border-line bg-panel p-0.5 text-sm">
          {LEVELS.map((l) => (
            <button
              key={l.id}
              type="button"
              onClick={() => update((p) => (l.id ? p.set("level", l.id) : p.delete("level")))}
              aria-pressed={minLevel === l.id}
              className={`rounded-md px-2.5 py-1 ${
                minLevel === l.id
                  ? l.id === "error"
                    ? "bg-bad/12 font-medium text-bad"
                    : l.id === "warn"
                      ? "bg-warn/12 font-medium text-warn-ink"
                      : "bg-panel-2 font-medium"
                  : "text-muted hover:text-fg"
              }`}
            >
              {l.label}
            </button>
          ))}
        </div>
        <button
          type="button"
          onClick={() => setLive(!live)}
          className={`flex items-center gap-1.5 rounded-lg border px-3 text-sm ${live ? "border-good text-good" : "border-line bg-panel text-muted hover:text-fg"}`}
          title={live ? "Stop following" : "Follow new lines as they arrive"}
        >
          {live ? (
            <>
              <span className="relative flex size-2">
                <span className="absolute inline-flex size-full animate-ping rounded-full bg-good opacity-75" />
                <span className="relative inline-flex size-2 rounded-full bg-good" />
              </span>
              <Pause className="size-4" /> Live
            </>
          ) : (
            <>
              <Play className="size-4" /> Tail
            </>
          )}
        </button>
      </form>

      {(filters.length > 0 || zoomed) && (
        <div className="flex flex-wrap items-center gap-2">
          {filters.map((f) => (
            <span
              key={encodeFilter(f)}
              className={`inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs ${
                f.negate ? "border-bad/40 bg-bad/10 text-bad" : "border-accent/40 bg-accent/10 text-accent"
              }`}
            >
              <span className="text-muted">{facetLabel(f.field)}</span>
              <span className="text-muted">{f.negate ? "not" : "is"}</span>
              <span className="max-w-64 truncate font-medium">{f.value}</span>
              <button type="button" onClick={() => removeFilter(f)} className="ml-0.5 hover:text-fg" title="Remove filter">
                <X className="size-3" />
              </button>
            </span>
          ))}
          {zoomed && (
            <span className="inline-flex items-center gap-1 rounded-full border border-line px-2.5 py-0.5 text-xs text-muted">
              {fmt(zoomFrom)} – {fmt(zoomTo)}
              <button
                type="button"
                onClick={() => update((p) => (p.delete("from"), p.delete("to")))}
                className="ml-0.5 hover:text-fg"
                title="Back to the full range"
              >
                <ZoomOut className="size-3" />
              </button>
            </span>
          )}
          {filters.length > 1 && (
            <button type="button" onClick={() => update((p) => p.delete("f"))} className="text-xs text-muted hover:text-fg">
              Clear all
            </button>
          )}
        </div>
      )}

      {!live && hits.data && (
        <div className="rounded-xl border border-line-strong/70 bg-panel px-3 pt-2">
          <LogHistogram
            times={hits.data.times}
            total={hits.data.total}
            errors={hits.data.errors}
            onZoom={(from, to) =>
              update((p) => {
                p.set("from", String(from));
                p.set("to", String(to));
              })
            }
          />
        </div>
      )}

      <div className="flex gap-4">
        <aside className="hidden w-60 shrink-0 lg:block">
          <div className="sticky top-[calc(var(--header-h,3rem)+1rem)] max-h-[calc(100dvh-var(--header-h,3rem)-2rem)] overflow-y-auto pr-1">
            {facetPanel}
          </div>
        </aside>
        <div className="min-w-0 flex-1 space-y-2">
          <div className="flex items-center gap-2 text-xs text-muted">
            <button
              type="button"
              onClick={() => setShowFacets(!showFacets)}
              className="inline-flex items-center gap-1 rounded-md border border-line px-2 py-1 hover:text-fg lg:hidden"
            >
              <SlidersHorizontal className="size-3.5" /> Filters
            </button>
            <button
              type="button"
              onClick={() => setShowQuery(!showQuery)}
              className={`inline-flex items-center gap-1 rounded-md border px-2 py-1 ${showQuery ? "border-accent text-accent" : "border-line hover:text-fg"}`}
              title="Show the LogsQL query behind this view"
            >
              <Code className="size-3.5" /> LogsQL
            </button>
            <button
              type="button"
              onClick={() => {
                setWrap(!wrap);
                try {
                  localStorage.setItem("tremolo-logs-wrap", wrap ? "0" : "1");
                } catch {
                  // remembered for this visit only
                }
              }}
              className={`ml-auto inline-flex items-center gap-1 rounded-md border px-2 py-1 ${wrap ? "border-accent text-accent" : "border-line hover:text-fg"}`}
              title="Wrap long lines"
            >
              <WrapText className="size-3.5" /> Wrap
            </button>
          </div>
          {showQuery && (
            <div className="flex items-start gap-2 rounded-lg border border-line bg-panel-2 p-2 font-mono text-xs break-all">
              <span className="flex-1">{query}</span>
              <button type="button" onClick={() => navigator.clipboard?.writeText(query)} className="shrink-0 text-muted hover:text-fg" title="Copy">
                <Copy className="size-3.5" />
              </button>
            </div>
          )}
          {showFacets && <div className="rounded-xl border border-line bg-panel p-3 lg:hidden">{facetPanel}</div>}
          <div className="rounded-xl border border-line-strong/70 bg-panel px-2 py-1">
            {results.error && !live ? (
              <p className="py-10 text-center text-sm text-bad">{message(results.error)}</p>
            ) : entries.length ? (
              <LogList
                entries={entries}
                wrap={wrap}
                terms={terms(text)}
                actions={{ onFilter: (field, value, negate) => addFilter({ field, value, negate }), onContext: setContext }}
              />
            ) : (
              <Empty live={live} loading={results.isFetching} hasFilters={filters.length > 0 || !!text || !!minLevel} />
            )}
          </div>
          {canLoadOlder && (
            <button
              type="button"
              onClick={loadOlder}
              disabled={loadingOlder}
              className="mx-auto flex items-center gap-1 rounded-lg border border-line-strong/70 bg-panel px-3 py-1.5 text-sm text-muted hover:text-fg disabled:opacity-50"
            >
              <ChevronDown className="size-4" />{" "}
              {loadingOlder ? "Loading…" : `Load older (showing ${entries.length}${total ? ` of ${formatValue(total)}` : ""})`}
            </button>
          )}
        </div>
      </div>

      {context && <ContextView entry={context} onClose={() => setContext(null)} />}
    </div>
  );
}

function Empty({ live, loading, hasFilters }: { live: boolean; loading: boolean; hasFilters: boolean }) {
  return (
    <div className="flex flex-col items-center gap-2 py-12 text-center text-sm text-muted">
      <img src="/loon-quiet.png" alt="" className="w-16 opacity-60" />
      {live
        ? "Waiting for new lines…"
        : loading
          ? "Searching…"
          : hasFilters
            ? "No lines match. Try a wider time range or fewer filters."
            : "No logs in this time range."}
    </div>
  );
}

/** ContextView shows the lines around one entry from the same stream, oldest first. */
function ContextView({ entry, onClose }: { entry: LogEntry; onClose: () => void }) {
  const t = Date.parse(entry._time);
  const around = useQuery({
    queryKey: ["context", entry._stream_id, entry._time],
    queryFn: ({ signal }) =>
      api.logsBetween(`_stream_id:${entry._stream_id}`, new Date(t - 5 * 60_000).toISOString(), new Date(t + 5 * 60_000).toISOString(), 400, signal),
  });
  const list = (around.data ?? []).sort((a, b) => (a._time < b._time ? -1 : 1));
  const at = list.findIndex((e) => e._time === entry._time && e._msg === entry._msg);
  const shown = at < 0 ? list : list.slice(Math.max(0, at - 60), at + 61);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    box.current?.querySelector(".ring-accent")?.scrollIntoView({ block: "center" });
  }, [shown.length]);
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm" onClick={onClose}>
      <div
        className="flex max-h-[85dvh] w-full max-w-6xl flex-col rounded-2xl border border-line bg-panel shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-3 border-b border-line px-4 py-3">
          <h2 className="font-semibold">Context</h2>
          <p className="truncate text-sm text-muted">Lines from the same stream, 5 minutes either side</p>
          <button type="button" onClick={onClose} className="ml-auto text-muted hover:text-fg" title="Close (Esc)">
            <X className="size-5" />
          </button>
        </div>
        <div ref={box} className="overflow-auto px-2 py-1">
          {around.isLoading ? <p className="py-10 text-center text-sm text-muted">Loading…</p> : <LogList entries={shown} wrap focus={entry} />}
        </div>
      </div>
    </div>
  );
}

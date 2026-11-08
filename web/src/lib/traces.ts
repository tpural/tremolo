import { quote } from "./logs";

/** A span as VictoriaTraces' Jaeger API returns it. Times are microseconds. */
export interface JaegerSpan {
  traceID: string;
  spanID: string;
  operationName: string;
  references: { refType: string; spanID: string; traceID: string }[];
  startTime: number;
  duration: number;
  tags: KeyValue[];
  logs: { timestamp: number; fields: KeyValue[] }[];
  processID: string;
}

export interface KeyValue {
  key: string;
  type?: string;
  value: string | number | boolean;
}

export interface JaegerTrace {
  traceID: string;
  spans: JaegerSpan[];
  processes: Record<string, { serviceName: string; tags: KeyValue[] }>;
}

/** What a search finds: one line per trace. Times in milliseconds. */
export interface TraceSummary {
  id: string;
  start: number;
  duration: number;
  spans: number;
  errors: number;
  services: string[];
  rootName: string;
  rootService: string;
  rootError: boolean;
}

export interface TraceSearch {
  service?: string;
  operation?: string;
  errorsOnly?: boolean;
  /** Minimum whole-trace duration, in milliseconds. */
  minMs?: number;
  /** Extra LogsQL over spans, e.g. span_attr:http.route:="/checkout". */
  filter?: string;
}

export const SERVICE = "resource_attr:service.name";
const ERROR = "status_code:=2";
// Spans that start work: a trace's root, or a span serving a request.
const ENTRY = `(parent_span_id:="" OR kind:=2)`;

function field(f: string) {
  return /^[\w.]+$/.test(f) ? f : quote(f);
}

/** spanFilter is the LogsQL a span must match for its trace to be found. */
export function spanFilter(s: TraceSearch): string {
  const parts: string[] = [];
  if (s.service) parts.push(`${field(SERVICE)}:=${quote(s.service)}`);
  if (s.operation) parts.push(`name:=${quote(s.operation)}`);
  if (s.filter?.trim()) parts.push(`(${s.filter.trim()})`);
  return parts.join(" ");
}

/**
 * searchQuery finds traces with at least one matching span and sums each up: its
 * first span (the root, usually), services, span and error counts, and duration.
 * Only "span_id:*" rows are spans; VictoriaTraces keeps index rows alongside them.
 */
export function searchQuery(s: TraceSearch, limit: number): string {
  const match = spanFilter(s);
  const stages = [
    `span_id:* | stats by (trace_id)`,
    [
      match ? `count() if (${match}) matched` : "",
      "min(start_time_unix_nano) start",
      "max(end_time_unix_nano) end",
      "count() spans",
      `count() if (${ERROR}) errors`,
      `uniq_values(${field(SERVICE)}) services`,
      `row_min(start_time_unix_nano, name, ${field(SERVICE)}, status_code) root`,
    ]
      .filter(Boolean)
      .join(", "),
  ];
  const after = [];
  if (match) after.push("filter matched:>0");
  after.push("math (end - start) as dur");
  const keep = [];
  if (s.minMs) keep.push(`dur:>${Math.round(s.minMs * 1e6)}`);
  if (s.errorsOnly) keep.push("errors:>0");
  if (keep.length) after.push(`filter ${keep.join(" ")}`);
  after.push("sort by (start desc)", `limit ${limit}`);
  return [stages.join(" "), ...after].join(" | ");
}

export function parseSummary(row: Record<string, string>): TraceSummary {
  let services: string[] = [];
  let root: Record<string, string> = {};
  try {
    services = JSON.parse(row.services ?? "[]");
  } catch {
    // leave empty
  }
  try {
    root = JSON.parse(row.root ?? "{}");
  } catch {
    // leave empty
  }
  const start = Number(row.start) / 1e6;
  return {
    id: row.trace_id,
    start,
    duration: (Number(row.end) - Number(row.start)) / 1e6,
    spans: Number(row.spans) || 0,
    errors: Number(row.errors) || 0,
    services: services.filter(Boolean).sort(),
    rootName: root.name ?? "",
    rootService: root[SERVICE] ?? "",
    rootError: root.status_code === "2",
  };
}

/** servicesQuery sums up each service: spans, errors, and latency of the work it serves. */
export const servicesQuery = `span_id:* | stats by (${field(SERVICE)}) count() spans, count() if (${ERROR}) errors, count() if ${ENTRY} requests, quantile(0.5, duration) if ${ENTRY} p50, quantile(0.95, duration) if ${ENTRY} p95 | sort by (spans desc)`;

export interface ServiceStats {
  service: string;
  spans: number;
  errors: number;
  requests: number;
  /** Milliseconds. */
  p50?: number;
  p95?: number;
}

export function parseServiceStats(row: Record<string, string>): ServiceStats {
  const ms = (v?: string) => (v ? Number(v) / 1e6 : undefined);
  return {
    service: row[SERVICE] ?? "",
    spans: Number(row.spans) || 0,
    errors: Number(row.errors) || 0,
    requests: Number(row.requests) || 0,
    p50: ms(row.p50),
    p95: ms(row.p95),
  };
}

/** One row of a waterfall. */
export interface SpanRow {
  span: JaegerSpan;
  service: string;
  depth: number;
  children: number;
  error: boolean;
  /** Ids of this span's ancestors, nearest last; a row is hidden when any is collapsed. */
  ancestors: string[];
}

export function tag(span: JaegerSpan, key: string): string | undefined {
  const t = span.tags.find((t) => t.key === key);
  return t == null ? undefined : String(t.value);
}

export function isError(span: JaegerSpan): boolean {
  return tag(span, "error") === "true" || tag(span, "otel.status_code") === "ERROR";
}

/** tree orders a trace's spans depth-first, children by start time. Orphans become roots. */
export function tree(trace: JaegerTrace): SpanRow[] {
  const ids = new Set(trace.spans.map((s) => s.spanID));
  const kids = new Map<string, JaegerSpan[]>();
  const roots: JaegerSpan[] = [];
  for (const s of trace.spans) {
    const parent = s.references.find((r) => r.refType === "CHILD_OF" && ids.has(r.spanID))?.spanID;
    if (parent && parent !== s.spanID) kids.set(parent, [...(kids.get(parent) ?? []), s]);
    else roots.push(s);
  }
  const byStart = (a: JaegerSpan, b: JaegerSpan) => a.startTime - b.startTime;
  const out: SpanRow[] = [];
  const seen = new Set<string>();
  const walk = (s: JaegerSpan, ancestors: string[]) => {
    if (seen.has(s.spanID)) return;
    seen.add(s.spanID);
    const children = (kids.get(s.spanID) ?? []).sort(byStart);
    out.push({
      span: s,
      service: trace.processes[s.processID]?.serviceName ?? "",
      depth: ancestors.length,
      children: children.length,
      error: isError(s),
      ancestors,
    });
    for (const c of children) walk(c, [...ancestors, s.spanID]);
  };
  for (const r of roots.sort(byStart)) walk(r, []);
  return out;
}

/** bounds is a trace's start and end, in microseconds. */
export function bounds(trace: JaegerTrace): { start: number; end: number } {
  let start = Infinity;
  let end = -Infinity;
  for (const s of trace.spans) {
    start = Math.min(start, s.startTime);
    end = Math.max(end, s.startTime + s.duration);
  }
  return Number.isFinite(start) ? { start, end } : { start: 0, end: 0 };
}

/** ms formats milliseconds compactly: 840µs, 12.3ms, 1.42s, 2m 5s. */
export function ms(v: number): string {
  if (v < 1) return `${Math.round(v * 1000)}µs`;
  if (v < 10) return `${v.toFixed(2)}ms`;
  if (v < 1000) return `${v.toFixed(v < 100 ? 1 : 0)}ms`;
  const s = v / 1000;
  if (s < 60) return `${s.toFixed(s < 10 ? 2 : 1)}s`;
  return `${Math.floor(s / 60)}m ${Math.round(s % 60)}s`;
}

// Distinct, readable on both themes. Each service takes its hashed color unless another
// service already has it, then the next free one; services keep their color for the session.
// No reds: red means failure.
const SERVICE_COLORS = ["#0ea5e9", "#8b5cf6", "#10b981", "#f59e0b", "#d946ef", "#f97316", "#84cc16", "#14b8a6", "#64748b", "#a16207"];
const assigned = new Map<string, string>();

function hash(s: string): number {
  let h = 2166136261;
  for (const c of s) h = Math.imul(h ^ c.charCodeAt(0), 16777619) >>> 0;
  return h;
}

export function serviceColor(name: string): string {
  const known = assigned.get(name);
  if (known) return known;
  const taken = new Set(assigned.values());
  const first = hash(name) % SERVICE_COLORS.length;
  let color = SERVICE_COLORS[first];
  for (let i = 0; i < SERVICE_COLORS.length; i++) {
    const c = SERVICE_COLORS[(first + i) % SERVICE_COLORS.length];
    if (!taken.has(c)) {
      color = c;
      break;
    }
  }
  assigned.set(name, color);
  return color;
}

/** colorServices gives services their colors in a stable order, so a page's first render doesn't decide them. */
export function colorServices(names: string[]) {
  for (const n of [...names].sort()) serviceColor(n);
}

export const KIND: Record<string, string> = {
  server: "Server",
  client: "Client",
  producer: "Producer",
  consumer: "Consumer",
  internal: "Internal",
};

/** parseMs reads "250ms", "1.5s", "2m", "800us" or a bare number of milliseconds. */
export function parseMs(s: string): number | undefined {
  const m = /^\s*(\d+(?:\.\d+)?)\s*(us|µs|ms|s|m|h)?\s*$/.exec(s);
  if (!m) return undefined;
  const n = Number(m[1]);
  const unit = { us: 0.001, µs: 0.001, ms: 1, s: 1000, m: 60_000, h: 3_600_000 }[m[2] ?? "ms"] ?? 1;
  return n * unit;
}

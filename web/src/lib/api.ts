export type HostState = "ready" | "notready" | "asleep" | "off" | "booting" | "unreachable";

export interface Host {
  name: string;
  state: HostState;
  reason?: string;
  since?: string;
  group?: string;
  roles?: string[];
  arch?: string;
  cpu?: number;
  memoryBytes?: number;
  pods: number;
  kubelet?: string;
  address?: string;
  lastSeen?: string;
  /** How the host is collected: a Kubernetes node, or a server pushing its own metrics. */
  kind: "kubernetes" | "server";
  cluster?: string;
  /** The ingest token name its metrics arrive with. */
  source?: string;
}

export interface Pod {
  namespace: string;
  name: string;
  owner?: string;
  phase: string;
  reason?: string;
  ready: number;
  containers: number;
  restarts: number;
  started?: string;
  cpuRequestMillis?: number;
  memoryRequestBytes?: number;
}

export interface Panel {
  title: string;
  type: "timeseries" | "stat" | "table" | "logs";
  query: string;
  legend?: string;
  unit?: string;
  width: number;
  stack?: boolean;
  warn?: number;
  bad?: number;
}

export interface Dashboard {
  id: string;
  title: string;
  description?: string;
  group?: string;
  variables?: string[];
  panels: Panel[];
}

export type DashboardSummary = Omit<Dashboard, "panels" | "variables">;

export interface Series {
  metric: Record<string, string>;
  values: [number, string][];
}

export interface Sample {
  metric: Record<string, string>;
  value: [number, string];
}

export type LogEntry = Record<string, string>;

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

async function get<T>(path: string, params?: Record<string, string>, signal?: AbortSignal): Promise<T> {
  const url = params ? `${path}?${new URLSearchParams(params)}` : path;
  const res = await fetch(url, { signal });
  const text = await res.text();
  if (!res.ok) {
    let message = text;
    try {
      const body = JSON.parse(text);
      message = body.error ?? message;
    } catch {
      // not JSON: keep the text
    }
    throw new ApiError(message || res.statusText, res.status);
  }
  return (text ? JSON.parse(text) : undefined) as T;
}

interface PromResponse<T> {
  status: "success" | "error";
  data: { resultType: string; result: T };
  error?: string;
}

export const api = {
  hosts: (signal?: AbortSignal) => get<Host[]>("/api/hosts", undefined, signal),
  pods: (host: string, signal?: AbortSignal) => get<Pod[]>(`/api/hosts/${encodeURIComponent(host)}/pods`, undefined, signal),
  dashboards: (signal?: AbortSignal) => get<DashboardSummary[]>("/api/dashboards", undefined, signal),
  dashboard: (id: string, signal?: AbortSignal) => get<Dashboard>(`/api/dashboards/${encodeURIComponent(id)}`, undefined, signal),
  version: () => get<{ version: string }>("/api/version"),

  async range(query: string, start: number, end: number, step: number, signal?: AbortSignal): Promise<Series[]> {
    const r = await get<PromResponse<Series[]>>(
      "/api/metrics/query_range",
      { query, start: String(start), end: String(end), step: `${step}s` },
      signal,
    );
    return r.data.result;
  },

  async instant(query: string, time?: number, signal?: AbortSignal): Promise<Sample[]> {
    const params: Record<string, string> = { query };
    if (time) params.time = String(time);
    const r = await get<PromResponse<Sample[] | [number, string]>>("/api/metrics/query", params, signal);
    // Scalars come back as a bare [time, value] pair.
    if (r.data.resultType === "scalar") {
      return [{ metric: {}, value: r.data.result as [number, string] }];
    }
    return r.data.result as Sample[];
  },

  async logs(query: string, start: number, end: number, limit = 500, signal?: AbortSignal): Promise<LogEntry[]> {
    const params = new URLSearchParams({ query, start: String(start), end: String(end), limit: String(limit) });
    const res = await fetch(`/api/logs/query?${params}`, { signal });
    const text = await res.text();
    if (!res.ok) throw new ApiError(text || res.statusText, res.status);
    return parseLogLines(text).sort((a, b) => (a._time < b._time ? 1 : -1));
  },

  /** hits counts matching lines per step, for a volume histogram. */
  async hits(query: string, start: number, end: number, step: number, signal?: AbortSignal): Promise<{ t: number; n: number }[]> {
    const params = new URLSearchParams({ query, start: String(start), end: String(end), step: `${step}s` });
    const res = await fetch(`/api/logs/hits?${params}`, { signal });
    if (!res.ok) throw new ApiError(await res.text(), res.status);
    const body = (await res.json()) as { hits: { timestamps: string[]; values: number[] }[] };
    const out: { t: number; n: number }[] = [];
    for (const h of body.hits ?? []) h.timestamps.forEach((ts, i) => out.push({ t: Date.parse(ts) / 1000, n: h.values[i] }));
    return out.sort((a, b) => a.t - b.t);
  },

  async fieldNames(query: string, start: number, end: number, signal?: AbortSignal) {
    const params = new URLSearchParams({ query, start: String(start), end: String(end) });
    const res = await fetch(`/api/logs/field_names?${params}`, { signal });
    if (!res.ok) throw new ApiError(await res.text(), res.status);
    return ((await res.json()) as { values: { value: string; hits: number }[] }).values ?? [];
  },

  /** logsBetween reads lines in an exact window, e.g. a line's surroundings; times are RFC 3339. */
  async logsBetween(query: string, start: string, end: string, limit: number, signal?: AbortSignal): Promise<LogEntry[]> {
    const params = new URLSearchParams({ query, start, end, limit: String(limit) });
    const res = await fetch(`/api/logs/query?${params}`, { signal });
    const text = await res.text();
    if (!res.ok) throw new ApiError(text || res.statusText, res.status);
    return parseLogLines(text);
  },

  async fieldValues(query: string, field: string, start: number, end: number, signal?: AbortSignal, limit = 50) {
    const params = new URLSearchParams({ query, field, start: String(start), end: String(end), limit: String(limit) });
    const res = await fetch(`/api/logs/field_values?${params}`, { signal });
    if (!res.ok) throw new ApiError(await res.text(), res.status);
    const body = (await res.json()) as { values: { value: string; hits: number }[] };
    return body.values;
  },
};

/** parseLogLines reads VictoriaLogs' newline-delimited JSON. */
export function parseLogLines(text: string): LogEntry[] {
  const out: LogEntry[] = [];
  for (const line of text.split("\n")) {
    if (!line.trim()) continue;
    try {
      out.push(JSON.parse(line));
    } catch {
      // a partial line from a cut stream
    }
  }
  return out;
}

/** tailLogs streams new log entries until the signal aborts. */
export async function tailLogs(query: string, onEntry: (e: LogEntry) => void, signal: AbortSignal) {
  const res = await fetch(`/api/logs/tail?${new URLSearchParams({ query })}`, { signal });
  if (!res.ok || !res.body) throw new ApiError(await res.text(), res.status);
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    buf += value;
    const lines = buf.split("\n");
    buf = lines.pop() ?? "";
    for (const e of parseLogLines(lines.join("\n"))) onEntry(e);
  }
}

import { parseServiceStats, parseSummary, searchQuery, servicesQuery, type JaegerTrace, type TraceSearch } from "./traces";

export type HostState = "ready" | "notready" | "asleep" | "off" | "booting" | "maintenance" | "unreachable";

export interface Host {
  name: string;
  state: HostState;
  reason?: string;
  since?: string;
  /** What decided the state: kubernetes, metrics, or a provider such as possum or maintenance. */
  stateBy?: string;
  /** When a state set for a while, such as maintenance, ends. */
  until?: string;
  /** A provider's extra facts, e.g. possum's group. */
  details?: Record<string, string>;
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

export type Severity = "page" | "warn" | "info";

export interface Alert {
  id: string;
  rule: string;
  key?: string;
  summary: string;
  severity: Severity;
  notify?: string[];
  labels?: Record<string, string>;
  state: "pending" | "firing";
  since: string;
  firingSince?: string;
  /** When the current round of notifications began: firing, or the last forward. */
  escalating?: string;
  /** When each recipient of this round was last told, by plan key. */
  sent?: Record<string, string>;
  acked?: string;
  ackedBy?: string;
  /** Who has it: a person or team it was forwarded to or taken by. */
  assignee?: string;
  forwardedBy?: string;
  forwardNote?: string;
  timeline?: AlertEvent[];
  silenced: boolean;
  /** The maintenance period holding it: it won't fire, or notify if it already has, until that ends. */
  maintenance?: string;
  /** Where it goes and when, in nanoseconds after the round began. */
  plan?: Planned[];
}

export interface Planned {
  key: string;
  channel: string;
  to?: string;
  person?: string;
  after: number;
}

export interface AlertEvent {
  time: string;
  kind: "firing" | "notified" | "failed" | "acknowledged" | "forwarded" | "taken" | "note" | "held" | "released" | "resolved";
  by?: string;
  to?: string;
  note?: string;
}

/** Past is an alert that fired and resolved; entry pages back through the history. */
export interface Past extends Alert {
  entry: number;
  resolved: string;
}

/** Session is somewhere you're signed in. */
export interface Session {
  id: string;
  provider: string;
  created: string;
  lastSeen: string;
  expires: string;
  address?: string;
  agent?: string;
  current?: boolean;
}

/** AuditEntry is one thing someone did, for admins to look back on. */
export interface AuditEntry {
  id: number;
  time: string;
  actor: string;
  action: string;
  target: string;
  detail?: string;
}

export type Role = "viewer" | "editor" | "admin";

export interface Contact {
  channel: string;
  to: string;
  /** A Go duration such as "10m0s". */
  after: string;
}

export interface User {
  name: string;
  displayName?: string;
  role: Role;
  contacts: Contact[] | null;
  created: string;
}

export interface Me extends User {
  teams: string[];
}

export interface Team {
  name: string;
  members: string[];
  notify?: string[];
}

export interface Token {
  id: string;
  name: string;
  created: string;
  lastUsed?: string;
}

/** can reports whether a role includes at least another. */
export function can(role: Role | undefined, want: Role) {
  const order: Role[] = ["viewer", "editor", "admin"];
  return role !== undefined && order.indexOf(role) >= order.indexOf(want);
}

export interface Silence {
  id: string;
  rule: string;
  key?: string;
  until: string;
  note?: string;
  by?: string;
  created: string;
}

export type RuleOrigin = "builtin" | "file" | "editor";

export interface Rule {
  name: string;
  builtin: boolean;
  origin: RuleOrigin;
  version?: number;
  /** Nanoseconds, as Go durations marshal. */
  every: number;
  for: number;
  source: string;
  lastRun?: string;
  took: number;
  error?: string;
  warning?: string;
}

export interface AlertsSnapshot {
  alerts: Alert[] | null;
  rules: Rule[] | null;
  silences: Silence[] | null;
  channels: string[] | null;
  escalations: string[] | null;
  people: string[];
  teams: string[];
  loadError?: string;
  timezone: string;
}

/** Period is planned maintenance; empty rules and hosts mean every alert. */
export interface Period {
  id: string;
  title: string;
  start: string;
  end?: string;
  every?: "" | "day" | "week";
  repeatUntil?: string;
  rules: string[];
  hosts: string[];
  note?: string;
  by?: string;
}

export interface Occurrence {
  period: Period;
  start: string;
  end?: string;
}

export interface CalendarView {
  maintenance: Occurrence[];
  timezone: string;
}

export interface Found {
  key: string;
  summary: string;
  severity: Severity;
  notify?: string[];
  labels?: Record<string, string>;
}

/** Preview is the notification an alert would send when it fires, and who'd get it. */
export interface Preview {
  key: string;
  severity: Severity;
  title: string;
  text: string;
  plan: Planned[];
  problems?: string[];
}

/** RuleVersion is one saved version of an editor rule. */
export interface RuleVersion {
  version: number;
  author?: string;
  note?: string;
  created: string;
  source?: string;
}

/** Episode is one time a replayed rule would have fired. */
export interface Episode {
  key: string;
  summary: string;
  severity: Severity;
  pending: string;
  fired: string;
  resolved?: string;
}

/** Replay is what a rule would have done over the past few hours or days. */
export interface Replay {
  from: string;
  to: string;
  step: number;
  every: number;
  for: number;
  runs: number;
  episodes: Episode[];
  firing: number[];
  errors: number;
  firstError?: string;
  warnings?: string[];
  found: Found[];
  output: string[];
  /** What the last run's check(now) returned, as Starlark writes it. */
  returned?: string;
  /** What the last run's alerts would send, and to whom. */
  previews: Preview[];
  took: number;
  /** What the running rule of that name really fired over the stretch; null for a draft. */
  actual: Episode[] | null;
}

export interface TestRun {
  found: Found[] | null;
  output: string[] | null;
  error?: string;
  took: number;
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

function get<T>(path: string, params?: Record<string, string>, signal?: AbortSignal): Promise<T> {
  return request<T>("GET", params ? `${path}?${new URLSearchParams(params)}` : path, undefined, signal);
}

/** cleanParams drops empty values, so they aren't sent as "undefined". */
export function cleanParams(p: Record<string, string | number | undefined>): Record<string, string> {
  return Object.fromEntries(Object.entries(p).flatMap(([k, v]) => (v === undefined || v === "" ? [] : [[k, String(v)]])));
}

let redirecting = false;

async function request<T>(method: string, url: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const res = await fetch(url, {
    method,
    signal,
    ...(body === undefined ? {} : { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }),
  });
  const text = await res.text();
  // A session that ended sends the app back to signing in, once.
  if (res.status === 401 && !redirecting && !/^\/api\/(login|me$|ack\/)/.test(url) && !/^\/(login|ack\/)/.test(location.pathname)) {
    redirecting = true;
    location.assign(`/login?next=${encodeURIComponent(location.pathname + location.search)}`);
  }
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
  /** setMaintenance puts a host in maintenance until a time, or until cleared when until is empty. */
  setMaintenance: (host: string, until: string | undefined, note: string) =>
    request<void>("PUT", `/api/hosts/${encodeURIComponent(host)}/maintenance`, { ...(until ? { until } : {}), note }),
  clearMaintenance: (host: string) => request<void>("DELETE", `/api/hosts/${encodeURIComponent(host)}/maintenance`),
  pods: (host: string, signal?: AbortSignal) => get<Pod[]>(`/api/hosts/${encodeURIComponent(host)}/pods`, undefined, signal),
  dashboards: (signal?: AbortSignal) => get<DashboardSummary[]>("/api/dashboards", undefined, signal),
  dashboard: (id: string, signal?: AbortSignal) => get<Dashboard>(`/api/dashboards/${encodeURIComponent(id)}`, undefined, signal),
  version: () => get<{ version: string; traces?: boolean; alerts?: boolean }>("/api/version"),
  alerts: (signal?: AbortSignal) => get<AlertsSnapshot>("/api/alerts", undefined, signal),
  silence: (rule: string, key: string | undefined, until: string, note = "") =>
    request<Silence>("POST", "/api/alerts/silences", { rule, key: key ?? "", until, note }),
  ack: (id: string) => request<Alert>("POST", "/api/alerts/ack", { id }),
  take: (id: string) => request<Alert>("POST", "/api/alerts/take", { id }),
  forward: (id: string, to: string, note: string) => request<Alert>("POST", "/api/alerts/forward", { id, to, note }),
  note: (id: string, note: string) => request<Alert>("POST", "/api/alerts/note", { id, note }),
  /** peekAck reads the alert a notification's link is for, without signing in. */
  peekAck: (token: string) => get<{ alert: Alert; person?: string }>(`/api/ack/${encodeURIComponent(token)}`),
  ackLink: (token: string, by: string) => request<{ alert: Alert; person?: string }>("POST", `/api/ack/${encodeURIComponent(token)}`, { by }),

  setupNeeded: () => get<{ needed: boolean }>("/api/setup"),
  /** setup creates the first admin on a new tremolo, and signs in as them. */
  setup: (name: string, password: string) => request<Me>("POST", "/api/setup", { name, password }),
  login: (name: string, password: string) => request<Me>("POST", "/api/login", { name, password }),
  logout: () => request<void>("POST", "/api/logout"),
  me: (signal?: AbortSignal) => get<Me>("/api/me", undefined, signal),
  updateMe: (change: { displayName?: string; contacts?: Contact[] }) => request<Me>("PUT", "/api/me", change),
  changePassword: (old: string, next: string) => request<void>("POST", "/api/me/password", { old, new: next }),
  sessions: (signal?: AbortSignal) => get<Session[]>("/api/me/sessions", undefined, signal),
  endSession: (id: string) => request<void>("DELETE", `/api/me/sessions/${encodeURIComponent(id)}`),
  endSessions: (user: string) => request<void>("DELETE", `/api/users/${encodeURIComponent(user)}/sessions`),
  tokens: (signal?: AbortSignal) => get<Token[]>("/api/me/tokens", undefined, signal),
  createToken: (name: string) => request<{ token: Token; secret: string }>("POST", "/api/me/tokens", { name }),
  deleteToken: (id: string) => request<void>("DELETE", `/api/me/tokens/${encodeURIComponent(id)}`),
  users: (signal?: AbortSignal) => get<User[]>("/api/users", undefined, signal),
  createUser: (u: { name: string; displayName: string; role: Role; password: string }) => request<User>("POST", "/api/users", u),
  updateUser: (name: string, change: { displayName?: string; role?: Role; password?: string }) =>
    request<User>("PUT", `/api/users/${encodeURIComponent(name)}`, change),
  deleteUser: (name: string) => request<void>("DELETE", `/api/users/${encodeURIComponent(name)}`),
  teams: (signal?: AbortSignal) => get<Team[]>("/api/teams", undefined, signal),
  calendar: (from: Date, to: Date, signal?: AbortSignal) =>
    get<CalendarView>("/api/calendar", { from: from.toISOString(), to: to.toISOString() }, signal),
  savePeriod: (p: Omit<Period, "id" | "by"> & { id?: string }) =>
    p.id ? request<Period>("PUT", `/api/maintenance/${encodeURIComponent(p.id)}`, p) : request<Period>("POST", "/api/maintenance", p),
  deletePeriod: (id: string) => request<void>("DELETE", `/api/maintenance/${encodeURIComponent(id)}`),
  saveTeam: (t: Team) => request<Team[]>("PUT", `/api/teams/${encodeURIComponent(t.name)}`, { members: t.members, notify: t.notify ?? [] }),
  deleteTeam: (name: string) => request<void>("DELETE", `/api/teams/${encodeURIComponent(name)}`),
  history: (f: { q?: string; rule?: string; before?: number }, signal?: AbortSignal) =>
    get<Past[]>("/api/alerts/history", cleanParams({ q: f.q, rule: f.rule, before: f.before, limit: 50 }), signal),
  audit: (f: { actor?: string; before?: number }, signal?: AbortSignal) =>
    get<AuditEntry[]>("/api/audit", cleanParams({ actor: f.actor, before: f.before, limit: 100 }), signal),
  unsilence: (id: string) => request<void>("DELETE", `/api/alerts/silences/${encodeURIComponent(id)}`),
  ruleVersions: (name: string, signal?: AbortSignal) =>
    get<RuleVersion[]>(`/api/alerts/rules/${encodeURIComponent(name)}/versions`, undefined, signal),
  ruleVersion: (name: string, version: number) => get<RuleVersion>(`/api/alerts/rules/${encodeURIComponent(name)}/versions/${version}`),
  saveRule: (name: string, source: string, note: string) =>
    request<RuleVersion>("PUT", `/api/alerts/rules/${encodeURIComponent(name)}`, { source, note }),
  deleteRule: (name: string) => request<void>("DELETE", `/api/alerts/rules/${encodeURIComponent(name)}`),
  /** replay runs source over the last hours; 0 hours runs it once, now. Nothing is saved or sent. */
  replay: (source: string, hours: number, name = "") => request<Replay>("POST", "/api/alerts/replay", { name, source, hours }),
  testRule: (name: string) => request<TestRun>("POST", `/api/alerts/rules/${encodeURIComponent(name)}/test`),

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

/** spanRows runs LogsQL over VictoriaTraces' spans, including the last 30 seconds. */
async function spanRows(query: string, start: number, end: number, signal?: AbortSignal): Promise<LogEntry[]> {
  const params = new URLSearchParams({ query, start: String(start), end: String(end), disable_latency_offset: "true" });
  const res = await fetch(`/api/traces/query?${params}`, { signal });
  const text = await res.text();
  if (!res.ok) throw new ApiError(text || res.statusText, res.status);
  return parseLogLines(text);
}

export const traces = {
  async search(s: TraceSearch, start: number, end: number, limit: number, signal?: AbortSignal) {
    return (await spanRows(searchQuery(s, limit), start, end, signal)).map(parseSummary);
  },

  async services(start: number, end: number, signal?: AbortSignal) {
    return (await spanRows(servicesQuery, start, end, signal)).map(parseServiceStats).filter((s) => s.service);
  },

  async values(query: string, field: string, start: number, end: number, signal?: AbortSignal) {
    const params = new URLSearchParams({ query, field, start: String(start), end: String(end), limit: "200" });
    const res = await fetch(`/api/traces/field_values?${params}`, { signal });
    if (!res.ok) throw new ApiError(await res.text(), res.status);
    return ((await res.json()) as { values: { value: string; hits: number }[] }).values ?? [];
  },

  async get(id: string, signal?: AbortSignal): Promise<JaegerTrace | null> {
    const body = await get<{ data: JaegerTrace[] | null }>(`/api/traces/trace/${encodeURIComponent(id)}`, undefined, signal);
    return body.data?.[0] ?? null;
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

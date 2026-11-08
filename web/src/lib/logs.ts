import type { LogEntry } from "./api";

export type Level = "error" | "warn" | "info" | "debug" | "";

/** A field filter: include (or, with negate, exclude) entries whose field equals value. */
export interface FieldFilter {
  field: string;
  value: string;
  negate?: boolean;
}

/** Fields shown as facets when present, most useful first. Others can be added by hand. */
export const PREFERRED_FACETS = [
  "service.name",
  "k8s.namespace.name",
  "k8s.container.name",
  "host.name",
  "source",
  "app",
  "job",
  "k8s.object.kind",
  "severity_text",
];

/** Fields that never make useful facets: unique per line, or internal. */
const NOT_FACETS = new Set(["_msg", "_time", "_stream", "_stream_id", "k8s.pod.uid", "log.file.path", "log.iostream", "logtag", "time"]);

export function facetCandidates(fields: { value: string; hits: number }[]): string[] {
  return fields.map((f) => f.value).filter((f) => !NOT_FACETS.has(f) && !f.startsWith("_"));
}

// What each level looks like in the wild: OTel severity, logfmt, JSON, plain words, klog ("E1106 12:00:00").
const LEVEL_PATTERNS: Record<Exclude<Level, "">, { severity: string; msg: RegExp; msgQL: string }> = {
  error: {
    severity: "^(error|err|fatal|panic|critical|crit|alert|emerg)",
    msg: /\blevel=(error|err|fatal|panic)\b|"(level|severity)":\s*"(error|err|fatal|panic|critical)"|\b(ERROR|FATAL|PANIC|CRITICAL)\b|^[EF]\d{4} \d/,
    msgQL: `(?i:level=(error|err|fatal|panic)\\b|"(level|severity)": ?"(error|err|fatal|panic|critical)")|\\b(ERROR|FATAL|PANIC|CRITICAL)\\b|^[EF][0-9]{4} [0-9]`,
  },
  warn: {
    severity: "^warn",
    msg: /\blevel=warn(ing)?\b|"(level|severity)":\s*"warn(ing)?"|\bWARN(ING)?\b|^W\d{4} \d/,
    msgQL: `(?i:level=warn(ing)?\\b|"(level|severity)": ?"warn(ing)?")|\\bWARN(ING)?\\b|^W[0-9]{4} [0-9]`,
  },
  info: {
    severity: "^(info|notice)",
    msg: /\blevel=info\b|"(level|severity)":\s*"info"|\bINFO\b|^I\d{4} \d/,
    msgQL: "",
  },
  debug: {
    severity: "^(debug|trace)",
    msg: /\blevel=(debug|trace)\b|"(level|severity)":\s*"(debug|trace)"|\b(DEBUG|TRACE)\b|^D\d{4} \d/,
    msgQL: "",
  },
};

const ORDER: Exclude<Level, "">[] = ["error", "warn", "info", "debug"];

/** level guesses a line's level from its severity field, then from the message. */
export function level(e: LogEntry): Level {
  const sev = (e.severity_text ?? e.level ?? e["log.level"] ?? e.severity ?? "").toLowerCase();
  if (sev) {
    for (const l of ORDER) if (new RegExp(LEVEL_PATTERNS[l].severity).test(sev)) return l;
  }
  const msg = e._msg ?? "";
  for (const l of ORDER) if (LEVEL_PATTERNS[l].msg.test(msg)) return l;
  return "";
}

/** levelClause is a LogsQL filter for lines at this level or worse ("warn" includes errors). */
export function levelClause(min: "" | "warn" | "error"): string {
  if (!min) return "";
  const levels = min === "error" ? (["error"] as const) : (["error", "warn"] as const);
  const sev = levels.map((l) => LEVEL_PATTERNS[l].severity.slice(1)).join("|");
  const msg = levels.map((l) => LEVEL_PATTERNS[l].msgQL).join("|");
  return `(severity_text:~"(?i)^(${sev})" OR level:~"(?i)^(${sev})" OR _msg:~${quote(msg)})`;
}

export function quote(v: string): string {
  return `"${v.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
}

function fieldName(f: string): string {
  return /^[\w.]+$/.test(f) ? f : quote(f);
}

/** buildQuery combines free text, field filters and a minimum level into LogsQL. */
export function buildQuery(text: string, filters: FieldFilter[], minLevel: "" | "warn" | "error"): string {
  const parts = [text.trim() || "*"];
  for (const f of filters) parts.push(`${f.negate ? "-" : ""}${fieldName(f.field)}:=${quote(f.value)}`);
  const lv = levelClause(minLevel);
  if (lv) parts.push(lv);
  return parts.join(" ");
}

/** encodeFilter/decodeFilter keep filters in the URL as "field=value" or "!field=value". */
export function encodeFilter(f: FieldFilter): string {
  return `${f.negate ? "!" : ""}${f.field}=${f.value}`;
}

export function decodeFilter(s: string): FieldFilter | null {
  const negate = s.startsWith("!");
  const body = negate ? s.slice(1) : s;
  const i = body.indexOf("=");
  if (i <= 0) return null;
  return { field: body.slice(0, i), value: body.slice(i + 1), negate };
}

/** sourceOf names where a line came from, as specifically as its fields allow. */
export function sourceOf(e: LogEntry): string {
  const ns = e["k8s.namespace.name"];
  const pod = e["k8s.pod.name"];
  const obj = e["k8s.object.name"];
  if (ns && pod) return `${ns}/${pod}`;
  if (obj) return `${ns ? ns + "/" : ""}${e["k8s.object.kind"] ?? ""} ${obj}`.trim();
  const svc = e["service.name"];
  const host = e["host.name"];
  if (svc && host) return `${svc}@${host}`;
  return svc || host || e.app || e.job || e.source || "";
}

/** A piece of a message, for coloring. */
export type Token = { kind: "text" | "key" | "string" | "number" | "bool" | "punct" | "time"; text: string };

/** tokenize splits a message into colorable pieces: JSON objects and key=value pairs get keys and values. */
export function tokenize(msg: string): Token[] {
  const trimmed = msg.trim();
  if (trimmed.startsWith("{") && trimmed.endsWith("}")) {
    try {
      const obj = JSON.parse(trimmed) as Record<string, unknown>;
      const out: Token[] = [];
      Object.entries(obj).forEach(([k, v], i) => {
        if (i) out.push({ kind: "punct", text: " " });
        out.push({ kind: "key", text: `${k}=` });
        out.push(valueToken(v));
      });
      return out;
    } catch {
      // not JSON after all
    }
  }
  // logfmt-ish: key=value or key="quoted value"; everything else stays text.
  const re = /([A-Za-z_][\w.\-/]*)=("(?:[^"\\]|\\.)*"|[^\s]*)/g;
  const out: Token[] = [];
  let last = 0;
  for (let m = re.exec(msg); m; m = re.exec(msg)) {
    if (m.index > last) out.push(...plain(msg.slice(last, m.index)));
    out.push({ kind: "key", text: `${m[1]}=` });
    out.push(valueToken(m[2]));
    last = m.index + m[0].length;
  }
  if (last < msg.length) out.push(...plain(msg.slice(last)));
  return out;
}

function valueToken(v: unknown): Token {
  if (typeof v === "number") return { kind: "number", text: String(v) };
  if (typeof v === "boolean") return { kind: "bool", text: String(v) };
  if (typeof v !== "string") return { kind: "string", text: JSON.stringify(v) };
  if (/^-?\d+(\.\d+)?(ms|s|µs|ns|m|h|%|[KMG]i?B)?$/.test(v)) return { kind: "number", text: v };
  if (v === "true" || v === "false") return { kind: "bool", text: v };
  if (/^\d{4}-\d{2}-\d{2}T/.test(v.replace(/^"/, ""))) return { kind: "time", text: v };
  return { kind: "string", text: v };
}

// Leading timestamps in plain lines are dimmed so the message stands out.
const LEADING_TIME = /^(\s*(\d{4}[-/]\d{2}[-/]\d{2}[T ][\d:.,]+(Z|[+-]\d{2}:?\d{2})?|[IWEDF]\d{4} [\d:.]+|\w{3} +\d+ [\d:]+)\s*)/;

function plain(s: string): Token[] {
  const m = LEADING_TIME.exec(s);
  if (m && m.index === 0) return [{ kind: "time", text: m[1] }, { kind: "text", text: s.slice(m[1].length) }];
  return [{ kind: "text", text: s }];
}

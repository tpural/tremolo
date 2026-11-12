import type { LogEntry } from "./api";
import type { JaegerSpan, JaegerTrace, KeyValue } from "./traces";

/**
 * Agent runs are traces that follow OpenTelemetry's GenAI conventions: an
 * invoke_agent span per run, a chat span per model call, an execute_tool span
 * per tool call. tremolo also reads a few attributes of its own:
 * tremolo.investigation.id (the investigation worked), tremolo.task
 * (e.g. patrol), tremolo.outcome and tremolo.summary on the run, and
 * tremolo.tool.found=false on a tool call that found nothing.
 */
const op = `"span_attr:gen_ai.operation.name"`;

/** runsQuery is the LogsQL for runs, newest first, optionally of one investigation. */
export function runsQuery(investigation?: number): string {
  const inv = investigation ? ` "span_attr:tremolo.investigation.id":=${investigation}` : "";
  return `${op}:=invoke_agent${inv} | sort by (_time) desc | limit 200`;
}

/** stepsQuery counts each run's model and tool calls, tokens and failures. */
export const stepsQuery =
  `${op}:in(chat, execute_tool) | stats by (trace_id, ${op}) count() n, ` +
  `sum("span_attr:gen_ai.usage.input_tokens") input, sum("span_attr:gen_ai.usage.output_tokens") output, ` +
  `count() if (status_code:=2) errors, count() if ("span_attr:tremolo.tool.found":=false) empty`;

/** toolsQuery is how each tool fares across runs. */
export const toolsQuery =
  `${op}:=execute_tool | stats by ("span_attr:gen_ai.tool.name") count() calls, ` +
  `count() if (status_code:=2) errors, count() if ("span_attr:tremolo.tool.found":=false) empty | sort by (calls) desc | limit 30`;

export interface Run {
  trace: string;
  agent: string;
  model: string;
  /** start is in milliseconds; duration in seconds. */
  start: number;
  duration: number;
  investigation?: number;
  title?: string;
  kind?: string;
  task?: string;
  /** name is the run span's name, less invoke_agent. */
  name: string;
  outcome?: string;
  summary?: string;
  error: boolean;
  steps: number;
  tokens: number;
  tools: number;
  toolErrors: number;
  empty: number;
}

const num = (v?: string) => (v ? Number(v) || 0 : 0);

/** parseRuns joins the runs with their step counts. */
export function parseRuns(runs: LogEntry[], steps: LogEntry[]): Run[] {
  const by = new Map<string, LogEntry[]>();
  for (const s of steps) by.set(s.trace_id, [...(by.get(s.trace_id) ?? []), s]);
  return runs.map((r) => {
    const chat = by.get(r.trace_id)?.find((s) => s[`span_attr:gen_ai.operation.name`] === "chat");
    const tool = by.get(r.trace_id)?.find((s) => s[`span_attr:gen_ai.operation.name`] === "execute_tool");
    const inv = num(r["span_attr:tremolo.investigation.id"]);
    return {
      trace: r.trace_id,
      agent: r["span_attr:gen_ai.agent.name"] || r["resource_attr:service.name"] || "agent",
      model: r["span_attr:gen_ai.request.model"] ?? "",
      start: num(r.start_time_unix_nano) / 1e6,
      duration: num(r.duration) / 1e9,
      investigation: inv || undefined,
      title: r["span_attr:tremolo.investigation.title"],
      kind: r["span_attr:tremolo.investigation.kind"],
      task: r["span_attr:tremolo.task"] || (inv ? "investigation" : undefined),
      name: (r.name ?? "").replace(/^invoke_agent\s*/, ""),
      outcome: r["span_attr:tremolo.outcome"],
      summary: r["span_attr:tremolo.summary"],
      error: r.status_code === "2",
      steps: num(chat?.n),
      tokens: num(chat?.input) + num(chat?.output),
      tools: num(tool?.n),
      toolErrors: num(tool?.errors),
      empty: num(tool?.empty),
    };
  });
}

/** OUTCOME is how each outcome reads, and whether it's good. */
export const OUTCOME: Record<string, { label: string; tone: "good" | "warn" | "bad" | "muted" }> = {
  fixed: { label: "fixed", tone: "good" },
  abandoned: { label: "nothing to do", tone: "muted" },
  resolved: { label: "resolved itself", tone: "muted" },
  escalated: { label: "escalated", tone: "warn" },
  stopped: { label: "stopped", tone: "bad" },
  error: { label: "failed", tone: "bad" },
  "wake-limit": { label: "wake limit", tone: "warn" },
  skipped: { label: "skipped", tone: "muted" },
  "all clear": { label: "all clear", tone: "good" },
  opened: { label: "found problems", tone: "warn" },
};

export function tag(s: { tags: KeyValue[] } | { fields: KeyValue[] }, key: string): string | undefined {
  const list = "tags" in s ? s.tags : s.fields;
  const v = list.find((t) => t.key === key)?.value;
  // VictoriaTraces keeps an empty value as "-".
  return v === undefined || v === "-" ? undefined : String(v);
}

/** Step is one entry of a run, in time order. */
export type Step =
  | {
      kind: "chat";
      at: number;
      duration: number;
      step: number;
      said: string;
      input?: string;
      inputRole?: string;
      tokensIn: number;
      tokensOut: number;
      error?: string;
      calls: string[];
    }
  | { kind: "tool"; at: number; duration: number; name: string; args: string; result: string; found: boolean; error?: string }
  | { kind: "event"; at: number; name: string; fields: Record<string, string> };

/** steps reads a run's trace into what happened, in order. Times are milliseconds from the run's start. */
export function steps(trace: JaegerTrace): { root?: JaegerSpan; steps: Step[] } {
  const root =
    trace.spans.find((s) => tag(s, "gen_ai.operation.name") === "invoke_agent") ?? trace.spans.find((s) => s.references.length === 0);
  const t0 = root?.startTime ?? Math.min(...trace.spans.map((s) => s.startTime));
  const out: Step[] = [];
  const failed = (s: JaegerSpan) =>
    tag(s, "error") === "true" || tag(s, "otel.status_code") === "ERROR" ? (tag(s, "otel.status_description") ?? "failed") : undefined;
  for (const s of trace.spans) {
    // Jaeger times are microseconds; steps are in milliseconds.
    const at = (s.startTime - t0) / 1000;
    const dur = s.duration / 1000;
    switch (tag(s, "gen_ai.operation.name")) {
      case "chat": {
        let calls: string[] = [];
        try {
          const out = JSON.parse(tag(s, "gen_ai.output.messages") ?? "[]") as { tool_calls?: { function: { name: string } }[] }[];
          calls = (out[0]?.tool_calls ?? []).map((c) => c.function.name);
        } catch {
          // a clipped attribute isn't JSON; the tool spans still say what was called
        }
        out.push({
          kind: "chat",
          at,
          duration: dur,
          step: Number(tag(s, "upkeeper.step") ?? 0),
          said: tag(s, "upkeeper.said") ?? said(tag(s, "gen_ai.output.messages")),
          input: tag(s, "upkeeper.input"),
          inputRole: tag(s, "upkeeper.input.role"),
          tokensIn: Number(tag(s, "gen_ai.usage.input_tokens") ?? 0),
          tokensOut: Number(tag(s, "gen_ai.usage.output_tokens") ?? 0),
          error: failed(s),
          calls,
        });
        break;
      }
      case "execute_tool":
        out.push({
          kind: "tool",
          at,
          duration: dur,
          name: tag(s, "gen_ai.tool.name") ?? s.operationName,
          args: tag(s, "gen_ai.tool.call.arguments") ?? "",
          result: tag(s, "gen_ai.tool.call.result") ?? "",
          found: tag(s, "tremolo.tool.found") !== "false",
          error: failed(s),
        });
        break;
    }
    for (const l of s.logs ?? []) {
      const fields: Record<string, string> = {};
      for (const f of l.fields) fields[f.key] = String(f.value);
      out.push({ kind: "event", at: (l.timestamp - t0) / 1000, name: fields.event ?? "event", fields });
    }
  }
  out.sort((a, b) => a.at - b.at);
  return { root, steps: out };
}

function said(messages?: string): string {
  try {
    return (JSON.parse(messages ?? "[]") as { content?: string }[])[0]?.content ?? "";
  } catch {
    return "";
  }
}

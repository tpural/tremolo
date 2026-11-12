import { useQuery } from "@tanstack/react-query";
import { BookOpen, Bot, ChevronRight, CircleAlert, Footprints, Lightbulb, MessageSquare, SearchX, Wrench } from "lucide-react";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { OUTCOME, parseRuns, runsQuery, steps, stepsQuery, tag, toolsQuery, type Run, type Step } from "../lib/agentRuns";
import { traces } from "../lib/api";
import { duration } from "../lib/format";
import { badge, badgeTone, card, EmptyState, ErrorNote, PageHeader, SectionHeader, seg, skeleton } from "../components/ui";

const WINDOWS = [
  { label: "1d", seconds: 86400 },
  { label: "7d", seconds: 7 * 86400 },
  { label: "30d", seconds: 30 * 86400 },
];

const tone = { good: badgeTone.good, warn: badgeTone.warn, bad: badgeTone.bad, muted: badgeTone.neutral };

function Outcome({ run }: { run: Run }) {
  const o = OUTCOME[run.outcome ?? ""] ?? (run.error ? OUTCOME.error : undefined);
  if (!o) return run.outcome ? <span className="text-xs text-muted">{run.outcome}</span> : null;
  return <span className={`${badge} ${tone[o.tone]}`}>{o.label}</span>;
}

const tokens = (n: number) => (n >= 10_000 ? `${Math.round(n / 1000)}k` : n.toLocaleString());

/** useRuns reads agent runs over the last seconds, optionally of one investigation. */
export function useRuns(seconds: number, investigation?: number) {
  return useQuery({
    queryKey: ["agent-runs", seconds, investigation],
    queryFn: async ({ signal }) => {
      const end = Math.floor(Date.now() / 1000) + 60;
      const start = end - seconds;
      const [runs, counts] = await Promise.all([
        traces.rows(runsQuery(investigation), start, end, signal),
        traces.rows(stepsQuery, start, end, signal),
      ]);
      return parseRuns(runs, counts);
    },
    refetchInterval: 30_000,
  });
}

/** RunList is a table of runs. */
export function RunList({ runs, compact }: { runs: Run[]; compact?: boolean }) {
  return (
    <ul className={`divide-y divide-line/60 ${card}`}>
      {runs.map((r) => (
        <li key={r.trace}>
          <Link to={`/agents/runs/${r.trace}`} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm hover:bg-panel-2">
            <span className="w-28 shrink-0 text-xs text-muted tabular-nums">
              {new Date(r.start).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}
            </span>
            {!compact && <span className="w-20 shrink-0 font-mono text-xs">{r.agent}</span>}
            <span className="min-w-0 flex-1 truncate">
              {r.task === "patrol" ? (
                <span className="text-muted">Patrol</span>
              ) : (
                (r.title ?? (r.investigation ? `#${r.investigation}` : r.name || r.trace))
              )}
              {r.summary && <span className="ml-2 text-xs text-muted">{r.summary}</span>}
            </span>
            <Outcome run={r} />
            <span className="w-40 shrink-0 text-right text-xs text-muted tabular-nums">
              {r.steps} steps · {tokens(r.tokens)} tokens · {duration(r.duration)}
            </span>
            <ChevronRight className="size-4 text-muted" />
          </Link>
        </li>
      ))}
    </ul>
  );
}

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className={`${card} px-3 py-2`}>
      <div className="text-xs text-muted">{label}</div>
      <div className="font-mono text-lg font-semibold">{value}</div>
      {sub && <div className="text-[11px] text-muted">{sub}</div>}
    </div>
  );
}

/** AgentRunsPage lists what agents did: each investigation and patrol, how it ended, and what it cost. */
export function AgentRunsPage() {
  const [win, setWin] = useState(WINDOWS[1]);
  const runs = useRuns(win.seconds);
  const tools = useQuery({
    queryKey: ["agent-tools", win.seconds],
    queryFn: ({ signal }) => {
      const end = Math.floor(Date.now() / 1000) + 60;
      return traces.rows(toolsQuery, end - win.seconds, end, signal);
    },
  });
  const list = runs.data ?? [];
  const work = list.filter((r) => r.task !== "patrol");
  const count = (o: string) => work.filter((r) => r.outcome === o).length;
  const total = list.reduce((t, r) => t + r.tokens, 0);
  const steps = work.length ? Math.round(work.reduce((t, r) => t + r.steps, 0) / work.length) : 0;
  return (
    <div className="space-y-6">
      <PageHeader
        title="Agents"
        sub="What agents did: each investigation and patrol, step by step, from their traces"
        actions={
          <div className={seg.wrap}>
            {WINDOWS.map((w) => (
              <button key={w.label} type="button" className={seg.item} aria-pressed={w === win} onClick={() => setWin(w)}>
                {w.label}
              </button>
            ))}
          </div>
        }
      />
      {runs.error && <ErrorNote error={runs.error} />}
      {!runs.data && !runs.error && <div className={`${skeleton} h-40`} />}
      {runs.data && list.length === 0 && (
        <EmptyState>
          No agent runs in the last {win.label}. Agents show here when they send traces that follow OpenTelemetry's GenAI conventions (an
          invoke_agent span per run, with chat and execute_tool spans under it); set{" "}
          <code className="font-mono">tremolo.investigation.id</code> on the run to link it to its investigation.
        </EmptyState>
      )}
      {list.length > 0 && (
        <>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-6">
            <Stat label="Investigations" value={String(work.length)} sub={`${list.length - work.length} patrols`} />
            <Stat label="Fixed" value={String(count("fixed"))} sub={`${count("abandoned") + count("resolved")} nothing to do`} />
            <Stat label="Escalated" value={String(count("escalated") + count("wake-limit"))} />
            <Stat label="Stopped" value={String(count("stopped") + count("error"))} sub="upkeeper gave up for the model" />
            <Stat label="Steps" value={String(steps)} sub="per investigation" />
            <Stat label="Tokens" value={tokens(total)} />
          </div>
          <section className="space-y-2">
            <SectionHeader title="Runs" />
            <RunList runs={list} />
          </section>
          {(tools.data?.length ?? 0) > 0 && (
            <section className="space-y-2">
              <SectionHeader title="Tools" />
              <p className="text-sm text-muted">
                A tool that often fails or finds nothing is one the model doesn't use well: its description, or what it returns, needs work.
              </p>
              <table className={`${card} w-full text-sm`}>
                <thead className="text-left text-xs text-muted">
                  <tr>
                    <th className="px-3 py-2 font-medium">Tool</th>
                    <th className="px-3 py-2 text-right font-medium">Calls</th>
                    <th className="px-3 py-2 text-right font-medium">Failed</th>
                    <th className="px-3 py-2 text-right font-medium">Found nothing</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line/60">
                  {tools.data?.map((t) => {
                    const calls = Number(t.calls) || 0;
                    const pct = (v: string) => (calls ? `${Math.round(((Number(v) || 0) / calls) * 100)}%` : "-");
                    return (
                      <tr key={t["span_attr:gen_ai.tool.name"]}>
                        <td className="px-3 py-1.5 font-mono text-xs">{t["span_attr:gen_ai.tool.name"]}</td>
                        <td className="px-3 py-1.5 text-right tabular-nums">{calls}</td>
                        <td className="px-3 py-1.5 text-right tabular-nums">{pct(t.errors)}</td>
                        <td className="px-3 py-1.5 text-right tabular-nums">{pct(t.empty)}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </section>
          )}
        </>
      )}
    </div>
  );
}

function Fold({ text, lines = 4 }: { text: string; lines?: number }) {
  const [open, setOpen] = useState(false);
  const all = text.split("\n");
  if (all.length <= lines && text.length < 600)
    return <pre className="whitespace-pre-wrap break-words font-mono text-xs text-muted">{text}</pre>;
  return (
    <div>
      <pre className="whitespace-pre-wrap break-words font-mono text-xs text-muted">
        {open ? text : all.slice(0, lines).join("\n").slice(0, 600) + "…"}
      </pre>
      <button type="button" onClick={() => setOpen(!open)} className="text-xs text-accent">
        {open ? "Show less" : `Show all (${all.length} lines)`}
      </button>
    </div>
  );
}

function StepRow({ s }: { s: Step }) {
  const at = <span className="w-14 shrink-0 pt-0.5 text-right text-[11px] text-muted tabular-nums">+{duration(s.at / 1000)}</span>;
  switch (s.kind) {
    case "chat":
      return (
        <li className="flex gap-3 px-3 py-2">
          {at}
          <MessageSquare className="mt-0.5 size-4 shrink-0 text-accent" />
          <div className="min-w-0 flex-1 space-y-1 text-sm">
            <div className="flex flex-wrap gap-x-2 text-xs text-muted">
              <span className="font-medium text-fg">Step {s.step || ""}</span>
              <span>
                thought {duration(s.duration / 1000)} · {s.tokensIn.toLocaleString()} in, {s.tokensOut.toLocaleString()} out
              </span>
              {s.calls.length > 0 && <span>→ {s.calls.join(", ")}</span>}
            </div>
            {s.error && <p className="text-bad">{s.error}</p>}
            {s.said.trim() ? (
              <p className="whitespace-pre-wrap">{s.said}</p>
            ) : (
              <p className="text-xs text-muted italic">said nothing, only called tools</p>
            )}
            {s.step === 1 && s.input && (
              <details className="text-xs">
                <summary className="cursor-pointer text-muted">What it was given</summary>
                <Fold text={s.input} lines={30} />
              </details>
            )}
          </div>
        </li>
      );
    case "tool":
      return (
        <li className="flex gap-3 px-3 py-2">
          {at}
          {s.error ? (
            <CircleAlert className="mt-0.5 size-4 shrink-0 text-bad" />
          ) : s.found ? (
            <Wrench className="mt-0.5 size-4 shrink-0 text-muted" />
          ) : (
            <SearchX className="mt-0.5 size-4 shrink-0 text-warn" />
          )}
          <div className="min-w-0 flex-1 space-y-1 text-sm">
            <div className="flex flex-wrap items-baseline gap-x-2">
              <span className="font-mono text-xs font-semibold">{s.name}</span>
              <span className="min-w-0 truncate font-mono text-xs text-muted">{s.args !== "{}" ? s.args : ""}</span>
              <span className="text-[11px] text-muted">{duration(s.duration / 1000)}</span>
              {!s.found && !s.error && <span className={`${badge} ${badgeTone.warn}`}>found nothing</span>}
              {s.error && <span className={`${badge} ${badgeTone.bad}`}>failed</span>}
            </div>
            <Fold text={s.result} />
          </div>
        </li>
      );
    case "checkpoint":
      return (
        <li className="flex gap-3 border-l-2 border-accent bg-accent/5 px-3 py-2">
          {at}
          <Footprints className="mt-0.5 size-4 shrink-0 text-accent" />
          <div className="min-w-0 flex-1 space-y-1 text-sm">
            <div className="flex flex-wrap gap-x-2 text-xs text-muted">
              <span className="font-medium text-fg">Stepped back</span>
              <span>because {s.why}</span>
              <span>· {duration(s.duration / 1000)}</span>
            </div>
            {s.error ? (
              <p className="text-bad">Couldn't decide: {s.error}</p>
            ) : (
              <dl className="grid grid-cols-[5rem_1fr] gap-x-2 gap-y-0.5">
                {(
                  [
                    ["Known", s.known],
                    ["Tried", s.tried],
                    ["Missing", s.missing],
                  ] as const
                )
                  .filter(([, v]) => v)
                  .map(([k, v]) => (
                    <div key={k} className="contents">
                      <dt className="text-muted">{k}</dt>
                      <dd>{v}</dd>
                    </div>
                  ))}
                <dt className="text-muted">Next</dt>
                <dd>
                  <span className={`${badge} ${s.next === "escalate" ? badgeTone.warn : badgeTone.info}`}>{s.next}</span>
                  {s.lookAt && <span className="ml-2">{s.lookAt}</span>}
                </dd>
              </dl>
            )}
          </div>
        </li>
      );
    case "event": {
      if (s.name === "upkeeper.source") {
        const still = Number(s.fields.still ?? 1);
        const state =
          s.fields.gone === "true"
            ? "the page is gone"
            : still < 0.8
              ? `changed (${Math.round(still * 100)}% still there)`
              : "still says it";
        return (
          <li className="flex gap-3 bg-panel-2/50 px-3 py-2">
            {at}
            <BookOpen className={`mt-0.5 size-4 shrink-0 ${state === "still says it" ? "text-good" : "text-warn"}`} />
            <p className="min-w-0 flex-1 text-sm">
              <span className="font-medium">Rechecked a runbook source</span> ({s.fields.status}):{" "}
              <a href={s.fields.url} className="break-all text-accent" target="_blank" rel="noreferrer">
                {s.fields.url}
              </a>{" "}
              <span className="text-muted">— {state}</span>
            </p>
          </li>
        );
      }
      const decision = s.fields["upkeeper.decision"];
      const nudge = s.fields["upkeeper.text"];
      return (
        <li className="flex gap-3 bg-panel-2/50 px-3 py-2">
          {at}
          <Lightbulb className="mt-0.5 size-4 shrink-0 text-warn" />
          <p className="min-w-0 flex-1 text-sm">
            {decision ? (
              <>
                <span className="font-medium">Decided: {decision}</span>
                {s.fields["upkeeper.reason"] && <span className="text-muted"> — {s.fields["upkeeper.reason"]}</span>}
              </>
            ) : nudge ? (
              <>
                <span className="font-medium">Nudged the model:</span> <span className="text-muted">{nudge}</span>
              </>
            ) : (
              <>
                <span className="font-medium">{s.name}</span>{" "}
                <span className="text-muted">
                  {Object.entries(s.fields)
                    .filter(([k]) => k !== "event")
                    .map(([k, v]) => `${k}=${v}`)
                    .join(" ")}
                </span>
              </>
            )}
          </p>
        </li>
      );
    }
  }
}

/** AgentRunPage reads one run as a conversation: what the model said, each tool it called and what came back, and the agent's own decisions. */
export function AgentRunPage() {
  const { trace = "" } = useParams();
  const t = useQuery({ queryKey: ["trace", trace], queryFn: ({ signal }) => traces.get(trace, signal) });
  if (t.error) return <ErrorNote error={t.error} />;
  if (!t.data)
    return t.isLoading ? (
      <div className={`${skeleton} h-60`} />
    ) : (
      <EmptyState>No trace {trace}; it may be older than trace retention.</EmptyState>
    );
  const { root, steps: list } = steps(t.data);
  const inv = root ? tag(root, "tremolo.investigation.id") : undefined;
  const outcome = root ? (tag(root, "tremolo.outcome") ?? "") : "";
  const chats = list.filter((s) => s.kind === "chat");
  const tools = list.filter((s) => s.kind === "tool");
  const tok = chats.reduce((n, s) => n + (s.kind === "chat" ? s.tokensIn + s.tokensOut : 0), 0);
  const o = OUTCOME[outcome];
  return (
    <div className="max-w-5xl space-y-6">
      <PageHeader
        title={
          root
            ? (tag(root, "tremolo.investigation.title") ?? (tag(root, "tremolo.task") === "patrol" ? "Patrol" : root.operationName))
            : trace
        }
        back={{ to: "/agents/runs", label: "Agents" }}
        sub={
          <span className="flex flex-wrap items-center gap-2">
            <Bot className="size-4" />
            {root && tag(root, "gen_ai.agent.name")} · {root && tag(root, "gen_ai.request.model")}
            {o ? <span className={`${badge} ${tone[o.tone]}`}>{o.label}</span> : outcome && <span>· {outcome}</span>}
            {inv && (
              <Link to={`/investigations/${inv}`} className="text-accent">
                · investigation #{inv}
              </Link>
            )}
            <Link to={`/traces/${trace}`} className="text-accent">
              · as a trace
            </Link>
          </span>
        }
      />
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label="Steps" value={String(chats.length)} />
        <Stat
          label="Tool calls"
          value={String(tools.length)}
          sub={`${tools.filter((s) => s.kind === "tool" && !s.found).length} found nothing`}
        />
        <Stat label="Tokens" value={tokens(tok)} />
        <Stat label="Took" value={root ? duration(root.duration / 1e6) : "-"} />
      </div>
      {root && tag(root, "tremolo.summary") && <p className={`${card} px-3 py-2 text-sm`}>{tag(root, "tremolo.summary")}</p>}
      <ul className={`divide-y divide-line/60 ${card}`}>
        {list.map((s, i) => (
          <StepRow key={i} s={s} />
        ))}
      </ul>
    </div>
  );
}

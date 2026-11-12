import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, CirclePause, CirclePlay, Hammer, ShieldAlert } from "lucide-react";
import { useState } from "react";
import { Link, NavLink, useParams, useSearchParams } from "react-router";
import { api, can, type Investigation, type InvestigationEvent } from "../lib/api";
import { ago } from "../lib/format";
import { useMe, useVersion } from "../lib/hooks";
import { RunList, useRuns } from "./AgentRunsPage";
import { badge, badgeTone, button, card, EmptyState, ErrorNote, field, PageHeader, SectionHeader, seg, skeleton } from "../components/ui";

const STATUS: Record<Investigation["status"], { label: string; tone: string }> = {
  open: { label: "open", tone: badgeTone.warn },
  escalated: { label: "needs a person", tone: badgeTone.bad },
  fixed: { label: "fixed", tone: badgeTone.good },
  resolved: { label: "went away", tone: badgeTone.good },
  abandoned: { label: "abandoned", tone: "" },
};

const TABS = [
  { id: "open", label: "Open", status: "open,escalated" },
  { id: "done", label: "Done", status: "fixed,resolved,abandoned" },
  { id: "all", label: "All", status: "" },
];

function claim(i: Investigation) {
  if (i.status !== "open" && i.status !== "escalated") return null;
  if (i.claimedBy && i.claimExpires && new Date(i.claimExpires) > new Date()) return `${i.claimedBy} is on it`;
  return i.status === "open" ? "waiting for an agent" : null;
}

/** InvestigationsPage is the agents' work: alerts to fix, gaps, tuning and runbook reviews. */
export function InvestigationsPage() {
  const [params, setParams] = useSearchParams();
  const tab = TABS.find((t) => t.id === params.get("show")) ?? TABS[0];
  const list = useInfiniteQuery({
    queryKey: ["investigations", tab.status, "page"],
    queryFn: ({ pageParam, signal }) => api.investigations({ status: tab.status, before: pageParam }, signal),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (last) => (last.length === 50 ? last[last.length - 1].id : undefined),
    refetchInterval: 15_000,
  });
  const rows = list.data?.pages.flat() ?? [];
  return (
    <div className="space-y-6">
      <PageHeader title="Investigations" sub="What agents are working on, and what they found and did" />
      <div className={seg.wrap} role="group" aria-label="Show">
        {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            aria-pressed={t.id === tab.id}
            className={seg.item}
            onClick={() =>
              setParams(
                (p) => {
                  p.set("show", t.id);
                  return p;
                },
                { replace: true },
              )
            }
          >
            {t.label}
          </button>
        ))}
      </div>
      {list.error && <ErrorNote error={list.error} />}
      {list.isLoading && <div className={`${skeleton} h-40`} />}
      {list.data && !rows.length && (
        <EmptyState>{tab.id === "open" ? "Nothing to work on. Each alert that fires opens one here." : "None yet."}</EmptyState>
      )}
      {rows.length > 0 && (
        <ul className={`divide-y divide-line/60 ${card}`}>
          {rows.map((i) => (
            <li key={i.id}>
              <Link
                to={`/investigations/${i.id}`}
                className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm hover:bg-panel-2"
              >
                <span className={`${badge} ${STATUS[i.status].tone}`}>{STATUS[i.status].label}</span>
                <span className="font-medium">{i.title}</span>
                <span className="text-xs text-muted">{i.kind === "incident" ? i.rule : i.kind}</span>
                {claim(i) && (
                  <span className="inline-flex items-center gap-1 text-xs text-muted">
                    <Bot className="size-3.5" /> {claim(i)}
                  </span>
                )}
                {i.fix && <span className="text-xs text-muted">· {i.fix}</span>}
                <span className="ml-auto text-xs text-muted">{ago(i.opened)} ago</span>
              </Link>
            </li>
          ))}
        </ul>
      )}
      {list.hasNextPage && (
        <button type="button" onClick={() => list.fetchNextPage()} disabled={list.isFetchingNextPage} className="text-sm text-accent">
          {list.isFetchingNextPage ? "Loading…" : "Older"}
        </button>
      )}
    </div>
  );
}

function EventRow({ e }: { e: InvestigationEvent }) {
  const action = e.kind === "action" || e.kind === "refused";
  return (
    <li className="flex gap-3 px-3 py-2 text-sm">
      <span className="w-20 shrink-0 font-mono text-xs text-muted" title={new Date(e.time).toLocaleString()}>
        {new Date(e.time).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
      </span>
      <div className="min-w-0 space-y-0.5">
        <p>
          <span className="font-medium">{e.by}</span> <span className="text-muted">{e.kind}</span>
          {action && (
            <span className={e.kind === "refused" ? "text-bad" : ""}>
              {" "}
              {e.action} <span className="font-mono text-xs">{e.target}</span>
              {e.disruptive && <span className={`${badge} ${badgeTone.warn} ml-1`}>disruptive</span>}
            </span>
          )}
        </p>
        {e.text && <p className="whitespace-pre-wrap break-words text-fg/90">{e.text}</p>}
        {e.kind === "action" && <p className="text-xs text-muted">{e.result ? `Result: ${e.result}` : "Running…"}</p>}
      </div>
    </li>
  );
}

/** InvestigationPage is one investigation's timeline, where a person can add a note or close it. */
export function InvestigationPage() {
  const id = Number(useParams().id);
  const me = useMe();
  const client = useQueryClient();
  const inv = useQuery({
    queryKey: ["investigation", id],
    queryFn: ({ signal }) => api.investigation(id, signal),
    refetchInterval: 10_000,
  });
  const [note, setNote] = useState("");
  const [fix, setFix] = useState("");
  const done = () => {
    void client.invalidateQueries({ queryKey: ["investigation", id] });
    void client.invalidateQueries({ queryKey: ["investigations"] });
  };
  const add = useMutation({ mutationFn: () => api.noteInvestigation(id, note.trim()), onSuccess: () => (setNote(""), done()) });
  const close = useMutation({
    mutationFn: (status: "fixed" | "abandoned") => api.closeInvestigation(id, status, fix.trim(), ""),
    onSuccess: done,
  });
  if (inv.error) return <ErrorNote error={inv.error} />;
  const i = inv.data;
  if (!i) return <div className={`${skeleton} h-60`} />;
  const live = i.status === "open" || i.status === "escalated";
  const editor = can(me.data?.role, "editor");
  return (
    <div className="max-w-4xl space-y-6">
      <PageHeader
        title={i.title}
        back={{ to: "/investigations", label: "Investigations" }}
        sub={
          <span className="flex flex-wrap items-center gap-2">
            <span className={`${badge} ${STATUS[i.status].tone}`}>{STATUS[i.status].label}</span>
            {i.kind}
            {i.rule && (
              <Link to={`/rules/${encodeURIComponent(i.rule)}`} className="font-mono text-xs text-accent">
                {i.rule}
              </Link>
            )}
            {claim(i) && <span>· {claim(i)}</span>}
            {i.previous ? (
              <Link to={`/investigations/${i.previous}`} className="text-accent">
                · the last time
              </Link>
            ) : null}
          </span>
        }
      />
      {i.fix && (
        <p className={`${card} px-3 py-2 text-sm`}>
          <span className="font-medium">Fix:</span> {i.fix}
          {i.summary && <span className="block text-muted">{i.summary}</span>}
        </p>
      )}
      <Runs id={i.id} />
      <section className="space-y-2">
        <SectionHeader title="Timeline" />
        <ul className={`divide-y divide-line/60 ${card}`}>
          {(i.events ?? []).map((e) => (
            <EventRow key={e.id} e={e} />
          ))}
        </ul>
      </section>
      {live && editor && (
        <section className="space-y-2">
          <SectionHeader title="Add to it" />
          <form
            className="flex flex-wrap gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (note.trim()) add.mutate();
            }}
          >
            <input
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="A note for the agents and people"
              className={`${field} min-w-0 flex-1`}
            />
            <button type="submit" className={button("secondary")} disabled={!note.trim() || add.isPending}>
              Add note
            </button>
          </form>
          <div className="flex flex-wrap gap-2">
            <input value={fix} onChange={(e) => setFix(e.target.value)} placeholder="What fixed it" className={`${field} min-w-0 flex-1`} />
            <button
              type="button"
              className={button("primary")}
              disabled={!fix.trim() || close.isPending}
              onClick={() => close.mutate("fixed")}
            >
              Fixed
            </button>
            <button type="button" className={button("quiet")} disabled={close.isPending} onClick={() => close.mutate("abandoned")}>
              Abandon
            </button>
          </div>
          {(add.error || close.error) && <ErrorNote error={add.error ?? close.error} />}
        </section>
      )}
    </div>
  );
}

/** AgentsPage is the agents' rails: the pause switch, what they did in the last hour, and their settings. */
export function AgentsPage() {
  const me = useMe();
  const client = useQueryClient();
  const st = useQuery({ queryKey: ["agents"], queryFn: ({ signal }) => api.agents(signal), refetchInterval: 15_000 });
  const [note, setNote] = useState("");
  const toggle = useMutation({
    mutationFn: () => (st.data?.paused.paused ? api.resumeAgents() : api.pauseAgents(note.trim())),
    onSuccess: () => {
      setNote("");
      void client.invalidateQueries({ queryKey: ["agents"] });
    },
  });
  if (st.error) return <ErrorNote error={st.error} />;
  const s = st.data;
  if (!s) return <div className={`${skeleton} h-60`} />;
  const c = s.config;
  const tokens = Object.keys(s.actions);
  return (
    <div className="max-w-4xl space-y-6">
      <PageHeader
        title="Agent controls"
        sub={
          <>
            AI agents working through tremolo with{" "}
            <Link to="/profile" className="text-accent">
              assistant tokens
            </Link>
            .{" "}
            <Link to="/investigations" className="text-accent">
              {s.open} open, {s.escalated} needing a person
            </Link>
          </>
        }
      />
      <section className={`${card} space-y-3 p-4 ${s.paused.paused ? "border-bad/50" : ""}`}>
        <div className="flex flex-wrap items-center gap-3">
          {s.paused.paused ? <CirclePause className="size-6 text-bad" /> : <CirclePlay className="size-6 text-good" />}
          <div>
            <p className="font-semibold">{s.paused.paused ? "Paused" : "Working"}</p>
            <p className="text-sm text-muted">
              {s.paused.paused
                ? `by ${s.paused.by} ${s.paused.at ? `${ago(s.paused.at)} ago` : ""}${s.paused.note ? `: ${s.paused.note}` : ""}. Agents can read but not change anything, and people hear of every alert.`
                : "Pausing stops every agent at once: they can still read, but changes and actions are refused."}
            </p>
          </div>
          {can(me.data?.role, "editor") && (
            <div className="ml-auto flex flex-wrap gap-2">
              {!s.paused.paused && (
                <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="Why (optional)" className={`${field} w-48`} />
              )}
              <button
                type="button"
                className={button(s.paused.paused ? "primary" : "danger")}
                onClick={() => toggle.mutate()}
                disabled={toggle.isPending}
              >
                {s.paused.paused ? "Resume agents" : "Pause agents"}
              </button>
            </div>
          )}
        </div>
        {toggle.error && <ErrorNote error={toggle.error} />}
      </section>

      <section className="space-y-2">
        <SectionHeader title="Rails" />
        <ul className={`${card} space-y-1 p-3 text-sm`}>
          <li>
            {c.first
              ? `Agents first: people hear of an alert if no agent claims it in ${c.claimWait}, it's escalated, or it's still firing after ${c.grace}.`
              : "People hear of alerts as usual; agents work alongside."}
            {c.always?.length ? ` Always at once: ${c.always.join(", ")}.` : ""}
          </li>
          <li>
            Each agent may make {c.maxActionsPerHour} actions an hour, {c.maxDisruptivePerHour} of them disruptive. Hosts asleep, off or in
            maintenance are left alone.
          </li>
          {(c.protect ?? []).map((g) => (
            <li key={g.name}>
              <ShieldAlert className="mr-1 inline size-4 text-muted" />
              {g.name}: at most {g.maxAtOnce} at once ({[...(g.hosts ?? []), ...(g.namespaces ?? [])].join(", ")})
            </li>
          ))}
          <li className="text-muted">Set under agents in the chart. Reports and pauses go to {c.notify?.join(", ") || "no channel"}.</li>
        </ul>
      </section>

      <section className="space-y-2">
        <SectionHeader title="Last hour" />
        {tokens.length ? (
          <ul className={`${card} divide-y divide-line/60`}>
            {tokens.map((t) => (
              <li key={t} className="flex items-center gap-3 px-3 py-2 text-sm">
                <Bot className="size-4 text-muted" />
                <span className="font-medium">{t}</span>
                <span className="text-muted">
                  {s.actions[t]} of {c.maxActionsPerHour} actions, {s.disruptive[t]} of {c.maxDisruptivePerHour} disruptive
                </span>
                <NavLink to={`/activity`} className="ml-auto text-xs text-accent">
                  Activity
                </NavLink>
              </li>
            ))}
          </ul>
        ) : (
          <EmptyState compact>No actions in the last hour.</EmptyState>
        )}
      </section>

      <Coverage />
      <Report canSend={can(me.data?.role, "editor") && (c.notify?.length ?? 0) > 0} />

      <section className="space-y-2">
        <SectionHeader title="Recent actions" />
        {s.recent.length ? (
          <ul className={`${card} divide-y divide-line/60`}>
            {s.recent.map((e) => (
              <li key={e.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 px-3 py-2 text-sm">
                <Hammer className={`size-3.5 ${e.kind === "refused" ? "text-bad" : "text-muted"}`} />
                <span className="font-mono text-xs text-muted">{ago(e.time)} ago</span>
                <span className={e.kind === "refused" ? "text-bad" : ""}>
                  {e.kind === "refused" ? "refused " : `${e.by} `}
                  {e.action} <span className="font-mono text-xs">{e.target}</span>
                </span>
                <Link to={`/investigations/${e.investigation}`} className="text-xs text-accent">
                  {e.title}
                </Link>
                {e.kind === "refused" && <span className="w-full text-xs text-muted">{e.text}</span>}
              </li>
            ))}
          </ul>
        ) : (
          <EmptyState compact>No actions yet.</EmptyState>
        )}
      </section>
    </div>
  );
}

/** Coverage lists gaps in what tremolo observes, for agents and people to close. */
function Coverage() {
  const [open, setOpen] = useState(false);
  const gaps = useQuery({ queryKey: ["coverage"], queryFn: ({ signal }) => api.coverage(signal), enabled: open, staleTime: 60_000 });
  return (
    <section className="space-y-2">
      <SectionHeader
        title="Coverage gaps"
        actions={
          <button type="button" className="text-sm text-accent" onClick={() => setOpen(!open)} aria-expanded={open}>
            {open ? "Hide" : "Check now"}
          </button>
        }
      />
      {!open && (
        <p className="text-sm text-muted">
          Hosts and namespaces that send no logs or metrics, senders that went quiet, rules without runbooks.
        </p>
      )}
      {open && gaps.isLoading && <div className={`${skeleton} h-24`} />}
      {gaps.error && <ErrorNote error={gaps.error} />}
      {gaps.data && (
        <ul className={`${card} divide-y divide-line/60 text-sm`}>
          {gaps.data.map((g, i) => (
            <li key={i} className="flex flex-wrap gap-x-3 px-3 py-2">
              <span className={`${badge} ${g.kind === "check" ? badgeTone.bad : badgeTone.warn}`}>{g.kind}</span>
              <span className="font-medium">{g.subject}</span>
              <span className="text-muted">{g.detail}</span>
            </li>
          ))}
          {!gaps.data.length && <li className="px-3 py-2 text-muted">No gaps found.</li>}
        </ul>
      )}
    </section>
  );
}

/** Report previews the daily report agents' work is summed up in, and sends it. */
function Report({ canSend }: { canSend: boolean }) {
  const [open, setOpen] = useState(false);
  const report = useQuery({ queryKey: ["agents-report"], queryFn: ({ signal }) => api.agentsReport(signal), enabled: open });
  const send = useMutation({ mutationFn: api.sendAgentsReport });
  return (
    <section className="space-y-2">
      <SectionHeader
        title="Daily report"
        actions={
          <div className="flex gap-3">
            <button type="button" className="text-sm text-accent" onClick={() => setOpen(!open)} aria-expanded={open}>
              {open ? "Hide" : "Preview"}
            </button>
            {canSend && (
              <button type="button" className="text-sm text-accent" onClick={() => send.mutate()} disabled={send.isPending}>
                {send.isSuccess ? "Sent" : "Send now"}
              </button>
            )}
          </div>
        }
      />
      {open && report.data && <pre className={`${card} overflow-x-auto p-3 text-xs whitespace-pre-wrap`}>{report.data}</pre>}
      {(report.error || send.error) && <ErrorNote error={report.error ?? send.error} />}
    </section>
  );
}

/** Runs lists the agent runs that worked an investigation, from their traces. */
function Runs({ id }: { id: number }) {
  const version = useVersion();
  const runs = useRuns(30 * 86400, id);
  if (!version.data?.traces || !runs.data?.length) return null;
  return (
    <section className="space-y-2">
      <SectionHeader title="Agent runs" />
      <RunList runs={runs.data} compact />
    </section>
  );
}

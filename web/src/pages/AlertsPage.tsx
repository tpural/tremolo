import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  BellOff,
  Check,
  ChevronDown,
  ChevronRight,
  CircleAlert,
  CircleCheck,
  Clock,
  Forward,
  Hand,
  MessageSquare,
  Play,
  TriangleAlert,
  UserCheck,
} from "lucide-react";
import { useState } from "react";
import { api, can, type Alert, type AlertEvent, type AlertsSnapshot, type Me, type Past, type Rule, type Severity, type TestRun } from "../lib/api";
import { ago, duration } from "../lib/format";
import { useAlerts, useMe } from "../lib/hooks";

const SEVERITY: Record<Severity, { label: string; text: string; bg: string }> = {
  page: { label: "Page", text: "text-bad", bg: "bg-bad/15" },
  warn: { label: "Warn", text: "text-warn", bg: "bg-warn/15" },
  info: { label: "Info", text: "text-accent", bg: "bg-accent/15" },
};

const SILENCE_FOR = [
  { label: "1 hour", hours: 1 },
  { label: "4 hours", hours: 4 },
  { label: "1 day", hours: 24 },
  { label: "1 week", hours: 168 },
];

const TONE: Record<Severity | "none", string> = {
  page: "border-bad/40 bg-bad/5",
  warn: "border-warn/40 bg-warn/5",
  info: "border-line bg-panel",
  none: "border-line bg-panel",
};

/** worst is the loudest severity among alerts nobody has silenced or acknowledged. */
export function worst(alerts: Alert[]): Severity | "none" {
  const live = alerts.filter((a) => !a.silenced && !a.acked);
  return (["page", "warn", "info"] as const).find((s) => live.some((a) => a.severity === s)) ?? "none";
}

/** seconds turns a Go duration (nanoseconds) into seconds. */
const seconds = (ns: number) => ns / 1e9;

/** mine is whether an alert is someone's to deal with: theirs, their team's, or on its way to them. */
export function mine(a: Alert, me: Pick<Me, "name" | "teams"> | undefined) {
  if (!me) return false;
  if (a.assignee) return a.assignee === me.name || me.teams.includes(a.assignee);
  return (a.plan ?? []).some((p) => p.person === me.name);
}

/** useTriage runs an alert action and refreshes the list. */
function useTriage<T>(fn: (v: T) => Promise<unknown>) {
  const client = useQueryClient();
  return useMutation({ mutationFn: fn, onSuccess: () => client.invalidateQueries({ queryKey: ["alerts"] }) });
}

export function AlertsPage() {
  const alerts = useAlerts();
  const me = useMe();
  const snap = alerts.data;
  const [onlyMine, setOnlyMine] = useState(false);
  const all = snap?.alerts ?? [];
  const active = onlyMine ? all.filter((a) => mine(a, me.data)) : all;
  const firing = active.filter((a) => a.state === "firing");
  const rules = snap?.rules ?? [];
  const broken = rules.filter((r) => r.error);
  const loudest = worst(firing);

  if (alerts.isLoading) return <p className="py-16 text-center text-sm text-muted">Loading alerts…</p>;
  if (alerts.error) return <p className="py-16 text-center text-sm text-bad">{String(alerts.error)}</p>;

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Alerts</h1>
        <span className="text-sm text-muted">
          {rules.length} rule{rules.length === 1 ? "" : "s"}, checked in {snap?.timezone}
          {snap?.channels?.length ? ` · notifies ${snap.channels.join(", ")}` : " · no notification channels, so alerts show only here"}
          {snap?.escalations?.length ? ` · escalations ${snap.escalations.join(", ")}` : ""}
        </span>
      </div>

      {snap?.loadError && (
        <pre className="overflow-x-auto rounded-lg border border-bad/40 bg-bad/5 p-3 text-xs text-bad">Some rules didn't load: {snap.loadError}</pre>
      )}

      <section className={`rounded-2xl border p-4 ${TONE[loudest]}`}>
        <div className="flex items-center gap-3">
          {firing.length ? (
            <CircleAlert className={`size-6 ${loudest === "none" ? "text-muted" : SEVERITY[loudest].text}`} />
          ) : (
            <CircleCheck className="size-6 text-good" />
          )}
          <div>
            <p className="font-semibold">{firing.length ? `${firing.length} firing` : "Nothing firing"}</p>
            <p className="text-sm text-muted">
              {[
                active.length - firing.length && `${active.length - firing.length} pending`,
                broken.length && `${broken.length} rule${broken.length === 1 ? "" : "s"} failing`,
              ]
                .filter(Boolean)
                .join(", ") || "Every rule ran cleanly."}
            </p>
          </div>
        </div>
        {all.length > 0 && (
          <div className="mt-3 flex gap-1 text-xs">
            {[
              { label: `All ${all.length}`, mine: false },
              { label: `Mine ${all.filter((a) => mine(a, me.data)).length}`, mine: true },
            ].map((t) => (
              <button
                key={t.label}
                type="button"
                onClick={() => setOnlyMine(t.mine)}
                className={`rounded-md px-2 py-0.5 ${onlyMine === t.mine ? "bg-panel-2 font-medium text-fg" : "text-muted hover:text-fg"}`}
              >
                {t.label}
              </button>
            ))}
          </div>
        )}
        {active.length > 0 && (
          <ul className="mt-1 divide-y divide-line/60">
            {active.map((a) => (
              <AlertRow key={a.id} alert={a} snap={snap!} me={me.data} />
            ))}
          </ul>
        )}
      </section>

      {(snap?.silences?.length ?? 0) > 0 && (
        <section className="space-y-2">
          <h2 className="font-semibold">Silences</h2>
          <ul className="divide-y divide-line/60 rounded-xl border border-line bg-panel">
            {snap?.silences?.map((s) => (
              <li key={s.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm">
                <BellOff className="size-4 text-muted" />
                <span className="font-mono text-xs">
                  {s.rule}
                  {s.key ? ` / ${s.key}` : " (all)"}
                </span>
                <span className="text-muted">until {new Date(s.until).toLocaleString()}</span>
                {s.note && <span className="text-muted">· {s.note}</span>}
                {s.by && <span className="text-muted">· by {s.by}</span>}
                {can(me.data?.role, "editor") && <Unsilence id={s.id} />}
              </li>
            ))}
          </ul>
        </section>
      )}

      <section className="space-y-2">
        <h2 className="font-semibold">Rules</h2>
        <div className="divide-y divide-line/60 rounded-xl border border-line bg-panel">
          {rules.map((r) => (
            <RuleRow key={r.name} rule={r} firing={all.filter((a) => a.rule === r.name).length} canRun={can(me.data?.role, "editor")} />
          ))}
        </div>
        <p className="text-xs text-muted">
          Rules are Starlark (Python-like) files with a <code className="font-mono">check(now)</code> function. Add your own in the rules directory; a
          file named like a built-in replaces it.
        </p>
      </section>

      <History />
    </div>
  );
}

function SeverityBadge({ severity }: { severity: Severity }) {
  const s = SEVERITY[severity] ?? SEVERITY.warn;
  return <span className={`rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide ${s.bg} ${s.text}`}>{s.label}</span>;
}

/** Progress shows where a firing alert has been sent and what comes next. */
function Progress({ alert: a }: { alert: Alert }) {
  const plan = a.plan ?? [];
  if (a.state !== "firing" || !plan.length) return null;
  const started = Date.parse(a.escalating ?? a.firingSince ?? a.since);
  return (
    <span className="flex w-full flex-wrap items-center gap-1.5 pl-1 text-[11px] text-muted">
      {plan.map((p) => {
        const sent = a.sent?.[p.key];
        const due = started + p.after / 1e6;
        const stopped = !sent && (a.acked || a.silenced);
        return (
          <span
            key={p.key}
            className={`inline-flex items-center gap-1 rounded-full border px-1.5 py-0.5 ${sent ? "border-good/40 text-good" : "border-line"} ${stopped ? "line-through opacity-60" : ""}`}
            title={`${p.key}: ${sent ? `sent ${new Date(sent).toLocaleString()}` : `due ${new Date(due).toLocaleTimeString()}`}`}
          >
            {sent ? <Check className="size-3" /> : <Clock className="size-3" />}
            {p.person ? `${p.person} · ${p.channel}` : p.channel}
            {!sent && p.after > 0 && ` +${duration(seconds(p.after))}`}
          </span>
        );
      })}
    </span>
  );
}

function AlertRow({ alert: a, snap, me }: { alert: Alert; snap: AlertsSnapshot; me?: Me }) {
  const [open, setOpen] = useState(false);
  const [forwarding, setForwarding] = useState(false);
  const ack = useTriage(() => api.ack(a.id));
  const take = useTriage(() => api.take(a.id));
  const silence = useTriage((hours: number) => api.silence(a.rule, a.key, new Date(Date.now() + hours * 3600_000).toISOString()));
  const editor = can(me?.role, "editor");
  const labels = Object.entries(a.labels ?? {});
  const error = silence.error ?? ack.error ?? take.error;
  const small = "inline-flex items-center gap-1 rounded-md border border-line px-1.5 py-0.5 text-xs hover:border-accent";
  return (
    <li className={`flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-sm ${a.silenced || a.state === "pending" ? "opacity-70" : ""}`}>
      <button type="button" onClick={() => setOpen(!open)} className="text-muted hover:text-fg" title="Timeline and notes">
        {open ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
      </button>
      <SeverityBadge severity={a.severity} />
      <span className="font-medium">{a.summary}</span>
      <span className="font-mono text-xs text-muted">{a.rule}</span>
      {labels.length > 0 && (
        <span className="text-xs text-muted">
          {labels.map(([k, v]) => (
            <span key={k} className="mr-2">
              <span className="text-tok-key">{k}=</span>
              <span className="text-tok-str">{v}</span>
            </span>
          ))}
        </span>
      )}
      {a.assignee && (
        <span className="inline-flex items-center gap-1 rounded-full bg-accent/10 px-2 py-0.5 text-xs text-accent" title={a.forwardNote}>
          <UserCheck className="size-3" /> {a.assignee === me?.name ? "you" : a.assignee}
          {a.forwardedBy && <span className="text-muted">from {a.forwardedBy}</span>}
        </span>
      )}
      <span className="ml-auto text-xs text-muted">
        {a.state === "pending" ? `pending for ${ago(a.since)}` : `firing for ${ago(a.firingSince)}`}
        {a.silenced ? " · silenced" : ""}
        {a.acked ? ` · acknowledged${a.ackedBy ? ` by ${a.ackedBy}` : ""} ${ago(a.acked)} ago` : ""}
      </span>
      {editor && a.state === "firing" && !a.acked && (
        <button type="button" disabled={ack.isPending} onClick={() => ack.mutate(undefined)} className={small}>
          <Hand className="size-3.5" /> Acknowledge
        </button>
      )}
      {editor && a.state === "firing" && a.assignee !== me?.name && (
        <button
          type="button"
          disabled={take.isPending}
          onClick={() => take.mutate(undefined)}
          className={small}
          title="Acknowledge and assign to you"
        >
          <UserCheck className="size-3.5" /> Take
        </button>
      )}
      {editor && a.state === "firing" && (
        <button type="button" onClick={() => setForwarding(!forwarding)} className={small}>
          <Forward className="size-3.5" /> Forward
        </button>
      )}
      {editor && !a.silenced && (
        <select
          aria-label="Silence"
          value=""
          disabled={silence.isPending}
          onChange={(e) => e.target.value && silence.mutate(Number(e.target.value))}
          className="rounded-md border border-line bg-panel-2 px-1.5 py-0.5 text-xs text-muted"
        >
          <option value="">Silence…</option>
          {SILENCE_FOR.map((s) => (
            <option key={s.hours} value={s.hours}>
              {s.label}
            </option>
          ))}
        </select>
      )}
      {a.forwardNote && (
        <span className="w-full pl-7 text-xs text-muted">
          “{a.forwardNote}” — {a.forwardedBy}
        </span>
      )}
      <Progress alert={a} />
      {forwarding && <ForwardForm alert={a} snap={snap} me={me} done={() => setForwarding(false)} />}
      {open && <Timeline alert={a} canNote={editor} />}
      {error && <span className="w-full text-xs text-bad">{error.message}</span>}
    </li>
  );
}

/** ForwardForm hands an alert to a person or team, who are told at once. */
function ForwardForm({ alert: a, snap, me, done }: { alert: Alert; snap: AlertsSnapshot; me?: Me; done: () => void }) {
  const [to, setTo] = useState("");
  const [note, setNote] = useState("");
  const forward = useTriage(() => api.forward(a.id, to, note));
  const people = (snap.people ?? []).filter((p) => p !== me?.name);
  return (
    <form
      className="flex w-full flex-wrap items-center gap-2 rounded-lg border border-accent/40 bg-accent/5 p-2 text-xs"
      onSubmit={(e) => {
        e.preventDefault();
        forward.mutate(undefined, { onSuccess: done });
      }}
    >
      <Forward className="size-4 text-accent" />
      <select
        value={to}
        onChange={(e) => setTo(e.target.value)}
        className="rounded-md border border-line bg-panel px-1.5 py-1"
        aria-label="Forward to"
      >
        <option value="">Forward to…</option>
        {(snap.teams ?? []).length > 0 && (
          <optgroup label="Teams">
            {snap.teams.map((t) => (
              <option key={t}>{t}</option>
            ))}
          </optgroup>
        )}
        <optgroup label="People">
          {people.map((p) => (
            <option key={p}>{p}</option>
          ))}
        </optgroup>
      </select>
      <input
        value={note}
        onChange={(e) => setNote(e.target.value)}
        maxLength={2000}
        placeholder="Note for them (optional)"
        className="min-w-48 flex-1 rounded-md border border-line bg-panel px-2 py-1"
      />
      <button
        type="submit"
        disabled={!to || forward.isPending}
        className="rounded-md bg-accent px-2.5 py-1 font-medium text-white disabled:opacity-60 dark:text-bg"
      >
        {forward.isPending ? "Forwarding…" : "Forward"}
      </button>
      <button type="button" onClick={done} className="text-muted">
        Cancel
      </button>
      <span className="w-full text-muted">They're told at once, and it escalates on their contacts until someone acknowledges it.</span>
      {forward.error && <span className="w-full text-bad">{forward.error.message}</span>}
    </form>
  );
}

const EVENT: Record<AlertEvent["kind"], (e: AlertEvent) => string> = {
  firing: () => "Started firing",
  notified: (e) => `Notified ${e.to}`,
  failed: (e) => `Couldn't notify ${e.to}: ${e.note}`,
  acknowledged: (e) => `Acknowledged${e.by ? ` by ${e.by}` : ""}`,
  forwarded: (e) => `${e.by || "Someone"} forwarded it to ${e.to}`,
  taken: (e) => `${e.by} took it`,
  note: (e) => `${e.by}: ${e.note}`,
  resolved: () => "Resolved",
};

/** when is a time of day for today, and a date and time before that. */
export function when(iso: string, now = new Date()) {
  const t = new Date(iso);
  return t.toDateString() === now.toDateString() ? t.toLocaleTimeString() : t.toLocaleString();
}

/** Timeline lists what happened to an alert, newest last, with a box for notes. */
function Timeline({ alert: a, canNote }: { alert: Alert; canNote: boolean }) {
  const [text, setText] = useState("");
  const note = useTriage(() => api.note(a.id, text));
  return (
    <div className="w-full space-y-2 pl-7">
      <ol className="space-y-1 border-l border-line pl-3 text-xs">
        {(a.timeline ?? []).map((e, i) => (
          <li key={i} className={e.kind === "failed" ? "text-bad" : e.kind === "note" ? "" : "text-muted"}>
            <span className="mr-2 font-mono text-muted">{when(e.time)}</span>
            {EVENT[e.kind]?.(e) ?? e.kind}
            {e.kind === "forwarded" && e.note && <span className="text-fg"> — “{e.note}”</span>}
          </li>
        ))}
        {!a.timeline?.length && <li className="text-muted">Nothing yet; it's pending.</li>}
      </ol>
      {canNote && (
        <form
          className="flex items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            note.mutate(undefined, { onSuccess: () => setText("") });
          }}
        >
          <MessageSquare className="size-3.5 text-muted" />
          <input
            value={text}
            onChange={(e) => setText(e.target.value)}
            maxLength={2000}
            placeholder="Add a note"
            className="flex-1 rounded-md border border-line bg-panel px-2 py-1 text-xs"
          />
          <button type="submit" disabled={!text.trim() || note.isPending} className="text-xs text-accent disabled:opacity-50">
            Add
          </button>
        </form>
      )}
    </div>
  );
}

function Unsilence({ id }: { id: string }) {
  const client = useQueryClient();
  const end = useMutation({ mutationFn: () => api.unsilence(id), onSuccess: () => client.invalidateQueries({ queryKey: ["alerts"] }) });
  return (
    <button type="button" className="ml-auto text-xs text-accent" disabled={end.isPending} onClick={() => end.mutate()}>
      End silence
    </button>
  );
}

function RuleRow({ rule: r, firing, canRun }: { rule: Rule; firing: number; canRun: boolean }) {
  const [open, setOpen] = useState(false);
  const [run, setRun] = useState<TestRun | null>(null);
  const test = useMutation({ mutationFn: () => api.testRule(r.name), onSuccess: setRun });
  return (
    <div>
      <button type="button" className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-panel-2" onClick={() => setOpen(!open)}>
        {open ? <ChevronDown className="size-4 text-muted" /> : <ChevronRight className="size-4 text-muted" />}
        <span className="font-mono font-medium">{r.name}</span>
        <span className="rounded border border-line px-1 text-[10px] uppercase tracking-wide text-muted">{r.builtin ? "built-in" : "custom"}</span>
        {firing > 0 && <span className="text-xs text-bad">{firing} active</span>}
        {r.error ? (
          <span className="inline-flex items-center gap-1 text-xs text-bad">
            <CircleAlert className="size-3.5" /> failing
          </span>
        ) : r.warning ? (
          <span className="inline-flex items-center gap-1 text-xs text-warn" title={r.warning}>
            <TriangleAlert className="size-3.5" /> {r.warning}
          </span>
        ) : null}
        <span className="ml-auto text-xs text-muted">
          every {duration(seconds(r.every))}
          {r.for ? ` · for ${duration(seconds(r.for))}` : ""}
          {r.lastRun ? ` · ran ${ago(r.lastRun)} ago in ${duration(seconds(r.took))}` : " · not run yet"}
        </span>
      </button>
      {open && (
        <div className="space-y-3 px-3 pb-3">
          {r.error && <pre className="overflow-x-auto rounded-md bg-bad/10 p-2 text-xs text-bad">{r.error}</pre>}
          <pre className="max-h-96 overflow-auto rounded-md border border-line bg-panel-2 p-3 font-mono text-xs leading-5">{r.source}</pre>
          <div className={`flex items-center gap-3 ${canRun ? "" : "hidden"}`}>
            <button
              type="button"
              disabled={test.isPending}
              onClick={() => test.mutate()}
              className="inline-flex items-center gap-1.5 rounded-md bg-accent px-2.5 py-1 text-xs font-medium text-white disabled:opacity-60"
            >
              <Play className="size-3.5" /> {test.isPending ? "Running…" : "Run now"}
            </button>
            <span className="text-xs text-muted">Runs the rule once against live data. It changes no alerts and sends nothing.</span>
          </div>
          {test.error && <p className="text-xs text-bad">{String(test.error)}</p>}
          {run && <TestResult run={run} />}
        </div>
      )}
    </div>
  );
}

function TestResult({ run }: { run: TestRun }) {
  const found = run.found ?? [];
  return (
    <div className="space-y-2 rounded-md border border-line p-3 text-xs">
      <p className="text-muted">
        Took {duration(seconds(run.took))}.{" "}
        {run.error
          ? "It failed:"
          : found.length
            ? `It found ${found.length} alert${found.length === 1 ? "" : "s"}:`
            : "It found nothing to alert on."}
      </p>
      {run.error && <pre className="overflow-x-auto text-bad">{run.error}</pre>}
      {found.map((f) => (
        <div key={f.key} className="flex flex-wrap items-center gap-2">
          <SeverityBadge severity={f.severity} />
          <span>{f.summary}</span>
          {f.key && <span className="font-mono text-muted">key={f.key}</span>}
          {f.notify?.length ? <span className="text-muted">→ {f.notify.join(", ")}</span> : null}
        </div>
      ))}
      {(run.output?.length ?? 0) > 0 && (
        <pre className="max-h-48 overflow-auto border-t border-line pt-2 font-mono text-muted">{run.output?.join("\n")}</pre>
      )}
    </div>
  );
}

/** History lists alerts that fired and resolved, newest first, with what happened to each. */
function History() {
  const [q, setQ] = useState("");
  const [search, setSearch] = useState("");
  const past = useInfiniteQuery({
    queryKey: ["alert-history", search],
    queryFn: ({ pageParam, signal }) => api.history({ q: search, before: pageParam }, signal),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (last) => (last.length === 50 ? last[last.length - 1].entry : undefined),
  });
  const rows = past.data?.pages.flat() ?? [];
  return (
    <section className="space-y-2">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="font-semibold">History</h2>
        <form
          className="ml-auto"
          onSubmit={(e) => {
            e.preventDefault();
            setSearch(q.trim());
          }}
        >
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search resolved alerts"
            className="w-56 rounded-md border border-line bg-panel px-2 py-1 text-sm"
          />
        </form>
      </div>
      <div className="rounded-xl border border-line bg-panel">
        {rows.length ? (
          <ul className="divide-y divide-line/60">
            {rows.map((a) => (
              <PastRow key={a.entry} alert={a} />
            ))}
          </ul>
        ) : (
          <p className="py-6 text-center text-sm text-muted">
            {past.isLoading ? "Loading…" : past.error ? String(past.error) : search ? "Nothing matches." : "No alert has resolved yet."}
          </p>
        )}
      </div>
      {past.hasNextPage && (
        <button type="button" onClick={() => past.fetchNextPage()} disabled={past.isFetchingNextPage} className="text-sm text-accent">
          {past.isFetchingNextPage ? "Loading…" : "Older"}
        </button>
      )}
    </section>
  );
}

function PastRow({ alert: a }: { alert: Past }) {
  const [open, setOpen] = useState(false);
  const fired = Date.parse(a.firingSince ?? a.since);
  const lasted = (Date.parse(a.resolved) - fired) / 1000;
  const toAck = a.acked ? (Date.parse(a.acked) - fired) / 1000 : undefined;
  return (
    <li className="px-3 py-1.5 text-sm">
      <button type="button" onClick={() => setOpen(!open)} className="flex w-full flex-wrap items-center gap-x-3 gap-y-1 text-left">
        {open ? <ChevronDown className="size-4 text-muted" /> : <ChevronRight className="size-4 text-muted" />}
        <span className="w-44 shrink-0 font-mono text-xs text-muted">{new Date(fired).toLocaleString()}</span>
        <SeverityBadge severity={a.severity} />
        <span>{a.summary}</span>
        <span className="ml-auto flex flex-wrap gap-x-3 text-xs text-muted">
          <span>lasted {duration(Math.max(0, lasted))}</span>
          {toAck !== undefined ? (
            <span>
              acknowledged in {duration(Math.max(0, toAck))}
              {a.ackedBy ? ` by ${a.ackedBy}` : ""}
            </span>
          ) : (
            <span>not acknowledged</span>
          )}
          {a.assignee && <span>· {a.assignee}</span>}
          <span className="font-mono">{a.rule}</span>
        </span>
      </button>
      {open && (
        <div className="mt-2">
          <Timeline alert={a} canNote={false} />
        </div>
      )}
    </li>
  );
}

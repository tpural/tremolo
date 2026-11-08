import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BellOff, Check, ChevronDown, ChevronRight, CircleAlert, CircleCheck, Clock, Hand, Play, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { useSearchParams } from "react-router";
import { api, type Alert, type Rule, type Severity, type TestRun } from "../lib/api";
import { ago, duration } from "../lib/format";
import { useAlerts } from "../lib/hooks";

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

const NAME_KEY = "tremolo-ack-name";

/** readName is the name acknowledgements are signed with, remembered on this device. */
function readName() {
  try {
    return localStorage.getItem(NAME_KEY) ?? "";
  } catch {
    return "";
  }
}

function saveName(name: string) {
  try {
    localStorage.setItem(NAME_KEY, name);
  } catch {
    // private mode: not remembered
  }
}

/** useAck acknowledges an alert by id or link token and refreshes the list. */
function useAck() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, by }: { id: string; by: string }) => api.ack(id, by),
    onSuccess: () => client.invalidateQueries({ queryKey: ["alerts"] }),
  });
}

export function AlertsPage() {
  const alerts = useAlerts();
  const snap = alerts.data;
  const active = snap?.alerts ?? [];
  const firing = active.filter((a) => a.state === "firing");
  const rules = snap?.rules ?? [];
  const broken = rules.filter((r) => r.error);
  const loudest = worst(firing);
  const [params, setParams] = useSearchParams();
  const ackToken = params.get("ack");

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

      {ackToken && (
        <AckLink
          token={ackToken}
          alert={active.find((a) => a.ackToken === ackToken)}
          done={() => {
            params.delete("ack");
            setParams(params, { replace: true });
          }}
        />
      )}

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
        {active.length > 0 && (
          <ul className="mt-3 divide-y divide-line/60">
            {active.map((a) => (
              <AlertRow key={a.id} alert={a} />
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
                <Unsilence id={s.id} />
              </li>
            ))}
          </ul>
        </section>
      )}

      <section className="space-y-2">
        <h2 className="font-semibold">Rules</h2>
        <div className="divide-y divide-line/60 rounded-xl border border-line bg-panel">
          {rules.map((r) => (
            <RuleRow key={r.name} rule={r} firing={active.filter((a) => a.rule === r.name).length} />
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

/** AckLink confirms an acknowledgement opened from a notification's link, so a link preview can't acknowledge by itself. */
function AckLink({ token, alert: a, done }: { token: string; alert?: Alert; done: () => void }) {
  const [name, setName] = useState(readName);
  const ack = useAck();
  if (!a) {
    return (
      <p className="rounded-xl border border-line bg-panel p-3 text-sm text-muted">
        That alert has resolved, or the link is old.{" "}
        <button type="button" className="text-accent" onClick={done}>
          Dismiss
        </button>
      </p>
    );
  }
  if (a.acked) {
    return (
      <p className="flex items-center gap-2 rounded-xl border border-good/40 bg-good/5 p-3 text-sm">
        <Check className="size-4 text-good" /> Acknowledged{a.ackedBy ? ` by ${a.ackedBy}` : ""} {ago(a.acked)} ago: {a.summary}
        <button type="button" className="ml-auto text-xs text-accent" onClick={done}>
          Dismiss
        </button>
      </p>
    );
  }
  return (
    <form
      className="flex flex-wrap items-center gap-3 rounded-xl border border-accent/50 bg-accent/5 p-3 text-sm"
      onSubmit={(e) => {
        e.preventDefault();
        saveName(name.trim());
        ack.mutate({ id: token, by: name.trim() });
      }}
    >
      <Hand className="size-5 text-accent" />
      <span>
        Acknowledge <span className="font-medium">{a.summary}</span>? Escalation and repeats stop until it resolves.
      </span>
      <input
        value={name}
        onChange={(e) => setName(e.target.value)}
        maxLength={100}
        placeholder="Your name (optional)"
        className="w-44 rounded-md border border-line bg-panel px-2 py-1 text-xs"
      />
      <button type="submit" disabled={ack.isPending} className="rounded-md bg-accent px-3 py-1 text-xs font-medium text-white disabled:opacity-60">
        {ack.isPending ? "Acknowledging…" : "Acknowledge"}
      </button>
      {ack.error && <span className="w-full text-xs text-bad">{String(ack.error)}</span>}
    </form>
  );
}

/** Progress shows where a firing alert has been sent and what comes next. */
function Progress({ alert: a }: { alert: Alert }) {
  const plan = a.plan ?? [];
  if (a.state !== "firing" || !plan.length) return null;
  const started = Date.parse(a.firingSince ?? a.since);
  return (
    <span className="flex w-full flex-wrap items-center gap-1.5 pl-1 text-[11px] text-muted">
      {plan.map((p) => {
        const sent = a.sent?.[p.channel];
        const due = started + p.after / 1e6;
        const stopped = !sent && (a.acked || a.silenced);
        return (
          <span
            key={p.channel}
            className={`inline-flex items-center gap-1 rounded-full border px-1.5 py-0.5 ${sent ? "border-good/40 text-good" : "border-line"} ${stopped ? "line-through opacity-60" : ""}`}
            title={sent ? `Sent ${new Date(sent).toLocaleString()}` : `Due ${new Date(due).toLocaleTimeString()}`}
          >
            {sent ? <Check className="size-3" /> : <Clock className="size-3" />}
            {p.channel}
            {!sent && p.after > 0 && ` +${duration(seconds(p.after))}`}
          </span>
        );
      })}
    </span>
  );
}

function AlertRow({ alert: a }: { alert: Alert }) {
  const client = useQueryClient();
  const ack = useAck();
  const silence = useMutation({
    mutationFn: (hours: number) => api.silence(a.rule, a.key, new Date(Date.now() + hours * 3600_000).toISOString()),
    onSuccess: () => client.invalidateQueries({ queryKey: ["alerts"] }),
  });
  const labels = Object.entries(a.labels ?? {});
  return (
    <li className={`flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-sm ${a.silenced || a.state === "pending" ? "opacity-70" : ""}`}>
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
      <span className="ml-auto text-xs text-muted">
        {a.state === "pending" ? `pending for ${ago(a.since)}` : `firing for ${ago(a.firingSince)}`}
        {a.silenced ? " · silenced" : ""}
        {a.acked ? ` · acknowledged${a.ackedBy ? ` by ${a.ackedBy}` : ""} ${ago(a.acked)} ago` : ""}
      </span>
      {a.state === "firing" && !a.acked && (
        <button
          type="button"
          disabled={ack.isPending}
          onClick={() => ack.mutate({ id: a.id, by: readName() })}
          className="inline-flex items-center gap-1 rounded-md border border-line px-1.5 py-0.5 text-xs hover:border-accent"
        >
          <Hand className="size-3.5" /> Acknowledge
        </button>
      )}
      {!a.silenced && (
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
      <Progress alert={a} />
      {(silence.error ?? ack.error) && <span className="w-full text-xs text-bad">{String(silence.error ?? ack.error)}</span>}
    </li>
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

function RuleRow({ rule: r, firing }: { rule: Rule; firing: number }) {
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
          <div className="flex items-center gap-3">
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

/** History lists the last week's firing, acknowledged and resolved events, which tremolo writes to its logs. */
function History() {
  const [now] = useState(() => Math.floor(Date.now() / 1000));
  const events = useQuery({
    queryKey: ["alert-history", now],
    queryFn: ({ signal }) => api.logs(`stream:="tremolo-alerts"`, now - 7 * 86400, now + 60, 100, signal),
  });
  const rows = events.data ?? [];
  return (
    <section className="space-y-2">
      <h2 className="font-semibold">Last 7 days</h2>
      <div className="rounded-xl border border-line bg-panel">
        {rows.length ? (
          <ul className="divide-y divide-line/60">
            {rows.map((e, i) => (
              <li key={i} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-1.5 text-sm">
                <span className="w-36 shrink-0 font-mono text-xs text-muted">{new Date(e._time).toLocaleString()}</span>
                <span
                  className={`w-24 text-xs font-medium ${e.event === "resolved" ? "text-good" : e.event === "acknowledged" ? "text-accent" : "text-bad"}`}
                >
                  {e.event}
                </span>
                <SeverityBadge severity={(e.severity as Severity) ?? "warn"} />
                <span>{e._msg}</span>
                <span className="ml-auto font-mono text-xs text-muted">{e.rule}</span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="py-6 text-center text-sm text-muted">{events.isLoading ? "Loading…" : "No alerts fired in the last week."}</p>
        )}
      </div>
    </section>
  );
}

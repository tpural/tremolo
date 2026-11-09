import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Copy, Download, History, Play, Save, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { api, can, type Replay, type Rule, type RuleVersion } from "../lib/api";
import { ago, duration } from "../lib/format";
import { useAlerts, useMe } from "../lib/hooks";
import { SeverityBadge, seconds } from "./AlertsPage";

/** STARTER is a new rule's source: a small, working example to edit. */
export const STARTER = `# Alert when the API serves errors.
EVERY = "1m"   # how often it runs
FOR = "5m"     # how long it must hold before it fires

def check(now):
    rate = metric('sum(rate(http_requests_total{code=~"5.."}[5m]))')
    if rate != None and rate > 1:
        return alert("API errors at %.1f a second" % rate, severity = "warn")
`;

const RANGES = [
  { label: "1 hour", hours: 1 },
  { label: "6 hours", hours: 6 },
  { label: "1 day", hours: 24 },
  { label: "3 days", hours: 72 },
  { label: "7 days", hours: 168 },
];

const ORIGIN: Record<Rule["origin"], string> = {
  builtin: "Built in to tremolo. Save a copy here to change it; your copy replaces the built-in.",
  file: "Comes from a file in the rules directory, so it's changed there. Copy it to make a new rule here.",
  editor: "Written here. Every save is kept as a version.",
};

/** validName matches what the server accepts for an editor rule's name. */
export function validName(name: string) {
  return /^[a-z0-9][a-z0-9_-]{0,63}$/.test(name);
}

/** RulePage edits a rule, replays it against the past, and keeps its versions. */
export function RulePage() {
  const { name = "new" } = useParams();
  const [params] = useSearchParams();
  const isNew = name === "new";
  const alerts = useAlerts();
  const me = useMe();
  const navigate = useNavigate();
  const client = useQueryClient();
  const rule = alerts.data?.rules?.find((r) => r.name === name);
  const copyOf = alerts.data?.rules?.find((r) => r.name === params.get("from"));
  const editable = can(me.data?.role, "editor") && (isNew || rule?.origin !== "file");

  const [newName, setNewName] = useState(copyOf ? `${copyOf.name}${copyOf.origin === "builtin" ? "" : "-copy"}` : "");
  const [source, setSource] = useState<string | null>(null);
  const [note, setNote] = useState("");
  const [hours, setHours] = useState(24);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const saved = isNew ? (copyOf?.source ?? STARTER) : (rule?.source ?? "");
  const draft = source ?? saved;
  const changed = draft !== saved;
  const target = isNew ? newName : name;

  // Leave the draft alone when the saved rule changes under it, but start
  // from the saved source when moving to another rule.
  useEffect(() => setSource(null), [name]);

  const versions = useQuery({
    queryKey: ["rule-versions", name],
    queryFn: ({ signal }) => api.ruleVersions(name, signal),
    enabled: !isNew && rule?.origin === "editor",
  });
  const replay = useMutation({
    mutationFn: async (h: number) => {
      const draftRun = await api.replay(draft, h, target);
      // With an existing rule, replay what's saved too, to compare.
      const savedRun = !isNew && changed && h > 0 ? await api.replay(saved, h, target) : undefined;
      return { draft: draftRun, saved: savedRun };
    },
  });
  const save = useMutation({
    mutationFn: () => api.saveRule(target, draft, note),
    onSuccess: async () => {
      setNote("");
      await client.invalidateQueries({ queryKey: ["alerts"] });
      await client.invalidateQueries({ queryKey: ["rule-versions", target] });
      if (isNew) navigate(`/alerts/rules/${target}`, { replace: true });
      else setSource(null);
    },
  });
  const remove = useMutation({
    mutationFn: () => api.deleteRule(name),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["alerts"] });
      navigate("/alerts");
    },
  });

  if (alerts.isLoading) return <p className="py-16 text-center text-sm text-muted">Loading…</p>;
  if (!isNew && !rule) {
    return (
      <p className="py-16 text-center text-sm text-muted">
        No rule called {name}.{" "}
        <Link to="/alerts" className="text-accent">
          Back to alerts
        </Link>
      </p>
    );
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-3">
        <Link to="/alerts" className="text-muted hover:text-fg" title="Back to alerts">
          <ArrowLeft className="size-5" />
        </Link>
        {isNew ? (
          <input
            value={newName}
            onChange={(e) => setNewName(e.target.value.toLowerCase())}
            placeholder="rule-name"
            autoFocus
            className="rounded-md border border-line bg-panel px-2 py-1 font-mono text-lg"
          />
        ) : (
          <h1 className="font-mono text-2xl font-semibold tracking-tight">{name}</h1>
        )}
        {rule && (
          <span className="rounded border border-line px-1.5 text-[10px] uppercase tracking-wide text-muted">
            {rule.origin === "editor" ? `version ${rule.version}` : rule.origin === "builtin" ? "built-in" : "file"}
          </span>
        )}
        <div className="ml-auto flex items-center gap-3 text-sm">
          {!isNew && rule?.origin !== "editor" && can(me.data?.role, "editor") && (
            <Link to={`/alerts/rules/new?from=${encodeURIComponent(name)}`} className="inline-flex items-center gap-1 text-accent">
              <Copy className="size-4" /> {rule?.origin === "builtin" ? "Edit a copy" : "Copy"}
            </Link>
          )}
          <button type="button" onClick={() => download(target || "rule", draft)} className="inline-flex items-center gap-1 text-muted hover:text-fg">
            <Download className="size-4" /> Export .star
          </button>
          {rule?.origin === "editor" && editable && (
            <button
              type="button"
              onClick={() => (confirmDelete ? remove.mutate() : setConfirmDelete(true))}
              className={`inline-flex items-center gap-1 ${confirmDelete ? "text-bad" : "text-muted hover:text-bad"}`}
            >
              <Trash2 className="size-4" /> {confirmDelete ? "Delete it and its versions?" : "Delete"}
            </button>
          )}
        </div>
      </div>

      <p className="text-sm text-muted">
        {isNew
          ? copyOf?.origin === "builtin" && newName === copyOf.name
            ? "Saving under the built-in's name replaces it; delete your copy to go back."
            : "Write a check(now) function that returns alert(...) for each problem, or nothing when all is well."
          : ORIGIN[rule!.origin]}
        {isNew && newName && !validName(newName) && <span className="text-bad"> Names are lowercase letters, digits, dashes and underscores.</span>}
      </p>

      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_18rem]">
        <div className="min-w-0 space-y-3">
          <Editor value={draft} onChange={setSource} readOnly={!editable} />
          {rule?.error && <pre className="overflow-x-auto rounded-md bg-bad/10 p-2 text-xs text-bad">Last run failed: {rule.error}</pre>}

          <div className="flex flex-wrap items-center gap-2 text-sm">
            <button
              type="button"
              disabled={replay.isPending || !can(me.data?.role, "editor")}
              onClick={() => replay.mutate(hours)}
              className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1 font-medium text-white disabled:opacity-60 dark:text-bg"
            >
              <History className="size-4" /> {replay.isPending ? "Replaying…" : "Replay"}
            </button>
            <select value={hours} onChange={(e) => setHours(Number(e.target.value))} className="rounded-md border border-line bg-panel px-2 py-1">
              {RANGES.map((r) => (
                <option key={r.hours} value={r.hours}>
                  the last {r.label}
                </option>
              ))}
            </select>
            <button
              type="button"
              disabled={replay.isPending || !can(me.data?.role, "editor")}
              onClick={() => replay.mutate(0)}
              className="inline-flex items-center gap-1.5 rounded-md border border-line px-3 py-1 disabled:opacity-60"
            >
              <Play className="size-4" /> Run once now
            </button>
            <span className="text-xs text-muted">Runs the draft against past data. Nothing is saved or sent.</span>
          </div>
          {replay.error && <pre className="overflow-x-auto rounded-md bg-bad/10 p-2 text-xs text-bad">{replay.error.message}</pre>}
          {replay.data && <ReplayResult run={replay.data.draft} saved={replay.data.saved} />}

          {editable && (
            <form
              className="flex flex-wrap items-center gap-2 border-t border-line pt-3"
              onSubmit={(e) => {
                e.preventDefault();
                save.mutate();
              }}
            >
              <input
                value={note}
                onChange={(e) => setNote(e.target.value)}
                maxLength={500}
                placeholder="What changed (optional)"
                className="min-w-0 flex-1 rounded-md border border-line bg-panel px-2 py-1 text-sm"
              />
              <button
                type="submit"
                disabled={save.isPending || (!isNew && !changed) || !validName(target)}
                className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1 text-sm font-medium text-white disabled:opacity-60 dark:text-bg"
              >
                <Save className="size-4" /> {save.isPending ? "Saving…" : isNew ? "Save rule" : "Save version"}
              </button>
              {changed && !isNew && (
                <button type="button" onClick={() => setSource(null)} className="text-sm text-muted hover:text-fg">
                  Discard changes
                </button>
              )}
              {save.error && <pre className="w-full overflow-x-auto rounded-md bg-bad/10 p-2 text-xs text-bad">{save.error.message}</pre>}
            </form>
          )}
        </div>

        <aside className="space-y-3">
          {rule?.origin === "editor" && (
            <Versions name={name} versions={versions.data ?? []} current={rule.version} onLoad={(src) => setSource(src)} canEdit={editable} />
          )}
          <Help />
        </aside>
      </div>
    </div>
  );
}

/** Editor is a plain code box: Tab indents with four spaces. */
function Editor({ value, onChange, readOnly }: { value: string; onChange: (v: string) => void; readOnly: boolean }) {
  const lines = value.split("\n").length;
  return (
    <textarea
      value={value}
      readOnly={readOnly}
      spellCheck={false}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={(e) => {
        if (e.key !== "Tab" || readOnly || e.shiftKey) return;
        e.preventDefault();
        const t = e.currentTarget;
        const { selectionStart: s, selectionEnd: end } = t;
        onChange(value.slice(0, s) + "    " + value.slice(end));
        requestAnimationFrame(() => t.setSelectionRange(s + 4, s + 4));
      }}
      rows={Math.min(40, Math.max(14, lines + 2))}
      className="block w-full resize-y rounded-xl border border-line bg-panel-2 p-3 font-mono text-xs leading-5 read-only:opacity-80"
      aria-label="Rule source"
    />
  );
}

/** summarize counts a replay's alerts: how many fired, and how many were still firing at the end. */
export function summarize(run: Replay) {
  const still = run.episodes.filter((e) => !e.resolved).length;
  const keys = new Set(run.episodes.map((e) => e.key)).size;
  return { fired: run.episodes.length, still, keys };
}

function ReplayResult({ run, saved }: { run: Replay; saved?: Replay }) {
  const once = run.runs === 1;
  const s = summarize(run);
  const max = Math.max(1, ...run.firing);
  return (
    <div className="space-y-3 rounded-xl border border-line bg-panel p-3 text-sm">
      {once ? (
        <p className="text-muted">
          Ran once in {duration(seconds(run.took))}:{" "}
          {run.errors
            ? "it failed."
            : run.found.length
              ? `it found ${run.found.length} alert${run.found.length === 1 ? "" : "s"}.`
              : "it found nothing to alert on."}
        </p>
      ) : (
        <>
          <p>
            <span className="font-semibold">{s.fired ? `Would have fired ${s.fired} time${s.fired === 1 ? "" : "s"}` : "Would not have fired"}</span>
            <span className="text-muted">
              {s.fired > 0 && s.keys > 1 ? ` across ${s.keys} keys` : ""}
              {s.still ? `, ${s.still} still firing at the end` : ""} · {run.runs} runs, every {duration(seconds(run.step))} · took{" "}
              {duration(seconds(run.took))}
            </span>
          </p>
          {saved && (
            <p className="text-xs text-muted">
              The saved version would have fired {summarize(saved).fired} time{summarize(saved).fired === 1 ? "" : "s"} over the same stretch
              {saved.errors ? `, with ${saved.errors} failed runs` : ""}.
            </p>
          )}
          <div className="flex h-10 items-end gap-px" title="Alerts firing at each run">
            {run.firing.map((n, i) => (
              <div
                key={i}
                className={`flex-1 rounded-t-sm ${n ? "bg-bad/70" : "bg-line/60"}`}
                style={{ height: n ? `${20 + (80 * n) / max}%` : "8%" }}
              />
            ))}
          </div>
          <div className="flex justify-between text-[11px] text-muted">
            <span>{new Date(run.from).toLocaleString()}</span>
            <span>now</span>
          </div>
        </>
      )}
      {run.warnings?.map((w) => (
        <p key={w} className="text-xs text-warn">
          {w}
        </p>
      ))}
      {run.errors > 0 && (
        <pre className="overflow-x-auto rounded-md bg-bad/10 p-2 text-xs text-bad">
          {once ? "" : `${run.errors} of ${run.runs} runs failed. First: `}
          {run.firstError}
        </pre>
      )}
      {!once && run.episodes.length > 0 && (
        <table className="w-full text-xs">
          <thead className="text-left text-muted">
            <tr>
              <th className="py-1 font-normal">Fired</th>
              <th className="font-normal">For</th>
              <th className="font-normal">Alert</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line/60">
            {run.episodes.slice(0, 100).map((e, i) => (
              <tr key={i}>
                <td className="py-1 pr-3 font-mono whitespace-nowrap">{new Date(e.fired).toLocaleString()}</td>
                <td className="pr-3 whitespace-nowrap text-muted">
                  {e.resolved ? duration((Date.parse(e.resolved) - Date.parse(e.fired)) / 1000) : "still firing"}
                </td>
                <td>
                  <span className="inline-flex flex-wrap items-center gap-2">
                    <SeverityBadge severity={e.severity} />
                    {e.summary}
                    {e.key && <span className="font-mono text-muted">key={e.key}</span>}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {once &&
        run.found.map((f) => (
          <div key={f.key} className="flex flex-wrap items-center gap-2 text-xs">
            <SeverityBadge severity={f.severity} />
            <span>{f.summary}</span>
            {f.key && <span className="font-mono text-muted">key={f.key}</span>}
            {f.notify?.length ? <span className="text-muted">→ {f.notify.join(", ")}</span> : null}
          </div>
        ))}
      {run.output.length > 0 && (
        <details className="text-xs">
          <summary className="cursor-pointer text-muted">print() output{once ? "" : " from the last run"}</summary>
          <pre className="mt-1 max-h-48 overflow-auto font-mono text-muted">{run.output.join("\n")}</pre>
        </details>
      )}
    </div>
  );
}

function Versions({
  name,
  versions,
  current,
  onLoad,
  canEdit,
}: {
  name: string;
  versions: RuleVersion[];
  current?: number;
  onLoad: (source: string) => void;
  canEdit: boolean;
}) {
  const load = useMutation({ mutationFn: (v: number) => api.ruleVersion(name, v), onSuccess: (v) => onLoad(v.source ?? "") });
  return (
    <section className="rounded-xl border border-line bg-panel">
      <h2 className="border-b border-line px-3 py-2 text-sm font-semibold">Versions</h2>
      <ul className="max-h-96 divide-y divide-line/60 overflow-auto">
        {versions.map((v) => (
          <li key={v.version} className="space-y-0.5 px-3 py-2 text-xs">
            <div className="flex items-center gap-2">
              <span className="font-mono font-medium">v{v.version}</span>
              <span className="text-muted">
                {v.author || "someone"}, {ago(v.created)} ago
              </span>
              {v.version === current ? (
                <span className="ml-auto text-good">running</span>
              ) : (
                canEdit && (
                  <button
                    type="button"
                    onClick={() => load.mutate(v.version)}
                    className="ml-auto text-accent"
                    title="Put this version in the editor; save to restore it"
                  >
                    Load
                  </button>
                )
              )}
            </div>
            {v.note && <p className="text-muted">{v.note}</p>}
          </li>
        ))}
      </ul>
    </section>
  );
}

function Help() {
  const fns = useMemo(
    () => [
      ["metric(q)", "one number from PromQL, or None"],
      ["metrics(q)", "every series: .labels and .value"],
      ["series(q, since, step)", "values over time"],
      ["logs(q, since)", "log lines from LogsQL, newest first"],
      ["log_count(q, since)", "how many lines match"],
      ["hosts()", "hosts by name with .state (now only)"],
      ["alert(summary, severity, notify, key)", "one alert; return a list for many"],
    ],
    [],
  );
  return (
    <section className="space-y-2 rounded-xl border border-line bg-panel p-3 text-xs">
      <h2 className="text-sm font-semibold">In a rule</h2>
      <ul className="space-y-1">
        {fns.map(([f, what]) => (
          <li key={f}>
            <code className="font-mono">{f}</code> <span className="text-muted">— {what}</span>
          </li>
        ))}
      </ul>
      <p className="text-muted">
        <code className="font-mono">EVERY</code> is how often it runs; <code className="font-mono">FOR</code> how long a problem must last before it
        fires. Severity is page, warn or info; notify names a channel, escalation, person or team.
      </p>
    </section>
  );
}

function download(name: string, source: string) {
  const url = URL.createObjectURL(new Blob([source], { type: "text/plain" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = `${name}.star`;
  a.click();
  URL.revokeObjectURL(url);
}

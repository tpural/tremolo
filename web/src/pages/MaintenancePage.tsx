import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarDays, ChevronLeft, ChevronRight, Plus, Trash2, Wrench } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api, can, type CalendarView, type Occurrence, type Period } from "../lib/api";
import { useAlerts, useHosts, useMe } from "../lib/hooks";
import { btn, ErrorNote, field, PageHeader } from "../components/ui";

const input = field;
const button = `${btn.base} ${btn.primary}`;
const quiet = `${btn.base} ${btn.sm} ${btn.quiet}`;
const STRIPES = {
  backgroundImage: "repeating-linear-gradient(135deg, transparent 0 5px, color-mix(in srgb, var(--sleep) 18%, transparent) 5px 10px)",
};
const WEEKDAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

/** monthGrid is the six weeks, Monday first, that show a month. */
export function monthGrid(month: Date): Date[] {
  const first = new Date(month.getFullYear(), month.getMonth(), 1);
  const start = new Date(first);
  start.setDate(1 - ((first.getDay() + 6) % 7));
  return Array.from({ length: 42 }, (_, i) => new Date(start.getFullYear(), start.getMonth(), start.getDate() + i));
}

const nextDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1);

/** on reports whether an occurrence overlaps a day; an open-ended one runs on. */
export function on(o: Occurrence, day: Date) {
  return Date.parse(o.start) < nextDay(day).getTime() && (!o.end || Date.parse(o.end) > day.getTime());
}

/** toInput is a time for a datetime-local input, in the browser's zone. */
export function toInput(t: Date | string | undefined) {
  if (!t) return "";
  const d = new Date(t);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

const fromInput = (s: string) => (s ? new Date(s).toISOString() : undefined);
const clock = (d: Date) => d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
const when = (t?: string) =>
  t ? new Date(t).toLocaleString([], { weekday: "short", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }) : "";
const sameDay = (a: Date, b: Date) => a.toDateString() === b.toDateString();

/** scope says what a period holds. */
export function scope(p: Pick<Period, "rules" | "hosts">) {
  if (!p.rules.length && !p.hosts.length) return "all alerts";
  const parts = [];
  if (p.rules.length) parts.push(p.rules.join(", "));
  if (p.hosts.length) parts.push(`on ${p.hosts.join(", ")}`);
  return parts.join(" ");
}

/** MaintenancePage is the calendar of maintenance: when alerts are held, once or on repeat. */
export function MaintenancePage() {
  const me = useMe();
  const editor = can(me.data?.role, "editor");
  const [month, setMonth] = useState(() => new Date(new Date().getFullYear(), new Date().getMonth(), 1));
  const [day, setDay] = useState(() => new Date(new Date().setHours(0, 0, 0, 0)));
  // A period being changed, or the day a new one starts on.
  const [editing, setEditing] = useState<Period | Date | null>(null);
  const grid = useMemo(() => monthGrid(month), [month]);
  const from = grid[0];
  const to = nextDay(grid[41]);
  const cal = useQuery({
    queryKey: ["calendar", from.toISOString(), to.toISOString()],
    queryFn: ({ signal }) => api.calendar(from, to, signal),
    refetchInterval: 60_000,
  });
  const data = cal.data;
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const now = new Date();
  const active = (data?.maintenance ?? []).filter((o) => Date.parse(o.start) <= now.getTime() && (!o.end || Date.parse(o.end) > now.getTime()));

  return (
    <div className="space-y-6">
      <div className="space-y-1">
        <PageHeader title="Maintenance" />
        <p className="max-w-4xl text-sm text-muted">
          While maintenance is on, the alerts it covers don't fire or notify; anything still wrong when it ends fires then, after the rule's{" "}
          <code className="font-mono">FOR</code>. Maintenance for whole hosts also shows them as in maintenance.
          {data && data.timezone !== tz && ` Times are in your zone (${tz}); repeats follow tremolo's (${data.timezone}).`}
        </p>
      </div>

      <div className="flex flex-wrap gap-3">
        {active.map((o) => (
          <div key={o.period.id + o.start} className="min-w-44 rounded-xl border border-sleep/40 px-3 py-2 text-sleep" style={STRIPES}>
            <p className="text-xs font-medium uppercase tracking-wide">maintenance now</p>
            <p className="flex items-center gap-1.5 font-semibold text-fg">
              <Wrench className="size-4" /> {o.period.title}
            </p>
            <p className="text-xs text-muted">
              {scope(o.period)} · {o.end ? `until ${when(o.end)}` : "until ended"}
            </p>
          </div>
        ))}
      </div>

      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <section className="min-w-0 space-y-2">
          <div className="flex items-center gap-2">
            <h2 className="font-semibold">{month.toLocaleDateString([], { month: "long", year: "numeric" })}</h2>
            <div className="ml-auto flex items-center gap-1">
              <button
                type="button"
                className={quiet}
                onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1))}
                aria-label="Previous month"
              >
                <ChevronLeft className="size-4" />
              </button>
              <button
                type="button"
                className="rounded-md border border-line px-2 py-0.5 text-xs"
                onClick={() => {
                  setMonth(new Date(now.getFullYear(), now.getMonth(), 1));
                  setDay(new Date(new Date().setHours(0, 0, 0, 0)));
                }}
              >
                Today
              </button>
              <button
                type="button"
                className={quiet}
                onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() + 1, 1))}
                aria-label="Next month"
              >
                <ChevronRight className="size-4" />
              </button>
            </div>
          </div>
          <div className="flex flex-col overflow-hidden rounded-xl border border-line bg-line/60 [gap:1px]">
            <div className="grid grid-cols-7 [gap:1px]">
              {WEEKDAYS.map((d) => (
                <div key={d} className="bg-panel px-1.5 py-1 text-[11px] font-medium text-muted">
                  {d}
                </div>
              ))}
            </div>
            {[0, 1, 2, 3, 4, 5].map((w) => (
              <Week key={w} week={grid.slice(w * 7, w * 7 + 7)} data={data} month={month} day={day} onPick={setDay} />
            ))}
          </div>
          {cal.error && <ErrorNote error={cal.error} />}
        </section>
        <DayPanel day={day} data={data} editor={editor} onEdit={setEditing} />
      </div>

      <Periods data={data} editor={editor} editing={editing} setEditing={setEditing} day={day} />
    </div>
  );
}

/** Bar is one maintenance period drawn across a week's days. */
export interface Bar {
  key: string;
  /** The period's id: its bars share a lane. */
  period: string;
  /** First and last day, 0 (Monday) to 6. */
  start: number;
  end: number;
  lane: number;
  label: string;
  title: string;
  /** It goes on before or after this week. */
  before: boolean;
  after: boolean;
}

type Draft = Omit<Bar, "lane">;

/** column is which of the week's days t falls on: -1 before it, 7 after. */
function column(week: Date[], t: number) {
  if (t < week[0].getTime()) return -1;
  const i = week.findIndex((d) => t < nextDay(d).getTime());
  return i === -1 ? 7 : i;
}

/**
 * stack puts each period's bars in one lane for the week, the lowest where none overlaps
 * another, longest first, so long events sit on top and a nightly one stays in its row.
 */
function stack(drafts: Draft[]): Bar[] {
  const groups = new Map<string, Draft[]>();
  for (const d of drafts) groups.set(d.period, [...(groups.get(d.period) ?? []), d]);
  const span = (g: Draft[]) => Math.max(...g.map((d) => d.end - d.start));
  const order = [...groups.values()].sort((a, b) => span(b) - span(a) || a[0].start - b[0].start);
  const out: Bar[] = [];
  for (const g of order) {
    let lane = 0;
    while (out.some((b) => b.lane === lane && g.some((d) => b.start <= d.end && d.start <= b.end))) lane++;
    out.push(...g.map((d) => ({ ...d, lane })));
  }
  return out;
}

const lanes = (bars: Bar[]) => Math.max(0, ...bars.map((b) => b.lane + 1));

/**
 * weekBars lays a week's maintenance out as bars across its days. Times a period is on back to
 * back, like a repeat lasting all day, blend into one bar; shorter ones, like nightly backups,
 * are one per day.
 */
export function weekBars(week: Date[], data?: CalendarView): Bar[] {
  const from = week[0].getTime();
  const to = nextDay(week[6]).getTime();
  const drafts: Draft[] = [];
  // The latest bar of each period, and when that time ended, to blend the next into.
  const last = new Map<string, { bar: Draft; end: number }>();
  for (const o of data?.maintenance ?? []) {
    const t0 = Date.parse(o.start);
    const t1 = o.end ? Date.parse(o.end) : Infinity;
    if (t1 <= from || t0 >= to) continue;
    const start = Math.max(column(week, t0), 0);
    const end = Math.min(column(week, Math.min(t1, to) - 1), 6);
    const p = o.period;
    const prev = last.get(p.id);
    if (prev && prev.end >= t0) {
      prev.bar.end = Math.max(prev.bar.end, end);
      prev.bar.after = t1 > to;
      prev.end = t1;
      continue;
    }
    const begins = new Date(t0);
    const at = t0 >= from && begins.getHours() + begins.getMinutes() > 0 ? `${clock(begins)} ` : "";
    const bar: Draft = {
      key: `${p.id}-${o.start}`,
      period: p.id,
      start,
      end,
      label: at + p.title,
      title: `${p.title}: holds ${scope(p)}${p.every ? `, every ${p.every}` : ""}, ${o.end ? `${when(o.start)} – ${when(o.end)}` : "until ended"}`,
      before: t0 < from,
      after: t1 > to,
    };
    last.set(p.id, { bar, end: t1 });
    drafts.push(bar);
  }
  return stack(drafts);
}

function Week({ week, data, month, day, onPick }: { week: Date[]; data?: CalendarView; month: Date; day: Date; onPick: (d: Date) => void }) {
  const bars = weekBars(week, data);
  const height = 28 + lanes(bars) * 20;
  // On a phone the bars are too narrow to read, so a dot marks each day with maintenance.
  const busy = new Set(bars.flatMap((b) => Array.from({ length: b.end - b.start + 1 }, (_, i) => b.start + i)));
  return (
    <div className="relative grid grid-cols-7 [gap:1px]">
      {week.map((d, col) => {
        const today = sameDay(d, new Date());
        return (
          <button
            key={d.toISOString()}
            type="button"
            onClick={() => onPick(d)}
            style={{ "--h": `${Math.max(height, 64)}px` } as React.CSSProperties}
            aria-label={d.toLocaleDateString([], { weekday: "long", month: "long", day: "numeric" })}
            aria-pressed={sameDay(d, day)}
            className={`flex min-h-12 flex-col items-start gap-1 p-1 text-left sm:min-h-[var(--h)] ${d.getMonth() === month.getMonth() ? "bg-panel" : "bg-panel-2/40"} ${sameDay(d, day) ? "ring-2 ring-accent ring-inset" : "hover:bg-panel-2"}`}
          >
            <span className={`text-xs ${today ? "rounded-full bg-accent px-1.5 font-semibold text-on-accent" : "text-muted"}`}>{d.getDate()}</span>
            {busy.has(col) && <span className="size-1.5 rounded-full bg-sleep sm:hidden" />}
          </button>
        );
      })}
      <div className="pointer-events-none absolute inset-x-0 top-6 hidden grid-cols-7 gap-y-0.5 sm:grid" style={{ gridAutoRows: "18px" }}>
        {bars.map((b) => (
          <div
            key={b.key}
            title={b.title}
            style={{ gridColumn: `${b.start + 1} / ${b.end + 2}`, gridRow: b.lane + 1, ...STRIPES }}
            className={`mx-0.5 truncate border border-sleep/40 px-1 text-[10px] leading-4 text-sleep sm:text-[11px] ${b.before ? "-ml-px rounded-l-none border-l-0" : "rounded-l"} ${b.after ? "-mr-px rounded-r-none border-r-0" : "rounded-r"}`}
          >
            <Wrench className="mr-0.5 inline size-2.5" />
            {b.label}
          </div>
        ))}
      </div>
    </div>
  );
}

function useInvalidate() {
  const client = useQueryClient();
  return () => {
    void client.invalidateQueries({ queryKey: ["calendar"] });
    void client.invalidateQueries({ queryKey: ["alerts"] });
  };
}

/** DayPanel lists a day's maintenance and plans more on it. */
function DayPanel({ day, data, editor, onEdit }: { day: Date; data?: CalendarView; editor: boolean; onEdit: (p: Period | Date) => void }) {
  const maint = (data?.maintenance ?? []).filter((o) => on(o, day));
  return (
    <aside className="space-y-3 rounded-xl border border-line bg-panel p-3 text-sm">
      <h2 className="flex items-center gap-2 font-semibold">
        <CalendarDays className="size-4" /> {day.toLocaleDateString([], { weekday: "long", month: "long", day: "numeric" })}
      </h2>
      {!maint.length && <p className="text-xs text-muted">No maintenance.</p>}
      {maint.map((o) => (
        <button
          key={o.period.id + o.start}
          type="button"
          disabled={!editor}
          onClick={() => onEdit(o.period)}
          className="block w-full rounded-md border border-sleep/40 px-2 py-1 text-left"
          style={STRIPES}
        >
          <span className="font-medium">{o.period.title}</span>
          <span className="block text-xs text-muted">
            {when(o.start)} – {o.end ? when(o.end) : "until ended"} · holds {scope(o.period)}
          </span>
          {o.period.note && <span className="block text-xs text-muted">“{o.period.note}”</span>}
        </button>
      ))}
      {editor && (
        <button type="button" className={quiet} onClick={() => onEdit(day)}>
          <Plus className="size-3.5" /> Plan maintenance on this day
        </button>
      )}
    </aside>
  );
}

/** Periods lists this month's periods and edits them. */
function Periods({
  data,
  editor,
  editing,
  setEditing,
  day,
}: {
  data?: CalendarView;
  editor: boolean;
  editing: Period | Date | null;
  setEditing: (p: Period | Date | null) => void;
  day: Date;
}) {
  const periods = new Map<string, Period>();
  for (const o of data?.maintenance ?? []) periods.set(o.period.id, o.period);
  return (
    <section className="space-y-3">
      <div className="flex items-center gap-3">
        <h2 className="font-semibold">This month</h2>
        {editor && editing === null && (
          <button type="button" className={quiet} onClick={() => setEditing(day)}>
            <Plus className="size-3.5" /> Plan maintenance
          </button>
        )}
      </div>
      {editing !== null && (
        <PeriodForm
          key={editing instanceof Date ? editing.toISOString() : editing.id}
          period={editing instanceof Date ? undefined : editing}
          day={editing instanceof Date ? editing : day}
          onDone={() => setEditing(null)}
        />
      )}
      <ul className="divide-y divide-line/60 rounded-xl border border-line bg-panel text-sm">
        {[...periods.values()].map((p) => (
          <li key={p.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
            <Wrench className="size-4 text-sleep" />
            <span className="font-medium">{p.title}</span>
            <span className="text-xs text-muted">
              {when(p.start)} – {p.end ? when(p.end) : "until ended"}
              {p.every ? ` · every ${p.every}${p.repeatUntil ? ` until ${new Date(p.repeatUntil).toLocaleDateString()}` : ""}` : ""} · holds{" "}
              {scope(p)}
              {p.by ? ` · by ${p.by}` : ""}
            </span>
            {editor && (
              <button type="button" className={`${quiet} ml-auto`} onClick={() => setEditing(p)}>
                Edit
              </button>
            )}
          </li>
        ))}
        {!periods.size && <li className="px-3 py-2 text-muted">None planned this month.</li>}
      </ul>
    </section>
  );
}

function PeriodForm({ period, day, onDone }: { period?: Period; day: Date; onDone: () => void }) {
  const invalidate = useInvalidate();
  const form = useRef<HTMLFormElement>(null);
  useEffect(() => form.current?.scrollIntoView({ block: "nearest", behavior: "smooth" }), []);
  const alerts = useAlerts();
  const hostList = useHosts();
  // Today from the next hour; another day from 09:00.
  const start = sameDay(day, new Date()) ? new Date(Math.ceil(Date.now() / 3_600_000) * 3_600_000) : new Date(new Date(day).setHours(9, 0, 0, 0));
  const [title, setTitle] = useState(period?.title ?? "");
  const [from, setFrom] = useState(toInput(period?.start ?? start));
  const [until, setUntil] = useState(period ? toInput(period.end) : toInput(new Date(start.getTime() + 2 * 3_600_000)));
  const [every, setEvery] = useState<"" | "day" | "week">(period?.every ?? "");
  const [repeatUntil, setRepeatUntil] = useState(period?.repeatUntil ? toInput(period.repeatUntil).slice(0, 10) : "");
  const [rules, setRules] = useState<string[]>(period?.rules ?? []);
  const [hosts, setHosts] = useState<string[]>(period?.hosts ?? []);
  const [everything, setEverything] = useState(period ? !period.rules.length && !period.hosts.length : true);
  const [note, setNote] = useState(period?.note ?? "");
  const [confirm, setConfirm] = useState(false);
  const save = useMutation({
    mutationFn: () =>
      api.savePeriod({
        id: period?.id,
        title,
        start: fromInput(from)!,
        end: fromInput(until),
        every,
        repeatUntil: repeatUntil ? new Date(`${repeatUntil}T23:59:59`).toISOString() : undefined,
        rules: everything ? [] : rules,
        hosts: everything ? [] : hosts,
        note,
      }),
    onSuccess: () => {
      invalidate();
      onDone();
    },
  });
  const remove = useMutation({
    mutationFn: () => api.deletePeriod(period!.id),
    onSuccess: () => {
      invalidate();
      onDone();
    },
  });
  const ruleNames = (alerts.data?.rules ?? []).map((r) => r.name);
  const hostNames = (hostList.data ?? []).map((h) => h.name);
  const toggle = (list: string[], set: (v: string[]) => void, v: string) => set(list.includes(v) ? list.filter((x) => x !== v) : [...list, v]);
  return (
    <form
      ref={form}
      className="space-y-3 rounded-xl border border-sleep/40 bg-panel p-3 text-sm"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <input
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        maxLength={200}
        placeholder="Title, e.g. Sunday patching"
        className={`${input} w-full`}
        required
        autoFocus
      />
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted">
        <label className="flex items-center gap-1.5">
          from <input type="datetime-local" value={from} onChange={(e) => setFrom(e.target.value)} className={input} required />
        </label>
        <label className="flex items-center gap-1.5">
          until <input type="datetime-local" value={until} onChange={(e) => setUntil(e.target.value)} className={input} required={every !== ""} />
        </label>
        {!until && !every && <span>(until someone ends it)</span>}
        <select value={every} onChange={(e) => setEvery(e.target.value as "" | "day" | "week")} className={input} aria-label="Repeat">
          <option value="">Once</option>
          <option value="day">Every day</option>
          <option value="week">Every week</option>
        </select>
        {every && (
          <label className="flex items-center gap-1.5">
            repeat until <input type="date" value={repeatUntil} onChange={(e) => setRepeatUntil(e.target.value)} className={input} />
          </label>
        )}
      </div>
      <div className="space-y-1.5">
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={everything} onChange={(e) => setEverything(e.target.checked)} /> Hold every alert
        </label>
        {!everything && (
          <>
            <Chips label="Rules (none picked: all of them)" names={ruleNames} picked={rules} toggle={(v) => toggle(rules, setRules, v)} />
            <Chips label="Hosts (none picked: any host)" names={hostNames} picked={hosts} toggle={(v) => toggle(hosts, setHosts, v)} />
          </>
        )}
      </div>
      <input value={note} onChange={(e) => setNote(e.target.value)} maxLength={500} placeholder="Note (optional)" className={`${input} w-full`} />
      <div className="flex items-center gap-3">
        <button type="submit" disabled={save.isPending || !title} className={button}>
          {save.isPending ? "Saving…" : period ? "Save" : "Plan it"}
        </button>
        <button type="button" className={quiet} onClick={onDone}>
          Cancel
        </button>
        {period && (
          <button
            type="button"
            className={`${quiet} ml-auto ${confirm ? "text-bad" : ""}`}
            onClick={() => (confirm ? remove.mutate() : setConfirm(true))}
          >
            <Trash2 className="size-3.5" /> {confirm ? "Delete it, every time it repeats?" : "Delete"}
          </button>
        )}
      </div>
      {(save.error ?? remove.error) && <p className="text-xs text-bad">{(save.error ?? remove.error)!.message}</p>}
    </form>
  );
}

function Chips({ label, names, picked, toggle }: { label: string; names: string[]; picked: string[]; toggle: (v: string) => void }) {
  const all = [...new Set([...names, ...picked])].sort();
  return (
    <div className="space-y-1">
      <p className="text-xs text-muted">{label}</p>
      <div className="flex flex-wrap gap-1">
        {all.map((n) => (
          <button
            key={n}
            type="button"
            onClick={() => toggle(n)}
            className={`rounded-full border px-2 py-0.5 text-xs ${picked.includes(n) ? "border-sleep bg-sleep/15 text-sleep" : "border-line text-muted hover:text-fg"}`}
          >
            {n}
          </button>
        ))}
      </div>
    </div>
  );
}

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, CalendarDays, ChevronLeft, ChevronRight, Phone, Plus, Trash2, Wrench, X } from "lucide-react";
import { useMemo, useState } from "react";
import { api, can, type CalendarView, type Occurrence, type Period, type Schedule } from "../lib/api";
import { useAlerts, useHosts, useMe } from "../lib/hooks";

const input = "rounded-md border border-line bg-bg px-2 py-1 text-sm";
const button = "rounded-md bg-accent px-3 py-1 text-sm font-medium text-white disabled:opacity-60 dark:text-bg";
const quiet = "inline-flex items-center gap-1 text-xs text-muted hover:text-fg disabled:opacity-50";
const TONES = [
  "border-accent/40 bg-accent/10 text-accent",
  "border-good/40 bg-good/10 text-good",
  "border-warn/40 bg-warn/10 text-warn",
  "border-fg/30 bg-fg/5 text-fg",
];
const STRIPES = {
  backgroundImage: "repeating-linear-gradient(135deg, transparent 0 5px, color-mix(in srgb, var(--sleep) 18%, transparent) 5px 10px)",
};
const WEEKDAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
const DAY = 86_400_000;

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

/** OnCallPage is the on-call calendar: who's on call when, overrides, and maintenance that holds alerts. */
export function OnCallPage() {
  const me = useMe();
  const editor = can(me.data?.role, "editor");
  const [month, setMonth] = useState(() => new Date(new Date().getFullYear(), new Date().getMonth(), 1));
  const [day, setDay] = useState(() => new Date(new Date().setHours(0, 0, 0, 0)));
  const [editing, setEditing] = useState<Period | "new" | null>(null);
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
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">On-call</h1>
        <p className="text-sm text-muted">
          Who's paged when, and maintenance that holds alerts. Name a schedule in a rule's <code className="font-mono">notify</code>, an escalation or
          a team to page whoever is on call.
          {data && data.timezone !== tz && ` Times are in your zone (${tz}); handoffs follow tremolo's (${data.timezone}).`}
        </p>
      </div>

      <div className="flex flex-wrap gap-3">
        {(data?.schedules ?? []).map((s, i) => (
          <div key={s.name} className={`min-w-44 rounded-xl border px-3 py-2 ${TONES[i % TONES.length]}`}>
            <p className="text-xs font-medium uppercase tracking-wide opacity-80">{s.name}</p>
            <p className="flex items-center gap-1.5 text-lg font-semibold text-fg">
              <Phone className="size-4" /> {s.onCall ?? "nobody"}
            </p>
            {s.until && <p className="text-xs text-muted">until {when(s.until)}</p>}
          </div>
        ))}
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
        {data && !data.schedules.length && <p className="text-sm text-muted">No schedules yet{editor ? "; make one below." : "."}</p>}
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
          {cal.error && <p className="text-sm text-bad">{String(cal.error.message)}</p>}
        </section>
        <DayPanel day={day} data={data} editor={editor} me={me.data?.name} onEditPeriod={setEditing} />
      </div>

      <Schedules data={data} editor={editor} />
      <Maintenance data={data} editor={editor} editing={editing} setEditing={setEditing} day={day} />
    </div>
  );
}

/** Bar is one stretch drawn across a week's days: a shift or a maintenance period. */
export interface Bar {
  key: string;
  kind: "shift" | "maintenance";
  /** Which schedule, for its colour. */
  tone: number;
  /** First and last day, 0 (Monday) to 6. */
  start: number;
  end: number;
  lane: number;
  label: string;
  title: string;
  override?: boolean;
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

/** stack gives each bar the lowest lane, from base, where it doesn't overlap another. */
function stack(drafts: Draft[], base: number): Bar[] {
  const out: Bar[] = [];
  for (const d of [...drafts].sort((a, b) => a.start - b.start || b.end - a.end)) {
    let lane = base;
    while (out.some((b) => b.lane === lane && b.start <= d.end && d.start <= b.end)) lane++;
    out.push({ ...d, lane });
  }
  return out;
}

const lanes = (bars: Bar[], base: number) => Math.max(base, ...bars.map((b) => b.lane + 1));

/**
 * weekBars lays a week's shifts and maintenance out as bars across its days: each schedule's
 * shifts in its own lanes, then maintenance. A daily period is one bar for the week.
 */
export function weekBars(week: Date[], data?: CalendarView): Bar[] {
  const from = week[0].getTime();
  const to = nextDay(week[6]).getTime();
  const out: Bar[] = [];
  let base = 0;
  (data?.schedules ?? []).forEach((s, i) => {
    const drafts: Draft[] = [];
    for (const sh of s.shifts) {
      const t0 = Date.parse(sh.start);
      const t1 = Date.parse(sh.end);
      if (t1 <= from || t0 >= to) continue;
      const start = Math.max(column(week, t0), 0);
      let end = Math.min(column(week, t1 - 1), 6);
      // A turn handed off in the morning belongs to the next person that day.
      if (t1 < to && new Date(t1).getHours() < 12 && column(week, t1) > Math.max(column(week, t0), -1)) end = column(week, t1) - 1;
      if (end < start) continue;
      const from0 = t0 >= from && new Date(t0).getHours() + new Date(t0).getMinutes() > 0 ? `${clock(new Date(t0))} ` : "";
      drafts.push({
        key: `${s.name}-${sh.start}`,
        kind: "shift",
        tone: i,
        start,
        end,
        label: from0 + sh.person,
        title: `${s.name}: ${sh.person}, ${when(sh.start)} – ${when(sh.end)}${sh.override ? " (covering)" : ""}`,
        override: sh.override,
        before: t0 < from,
        after: t1 > to,
      });
    }
    const bars = stack(drafts, base);
    out.push(...bars);
    base = lanes(bars, base + 1);
  });
  const drafts: Draft[] = [];
  const daily = new Map<string, Draft>();
  for (const o of data?.maintenance ?? []) {
    const t0 = Date.parse(o.start);
    const t1 = o.end ? Date.parse(o.end) : Infinity;
    if (t1 <= from || t0 >= to) continue;
    const start = Math.max(column(week, t0), 0);
    const end = Math.min(column(week, Math.min(t1, to) - 1), 6);
    const p = o.period;
    const hours = o.end ? `${clock(new Date(t0))}–${clock(new Date(t1))}` : "";
    const merged = daily.get(p.id);
    if (p.every === "day" && merged) {
      merged.end = Math.max(merged.end, end);
      merged.after = t1 > to;
      continue;
    }
    const d: Draft = {
      key: `${p.id}-${o.start}`,
      kind: "maintenance",
      tone: 0,
      start,
      end,
      label: p.every === "day" ? `${p.title} · ${hours} daily` : `${t0 >= from && hours ? `${clock(new Date(t0))} ` : ""}${p.title}`,
      title: `${p.title}: holds ${scope(p)}${p.every ? `, every ${p.every}` : ""}${hours ? `, ${hours}` : ", until ended"}`,
      before: t0 < from,
      after: t1 > to,
    };
    if (p.every === "day") daily.set(p.id, d);
    drafts.push(d);
  }
  out.push(...stack(drafts, base));
  return out;
}

function Week({ week, data, month, day, onPick }: { week: Date[]; data?: CalendarView; month: Date; day: Date; onPick: (d: Date) => void }) {
  const bars = weekBars(week, data);
  const height = 28 + lanes(bars, 0) * 20;
  return (
    <div className="relative grid grid-cols-7 [gap:1px]">
      {week.map((d) => {
        const today = sameDay(d, new Date());
        return (
          <button
            key={d.toISOString()}
            type="button"
            onClick={() => onPick(d)}
            style={{ minHeight: Math.max(height, 64) }}
            className={`bg-panel p-1 text-left align-top ${d.getMonth() === month.getMonth() ? "" : "bg-panel/60 text-muted/60"} ${sameDay(d, day) ? "ring-2 ring-accent ring-inset" : "hover:bg-panel-2"}`}
          >
            <span className={`text-xs ${today ? "rounded-full bg-accent px-1.5 font-semibold text-white dark:text-bg" : "text-muted"}`}>
              {d.getDate()}
            </span>
          </button>
        );
      })}
      <div className="pointer-events-none absolute inset-x-0 top-6 grid grid-cols-7 gap-y-0.5" style={{ gridAutoRows: "18px" }}>
        {bars.map((b) => (
          <div
            key={b.key}
            title={b.title}
            style={{ gridColumn: `${b.start + 1} / ${b.end + 2}`, gridRow: b.lane + 1, ...(b.kind === "maintenance" ? STRIPES : {}) }}
            className={`mx-0.5 truncate border px-1 text-[10px] leading-4 sm:text-[11px] ${
              b.kind === "maintenance" ? "border-sleep/40 text-sleep" : TONES[b.tone % TONES.length]
            } ${b.override ? "border-dashed" : ""} ${b.before ? "-ml-px rounded-l-none border-l-0" : "rounded-l"} ${b.after ? "-mr-px rounded-r-none border-r-0" : "rounded-r"}`}
          >
            {b.kind === "maintenance" && <Wrench className="mr-0.5 inline size-2.5" />}
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

/** DayPanel lists a day's shifts and maintenance, and covers a shift on it. */
function DayPanel({
  day,
  data,
  editor,
  me,
  onEditPeriod,
}: {
  day: Date;
  data?: CalendarView;
  editor: boolean;
  me?: string;
  onEditPeriod: (p: Period) => void;
}) {
  const invalidate = useInvalidate();
  const remove = useMutation({ mutationFn: (o: { schedule: string; id: string }) => api.deleteOverride(o.schedule, o.id), onSuccess: invalidate });
  const maint = (data?.maintenance ?? []).filter((o) => on(o, day));
  return (
    <aside className="space-y-4 rounded-xl border border-line bg-panel p-3 text-sm">
      <h2 className="flex items-center gap-2 font-semibold">
        <CalendarDays className="size-4" /> {day.toLocaleDateString([], { weekday: "long", month: "long", day: "numeric" })}
      </h2>
      {(data?.schedules ?? []).map((s) => {
        const shifts = s.shifts.filter((sh) => Date.parse(sh.start) < nextDay(day).getTime() && Date.parse(sh.end) > day.getTime());
        return (
          <div key={s.name} className="space-y-1">
            <p className="text-xs font-medium uppercase tracking-wide text-muted">{s.name}</p>
            {!shifts.length && <p className="text-xs text-muted">Nobody on call.</p>}
            {shifts.map((sh) => {
              const o = sh.override
                ? s.overrides.find(
                    (x) => x.person === sh.person && Date.parse(x.start) <= Date.parse(sh.start) && Date.parse(x.end) >= Date.parse(sh.end),
                  )
                : undefined;
              return (
                <p key={sh.start} className="flex items-center gap-2">
                  <span className="font-medium">{sh.person}</span>
                  <span className="text-xs text-muted">
                    {when(sh.start)} – {when(sh.end)}
                    {sh.override ? " · covering" : ""}
                    {o?.note ? ` · “${o.note}”` : ""}
                  </span>
                  {o && editor && (
                    <button
                      type="button"
                      className={`${quiet} ml-auto`}
                      onClick={() => remove.mutate({ schedule: s.name, id: o.id })}
                      aria-label="Remove override"
                    >
                      <X className="size-3.5" />
                    </button>
                  )}
                </p>
              );
            })}
          </div>
        );
      })}
      {maint.length > 0 && (
        <div className="space-y-1">
          <p className="text-xs font-medium uppercase tracking-wide text-muted">Maintenance</p>
          {maint.map((o) => (
            <button
              key={o.period.id + o.start}
              type="button"
              disabled={!editor}
              onClick={() => onEditPeriod(o.period)}
              className="block w-full rounded-md border border-sleep/40 px-2 py-1 text-left"
              style={STRIPES}
            >
              <span className="font-medium">{o.period.title}</span>
              <span className="block text-xs text-muted">
                {when(o.start)} – {o.end ? when(o.end) : "until ended"} · holds {scope(o.period)}
              </span>
            </button>
          ))}
        </div>
      )}
      {(data?.schedules ?? []).length > 0 && <Cover key={day.toISOString()} day={day} data={data!} editor={editor} me={me} />}
    </aside>
  );
}

/** Cover puts someone on call in place of the rotation: yourself, or anyone if you're an editor. */
function Cover({ day, data, editor, me }: { day: Date; data: CalendarView; editor: boolean; me?: string }) {
  const invalidate = useInvalidate();
  const alerts = useAlerts();
  const [schedule, setSchedule] = useState(data.schedules[0].name);
  const [person, setPerson] = useState(me ?? "");
  const start = new Date(day);
  start.setHours(new Date(data.schedules.find((s) => s.name === schedule)?.start ?? day).getHours(), 0);
  const [from, setFrom] = useState(toInput(start));
  const [until, setUntil] = useState(toInput(new Date(start.getTime() + DAY)));
  const [note, setNote] = useState("");
  const add = useMutation({
    mutationFn: () => api.addOverride(schedule, { person, start: fromInput(from)!, end: fromInput(until)!, note }),
    onSuccess: () => {
      setNote("");
      invalidate();
    },
  });
  const people = editor ? (alerts.data?.people ?? []) : me ? [me] : [];
  return (
    <form
      className="space-y-2 border-t border-line pt-3"
      onSubmit={(e) => {
        e.preventDefault();
        add.mutate();
      }}
    >
      <p className="font-medium">Cover a shift</p>
      <div className="flex flex-wrap gap-2">
        <select value={schedule} onChange={(e) => setSchedule(e.target.value)} className={input} aria-label="Schedule">
          {data.schedules.map((s) => (
            <option key={s.name}>{s.name}</option>
          ))}
        </select>
        <select value={person} onChange={(e) => setPerson(e.target.value)} className={input} aria-label="Who">
          {people.map((p) => (
            <option key={p} value={p}>
              {p === me ? `${p} (you)` : p}
            </option>
          ))}
        </select>
      </div>
      <label className="flex items-center gap-2 text-xs text-muted">
        From <input type="datetime-local" value={from} onChange={(e) => setFrom(e.target.value)} className={`${input} flex-1`} required />
      </label>
      <label className="flex items-center gap-2 text-xs text-muted">
        Until <input type="datetime-local" value={until} onChange={(e) => setUntil(e.target.value)} className={`${input} flex-1`} required />
      </label>
      <input
        value={note}
        onChange={(e) => setNote(e.target.value)}
        maxLength={500}
        placeholder="Note (optional), e.g. swapping with tim"
        className={`${input} w-full`}
      />
      <button type="submit" disabled={add.isPending || !person} className={button}>
        {add.isPending ? "Saving…" : "Put them on call"}
      </button>
      {add.error && <p className="text-xs text-bad">{add.error.message}</p>}
    </form>
  );
}

/** Schedules lists the rotations, each editable in place. */
function Schedules({ data, editor }: { data?: CalendarView; editor: boolean }) {
  const [adding, setAdding] = useState(false);
  return (
    <section className="space-y-3">
      <div className="flex items-center gap-3">
        <h2 className="font-semibold">Schedules</h2>
        {editor && !adding && (
          <button type="button" className={quiet} onClick={() => setAdding(true)}>
            <Plus className="size-3.5" /> New schedule
          </button>
        )}
      </div>
      <p className="text-sm text-muted">
        Members take turns in order, each for the turn's length, handing off at the first handoff's time of day. For a backup, make a second schedule
        with the same people starting a turn later, and add it to an escalation after a delay.
      </p>
      <div className="space-y-2">
        {(data?.schedules ?? []).map((s) => (
          <ScheduleForm key={s.name} schedule={s} editor={editor} />
        ))}
        {adding && <ScheduleForm editor={editor} onDone={() => setAdding(false)} />}
      </div>
    </section>
  );
}

const LENGTHS = [
  { days: 1, label: "Daily" },
  { days: 7, label: "Weekly" },
  { days: 14, label: "Every two weeks" },
];

function ScheduleForm({ schedule, editor, onDone }: { schedule?: Schedule; editor: boolean; onDone?: () => void }) {
  const invalidate = useInvalidate();
  const alerts = useAlerts();
  const [name, setName] = useState(schedule?.name ?? "");
  const [description, setDescription] = useState(schedule?.description ?? "");
  const [members, setMembers] = useState<string[]>(schedule?.members ?? []);
  const [days, setDays] = useState(schedule?.days ?? 7);
  const [start, setStart] = useState(() => {
    if (schedule) return toInput(schedule.start);
    const monday = new Date();
    monday.setDate(monday.getDate() - ((monday.getDay() + 6) % 7));
    monday.setHours(9, 0, 0, 0);
    return toInput(monday);
  });
  const [confirm, setConfirm] = useState(false);
  const save = useMutation({
    mutationFn: () => api.saveSchedule({ name, description, members, start: fromInput(start)!, days }),
    onSuccess: () => {
      invalidate();
      onDone?.();
    },
  });
  const remove = useMutation({ mutationFn: () => api.deleteSchedule(name), onSuccess: invalidate });
  const move = (i: number, d: number) => {
    const next = [...members];
    [next[i], next[i + d]] = [next[i + d], next[i]];
    setMembers(next);
  };
  const others = (alerts.data?.people ?? []).filter((p) => !members.includes(p));
  const disabled = !editor;
  return (
    <form
      className="space-y-2 rounded-xl border border-line bg-panel p-3 text-sm"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="flex flex-wrap items-center gap-2">
        {schedule ? (
          <span className="font-semibold">{schedule.name}</span>
        ) : (
          <input
            value={name}
            onChange={(e) => setName(e.target.value.toLowerCase())}
            placeholder="name, e.g. platform-oncall"
            className={input}
            required
            autoFocus
          />
        )}
        <input
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          disabled={disabled}
          maxLength={500}
          placeholder="What it covers"
          className={`${input} min-w-48 flex-1`}
        />
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        {members.map((m, i) => (
          <span key={m} className="inline-flex items-center gap-0.5 rounded-full border border-line bg-bg px-2 py-0.5 text-xs">
            <span className="mr-1 text-muted">{i + 1}.</span>
            {m}
            {editor && (
              <>
                <button type="button" className={quiet} disabled={i === 0} onClick={() => move(i, -1)} aria-label={`Move ${m} earlier`}>
                  <ArrowUp className="size-3" />
                </button>
                <button type="button" className={quiet} disabled={i === members.length - 1} onClick={() => move(i, 1)} aria-label={`Move ${m} later`}>
                  <ArrowDown className="size-3" />
                </button>
                <button type="button" className={quiet} onClick={() => setMembers(members.filter((x) => x !== m))} aria-label={`Remove ${m}`}>
                  <X className="size-3" />
                </button>
              </>
            )}
          </span>
        ))}
        {editor && others.length > 0 && (
          <select
            value=""
            onChange={(e) => e.target.value && setMembers([...members, e.target.value])}
            className={`${input} text-xs`}
            aria-label="Add a member"
          >
            <option value="">Add someone…</option>
            {others.map((p) => (
              <option key={p}>{p}</option>
            ))}
          </select>
        )}
        {!members.length && <span className="text-xs text-muted">No members yet.</span>}
      </div>
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted">
        <select
          value={LENGTHS.some((l) => l.days === days) ? days : "custom"}
          onChange={(e) => setDays(e.target.value === "custom" ? 3 : Number(e.target.value))}
          disabled={disabled}
          className={input}
          aria-label="Turn length"
        >
          {LENGTHS.map((l) => (
            <option key={l.days} value={l.days}>
              {l.label}
            </option>
          ))}
          <option value="custom">Every N days</option>
        </select>
        {!LENGTHS.some((l) => l.days === days) && (
          <input
            type="number"
            min={1}
            max={366}
            value={days}
            onChange={(e) => setDays(Number(e.target.value))}
            disabled={disabled}
            className={`${input} w-20`}
            aria-label="Days per turn"
          />
        )}
        <label className="flex items-center gap-1.5">
          first handoff{" "}
          <input type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} disabled={disabled} className={input} required />
        </label>
      </div>
      {editor && (
        <div className="flex items-center gap-3">
          <button type="submit" disabled={save.isPending || !name} className={button}>
            {save.isPending ? "Saving…" : schedule ? "Save" : "Create"}
          </button>
          {onDone && (
            <button type="button" className={quiet} onClick={onDone}>
              Cancel
            </button>
          )}
          {schedule && (
            <button
              type="button"
              className={`${quiet} ml-auto ${confirm ? "text-bad" : ""}`}
              onClick={() => (confirm ? remove.mutate() : setConfirm(true))}
            >
              <Trash2 className="size-3.5" /> {confirm ? "Delete it and its overrides?" : "Delete"}
            </button>
          )}
        </div>
      )}
      {(save.error ?? remove.error) && <p className="text-xs text-bad">{(save.error ?? remove.error)!.message}</p>}
    </form>
  );
}

/** Maintenance lists planned periods and edits them. */
function Maintenance({
  data,
  editor,
  editing,
  setEditing,
  day,
}: {
  data?: CalendarView;
  editor: boolean;
  editing: Period | "new" | null;
  setEditing: (p: Period | "new" | null) => void;
  day: Date;
}) {
  const periods = new Map<string, Period>();
  for (const o of data?.maintenance ?? []) periods.set(o.period.id, o.period);
  return (
    <section className="space-y-3">
      <div className="flex items-center gap-3">
        <h2 className="font-semibold">Maintenance</h2>
        {editor && editing === null && (
          <button type="button" className={quiet} onClick={() => setEditing("new")}>
            <Plus className="size-3.5" /> Plan maintenance
          </button>
        )}
      </div>
      <p className="text-sm text-muted">
        While maintenance is on, the alerts it covers don't fire or notify; anything still wrong when it ends fires then, after the rule's{" "}
        <code className="font-mono">FOR</code>. Maintenance for whole hosts also shows them as in maintenance.
      </p>
      {editing !== null && (
        <PeriodForm
          key={editing === "new" ? "new" : editing.id}
          period={editing === "new" ? undefined : editing}
          day={day}
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

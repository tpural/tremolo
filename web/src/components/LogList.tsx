import { Check, Copy, Filter, FilterX, ListTree } from "lucide-react";
import { Fragment, memo, useState } from "react";
import type { LogEntry } from "../lib/api";
import { level, sourceOf, tokenize, type Level, type Token } from "../lib/logs";

export const LEVEL_STYLE: Record<Level, { label: string; bar: string; pill: string; row: string }> = {
  error: { label: "ERR", bar: "bg-bad", pill: "bg-bad/15 text-bad", row: "bg-bad/[0.06]" },
  warn: { label: "WRN", bar: "bg-warn", pill: "bg-warn/15 text-warn", row: "bg-warn/[0.05]" },
  info: { label: "INF", bar: "bg-accent", pill: "bg-accent/12 text-accent", row: "" },
  debug: { label: "DBG", bar: "bg-muted/60", pill: "bg-panel-2 text-muted", row: "" },
  "": { label: "LOG", bar: "bg-line", pill: "bg-panel-2 text-muted", row: "" },
};

const TOKEN_CLASS: Record<Token["kind"], string> = {
  text: "",
  key: "text-tok-key",
  string: "text-tok-str",
  number: "text-tok-num",
  bool: "text-tok-bool",
  punct: "",
  time: "text-muted",
};

// Fields shown elsewhere in a row, or internal to VictoriaLogs.
const HIDDEN = new Set(["_msg", "_time", "_stream"]);

/** Highlight marks occurrences of terms, case-insensitively. */
function Highlight({ text, terms }: { text: string; terms: string[] }) {
  if (!terms.length) return <>{text}</>;
  const re = new RegExp(`(${terms.map((t) => t.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|")})`, "gi");
  return (
    <>
      {text.split(re).map((part, i) =>
        i % 2 ? (
          <mark key={i} className="rounded-sm bg-warn/30 px-px text-inherit">
            {part}
          </mark>
        ) : (
          <Fragment key={i}>{part}</Fragment>
        ),
      )}
    </>
  );
}

export function Message({ text, terms = [] }: { text: string; terms?: string[] }) {
  return (
    <>
      {tokenize(text).map((t, i) => (
        <span key={i} className={TOKEN_CLASS[t.kind]}>
          <Highlight text={t.text} terms={terms} />
        </span>
      ))}
    </>
  );
}

export interface RowActions {
  onFilter?: (field: string, value: string, negate: boolean) => void;
  onContext?: (e: LogEntry) => void;
}

function formatTime(iso: string, withDate: boolean) {
  const d = new Date(iso);
  const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
  const ms = String(d.getMilliseconds()).padStart(3, "0");
  return `${withDate ? d.toLocaleDateString([], { month: "short", day: "numeric" }) + " " : ""}${time}.${ms}`;
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      title={label}
      onClick={() => {
        navigator.clipboard?.writeText(text).then(() => {
          setDone(true);
          setTimeout(() => setDone(false), 1200);
        });
      }}
      className="inline-flex items-center gap-1 rounded-md border border-line px-2 py-0.5 text-xs text-muted hover:text-fg"
    >
      {done ? <Check className="size-3" /> : <Copy className="size-3" />} {label}
    </button>
  );
}

const Row = memo(function Row({
  e,
  open,
  toggle,
  wrap,
  withDate,
  terms,
  actions,
  focused,
}: {
  e: LogEntry;
  open: boolean;
  toggle: () => void;
  wrap: boolean;
  withDate: boolean;
  terms: string[];
  actions: RowActions;
  focused?: boolean;
}) {
  const lv = level(e);
  const style = LEVEL_STYLE[lv];
  const src = sourceOf(e);
  const fields = Object.entries(e)
    .filter(([k]) => !HIDDEN.has(k))
    .sort(([a], [b]) => a.localeCompare(b));
  return (
    <div className={`border-b border-line last:border-0 ${style.row} ${focused ? "ring-1 ring-accent ring-inset" : ""}`}>
      <button type="button" onClick={toggle} className="group flex w-full items-start gap-2 py-1 pr-2 text-left hover:bg-panel-2/70">
        <span className={`mt-0.5 h-4 w-1 shrink-0 rounded-full ${style.bar}`} />
        <span className="shrink-0 text-muted tabular-nums">{formatTime(e._time, withDate)}</span>
        <span className={`mt-px shrink-0 rounded px-1 text-[10px] font-semibold leading-4 ${style.pill}`}>{style.label}</span>
        {src && (
          <span className="hidden w-52 shrink-0 truncate text-accent/90 md:inline" title={src}>
            {src}
          </span>
        )}
        <span className={`min-w-0 flex-1 ${wrap || open ? "whitespace-pre-wrap break-all" : "truncate"}`}>
          <Message text={e._msg ?? ""} terms={terms} />
        </span>
      </button>
      {open && (
        <div className="mb-2 ml-3 space-y-2 rounded-lg border border-line bg-panel p-3 font-sans">
          <div className="flex flex-wrap gap-2">
            <CopyButton text={e._msg ?? ""} label="Copy message" />
            <CopyButton text={JSON.stringify(e, null, 2)} label="Copy JSON" />
            {actions.onContext && e._stream_id && (
              <button
                type="button"
                onClick={() => actions.onContext?.(e)}
                className="inline-flex items-center gap-1 rounded-md border border-line px-2 py-0.5 text-xs text-muted hover:text-fg"
              >
                <ListTree className="size-3" /> Show context
              </button>
            )}
          </div>
          <table className="w-full font-mono text-xs">
            <tbody>
              <tr>
                <td className="w-56 py-0.5 pr-3 align-top text-muted">_time</td>
                <td className="py-0.5 break-all">{e._time}</td>
                <td />
              </tr>
              {fields.map(([k, v]) => (
                <tr key={k} className="group/field hover:bg-panel-2">
                  <td className="py-0.5 pr-3 align-top text-muted">{k}</td>
                  <td className="py-0.5 break-all">{v}</td>
                  <td className="w-16 py-0.5 text-right whitespace-nowrap">
                    {actions.onFilter && k !== "_stream_id" && (
                      <span className="invisible inline-flex gap-1 group-hover/field:visible">
                        <button type="button" title={`Only ${k} = ${v}`} onClick={() => actions.onFilter?.(k, v, false)} className="text-muted hover:text-accent">
                          <Filter className="size-3.5" />
                        </button>
                        <button type="button" title={`Hide ${k} = ${v}`} onClick={() => actions.onFilter?.(k, v, true)} className="text-muted hover:text-bad">
                          <FilterX className="size-3.5" />
                        </button>
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
});

function rowKey(e: LogEntry, i: number) {
  return `${e._time}|${e._stream_id ?? ""}|${i}`;
}

export function LogList({
  entries,
  maxHeight,
  wrap = false,
  terms = [],
  actions = {},
  focus,
}: {
  entries: LogEntry[];
  maxHeight?: string;
  wrap?: boolean;
  terms?: string[];
  actions?: RowActions;
  /** An entry to mark, e.g. the line whose context is shown. */
  focus?: LogEntry;
}) {
  const [open, setOpen] = useState<string | null>(null);
  // Show the date only when the lines span more than one day.
  const days = new Set(entries.map((e) => (e._time ?? "").slice(0, 10)));
  return (
    <div className="overflow-auto font-mono text-xs leading-5" style={{ maxHeight }}>
      {entries.map((e, i) => {
        const k = rowKey(e, i);
        return (
          <Row
            key={k}
            e={e}
            open={open === k}
            toggle={() => setOpen(open === k ? null : k)}
            wrap={wrap}
            withDate={days.size > 1}
            terms={terms}
            actions={actions}
            focused={focus != null && focus._time === e._time && focus._msg === e._msg}
          />
        );
      })}
    </div>
  );
}

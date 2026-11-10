import { ChevronDown, ChevronRight, CircleAlert } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { ms, serviceColor, tag, type JaegerTrace, type KeyValue, type SpanRow } from "../lib/traces";

// Tags the waterfall shows in its own way.
const SHOWN = new Set(["span.kind", "error", "otel.status_code", "otel.status_description"]);

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] font-medium uppercase tracking-wide text-muted">{label}</div>
      <div className="truncate font-mono text-xs">{children}</div>
    </div>
  );
}

function Tags({ title, tags }: { title: string; tags: KeyValue[] }) {
  if (!tags.length) return null;
  return (
    <div>
      <h4 className="mb-1 text-xs font-medium text-muted">{title}</h4>
      <table className="w-full font-mono text-xs">
        <tbody>
          {[...tags]
            .sort((a, b) => a.key.localeCompare(b.key))
            .map((t) => (
              <tr key={t.key} className="hover:bg-panel-2">
                <td className="w-64 py-0.5 pr-3 align-top text-muted">{t.key}</td>
                <td className="py-0.5 break-all">{String(t.value)}</td>
              </tr>
            ))}
        </tbody>
      </table>
    </div>
  );
}

function SpanDetail({ row, trace, t0, actions }: { row: SpanRow; trace: JaegerTrace; t0: number; actions?: (row: SpanRow) => ReactNode }) {
  const s = row.span;
  const parent = s.references.find((r) => r.refType === "CHILD_OF")?.spanID;
  const status = tag(s, "otel.status_description");
  return (
    <div className="mx-2 mb-2 space-y-3 rounded-lg border border-line bg-panel p-3">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        <Fact label="Service">{row.service}</Fact>
        <Fact label="Kind">{tag(s, "span.kind") ?? "internal"}</Fact>
        <Fact label="Duration">{ms(s.duration / 1000)}</Fact>
        <Fact label="Starts at">+{ms((s.startTime - t0) / 1000)}</Fact>
        <Fact label="Span">{s.spanID}</Fact>
        <Fact label="Parent">{parent ?? "none (root)"}</Fact>
      </div>
      {row.error && (
        <p className="flex items-center gap-1.5 rounded-md bg-bad/10 px-2 py-1 text-xs text-bad">
          <CircleAlert className="size-3.5" /> {status || "Failed"}
        </p>
      )}
      {actions && <div className="flex flex-wrap gap-2">{actions(row)}</div>}
      <Tags title="Attributes" tags={s.tags.filter((t) => !SHOWN.has(t.key))} />
      {s.logs.length > 0 && (
        <div>
          <h4 className="mb-1 text-xs font-medium text-muted">Events</h4>
          <ul className="space-y-1 font-mono text-xs">
            {s.logs.map((l, i) => {
              const name = l.fields.find((f) => f.key === "event")?.value;
              const rest = l.fields.filter((f) => f.key !== "event");
              return (
                <li key={i}>
                  <span className="text-muted">+{ms((l.timestamp - t0) / 1000)}</span> <span className="font-medium">{String(name ?? "event")}</span>{" "}
                  {rest.map((f) => (
                    <span key={f.key} className="mr-2">
                      <span className="text-tok-key">{f.key}=</span>
                      <span className="text-tok-str">{String(f.value)}</span>
                    </span>
                  ))}
                </li>
              );
            })}
          </ul>
        </div>
      )}
      <Tags title="Resource" tags={trace.processes[s.processID]?.tags ?? []} />
    </div>
  );
}

/** Waterfall draws a trace's spans as a tree beside a timeline. */
export function Waterfall({
  trace,
  rows,
  t0,
  total,
  actions,
}: {
  trace: JaegerTrace;
  rows: SpanRow[];
  /** Trace start, microseconds. */
  t0: number;
  /** Trace length, microseconds. */
  total: number;
  actions?: (row: SpanRow) => ReactNode;
}) {
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [open, setOpen] = useState<string | null>(null);
  const visible = useMemo(() => rows.filter((r) => !r.ancestors.some((a) => collapsed.has(a))), [rows, collapsed]);
  const pct = (us: number) => `${(us / (total || 1)) * 100}%`;
  const toggle = (id: string) =>
    setCollapsed((c) => {
      const n = new Set(c);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });

  return (
    <div className="text-xs">
      <div className="sticky top-[var(--header-h,3rem)] z-10 flex border-b border-line bg-panel py-1.5 text-[10px] text-muted">
        <div className="flex w-[38%] shrink-0 items-center gap-2 px-3">
          <span>Service and operation</span>
          {collapsed.size > 0 ? (
            <button type="button" className="text-accent" onClick={() => setCollapsed(new Set())}>
              Expand all
            </button>
          ) : (
            <button
              type="button"
              className="text-accent"
              onClick={() => setCollapsed(new Set(rows.filter((r) => r.children && r.depth > 0).map((r) => r.span.spanID)))}
            >
              Collapse all
            </button>
          )}
        </div>
        <div className="relative flex-1 pr-3">
          {[0, 0.25, 0.5, 0.75].map((f) => (
            <span key={f} className="absolute" style={{ left: `${f * 100}%` }}>
              {f ? ms((total * f) / 1000) : "0"}
            </span>
          ))}
          <span className="absolute right-3">{ms(total / 1000)}</span>
        </div>
      </div>
      {visible.map((r) => {
        const s = r.span;
        const color = serviceColor(r.service);
        const left = (s.startTime - t0) / (total || 1);
        const right = left + s.duration / (total || 1);
        // The duration goes after the bar when there's room, else before it, else inside its end.
        const place = right < 0.85 ? "after" : left > 0.15 ? "before" : "inside";
        const isOpen = open === s.spanID;
        return (
          <div key={s.spanID} className={`border-b border-line/60 ${isOpen ? "bg-panel-2/60" : ""}`}>
            <div className="group flex h-7 cursor-pointer items-center hover:bg-panel-2" onClick={() => setOpen(isOpen ? null : s.spanID)}>
              <div className="flex w-[38%] shrink-0 items-center gap-1 overflow-hidden pr-2" style={{ paddingLeft: 8 + r.depth * 14 }}>
                {r.children ? (
                  <button
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation();
                      toggle(s.spanID);
                    }}
                    className="flex shrink-0 items-center text-muted hover:text-fg"
                    title={collapsed.has(s.spanID) ? `Show ${r.children} children` : "Hide children"}
                  >
                    {collapsed.has(s.spanID) ? <ChevronRight className="size-3.5" /> : <ChevronDown className="size-3.5" />}
                  </button>
                ) : (
                  <span className="w-3.5 shrink-0" />
                )}
                <span className="h-3.5 w-1 shrink-0 rounded-full" style={{ background: color }} />
                <span className="shrink-0 text-muted">{r.service}</span>
                <span className={`truncate font-medium ${r.error ? "text-bad" : ""}`} title={s.operationName}>
                  {s.operationName}
                </span>
                {r.error && <CircleAlert className="size-3.5 shrink-0 text-bad" />}
                {collapsed.has(s.spanID) && <span className="shrink-0 rounded bg-panel-2 px-1 text-[10px] text-muted">+{r.children}</span>}
              </div>
              <div className="relative h-full flex-1 border-l border-line/60 mr-3">
                {[0.25, 0.5, 0.75].map((f) => (
                  <span key={f} className="absolute inset-y-0 w-px bg-line/50" style={{ left: `${f * 100}%` }} />
                ))}
                <div
                  className={`absolute top-1.5 h-4 rounded-sm ${r.error ? "ring-1 ring-bad" : ""}`}
                  style={{
                    left: pct(s.startTime - t0),
                    width: `max(2px, ${pct(s.duration)})`,
                    background: r.error ? "var(--bad)" : color,
                    opacity: 0.85,
                  }}
                >
                  {s.logs.map((l, i) => (
                    <span
                      key={i}
                      className="absolute top-0 h-full w-0.5 bg-fg/70"
                      style={{ left: `${((l.timestamp - s.startTime) / (s.duration || 1)) * 100}%` }}
                    />
                  ))}
                </div>
                <span
                  className={`absolute top-1.5 whitespace-nowrap px-1 text-[10px] leading-4 ${place === "inside" ? "font-medium text-[#0b1020]" : "text-muted"}`}
                  style={
                    place === "after"
                      ? { left: `calc(${pct(s.startTime - t0 + s.duration)} + 2px)` }
                      : place === "before"
                        ? { right: `calc(${(1 - left) * 100}% + 2px)` }
                        : { right: `calc(${(1 - right) * 100}% + 2px)` }
                  }
                >
                  {ms(s.duration / 1000)}
                </span>
              </div>
            </div>
            {isOpen && <SpanDetail row={r} trace={trace} t0={t0} actions={actions} />}
          </div>
        );
      })}
    </div>
  );
}

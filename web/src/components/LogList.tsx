import { useState } from "react";
import type { LogEntry } from "../lib/api";

const HIDDEN = new Set(["_msg", "_time", "_stream", "_stream_id"]);

export function level(e: LogEntry): "error" | "warn" | "info" | "debug" | "" {
  const raw = (e.severity_text ?? e.level ?? e["log.level"] ?? "").toLowerCase();
  if (raw.startsWith("err") || raw === "fatal" || raw === "panic" || raw === "critical") return "error";
  if (raw.startsWith("warn")) return "warn";
  if (raw.startsWith("debug") || raw === "trace") return "debug";
  if (raw) return "info";
  const msg = e._msg ?? "";
  if (/\blevel=(error|fatal)|\b(ERROR|FATAL|PANIC)\b|"level":"error"/.test(msg)) return "error";
  if (/\blevel=warn|\bWARN(ING)?\b|"level":"warn/.test(msg)) return "warn";
  return "";
}

const LEVEL_COLOR = { error: "bg-bad", warn: "bg-warn", info: "bg-accent", debug: "bg-muted", "": "bg-line" };

function source(e: LogEntry) {
  const ns = e["k8s.namespace.name"];
  const pod = e["k8s.pod.name"];
  const obj = e["k8s.object.name"];
  if (ns && pod) return `${ns}/${pod}`;
  if (obj) return `${ns ? ns + "/" : ""}${e["k8s.object.kind"] ?? ""} ${obj}`.trim();
  return e["k8s.node.name"] ?? "";
}

export function LogList({ entries, maxHeight }: { entries: LogEntry[]; maxHeight?: string }) {
  const [open, setOpen] = useState<number | null>(null);
  return (
    <div className="overflow-auto font-mono text-xs" style={{ maxHeight }}>
      {entries.map((e, i) => {
        const lv = level(e);
        return (
          <div key={i} className="border-b border-line last:border-0">
            <button
              type="button"
              onClick={() => setOpen(open === i ? null : i)}
              className="flex w-full items-start gap-2 py-1 text-left hover:bg-panel-2"
            >
              <span className={`mt-1 h-3 w-1 shrink-0 rounded-full ${LEVEL_COLOR[lv]}`} />
              <span className="shrink-0 text-muted tabular-nums">{new Date(e._time).toLocaleTimeString()}</span>
              <span className="hidden w-56 shrink-0 truncate text-accent md:inline" title={source(e)}>
                {source(e)}
              </span>
              <span className={`min-w-0 ${open === i ? "whitespace-pre-wrap break-all" : "truncate"}`}>{e._msg}</span>
            </button>
            {open === i && (
              <dl className="mb-2 ml-4 grid grid-cols-[minmax(8rem,auto)_1fr] gap-x-4 gap-y-0.5 rounded-md bg-panel-2 p-2">
                <dt className="text-muted">_time</dt>
                <dd className="break-all">{e._time}</dd>
                {Object.entries(e)
                  .filter(([k]) => !HIDDEN.has(k))
                  .sort(([a], [b]) => a.localeCompare(b))
                  .map(([k, v]) => (
                    <div key={k} className="contents">
                      <dt className="text-muted">{k}</dt>
                      <dd className="break-all">{v}</dd>
                    </div>
                  ))}
              </dl>
            )}
          </div>
        );
      })}
    </div>
  );
}

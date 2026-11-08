import { useQuery } from "@tanstack/react-query";
import { Pause, Play, Search } from "lucide-react";
import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { LogList } from "../components/LogList";
import { api, tailLogs, type LogEntry } from "../lib/api";
import { useRange } from "../lib/hooks";

const MAX_TAIL = 1000;

function quote(v: string) {
  return `"${v.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
}

/** buildQuery combines the free-text query with the dropdown filters. */
export function buildQuery(text: string, namespace: string, level: string) {
  const parts = [text.trim() || "*"];
  if (namespace) parts.push(`k8s.namespace.name:${quote(namespace)}`);
  if (level === "error") parts.push(`(severity_text:~"(?i)^(error|fatal|panic|critical)" OR _msg:~"(?i)(level=(error|fatal)|\\b(ERROR|FATAL|PANIC)\\b)")`);
  if (level === "warn") parts.push(`(severity_text:~"(?i)^(warn|error|fatal)" OR _msg:~"(?i)(level=(warn|error|fatal)|\\b(WARN|WARNING|ERROR|FATAL)\\b)")`);
  return parts.join(" ");
}

export function LogsPage() {
  const [params, setParams] = useSearchParams();
  const range = useRange();
  const text = params.get("q") ?? "";
  const namespace = params.get("ns") ?? "";
  const level = params.get("level") ?? "";
  const [draft, setDraft] = useState(text);
  const [live, setLive] = useState(false);
  const [tail, setTail] = useState<LogEntry[]>([]);
  const query = buildQuery(text, namespace, level);

  const update = (k: string, v: string) =>
    setParams(
      (p) => {
        if (v) p.set(k, v);
        else p.delete(k);
        return p;
      },
      { replace: true },
    );

  const end = Math.floor(Date.now() / 1000);
  const results = useQuery({
    queryKey: ["logs", query, range.seconds],
    queryFn: ({ signal }) => api.logs(query, end - range.seconds, end, 500, signal),
    enabled: !live,
  });
  const namespaces = useQuery({
    queryKey: ["ns", range.seconds],
    queryFn: ({ signal }) => api.fieldValues("*", "k8s.namespace.name", end - range.seconds, end, signal),
    staleTime: 60_000,
  });

  useEffect(() => {
    if (!live) return;
    const ctl = new AbortController();
    setTail([]);
    tailLogs(query, (e) => setTail((t) => [e, ...t].slice(0, MAX_TAIL)), ctl.signal).catch(() => {});
    return () => ctl.abort();
  }, [live, query]);

  const entries = live ? tail : (results.data ?? []);
  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold tracking-tight">Logs</h1>
      <form
        className="flex flex-wrap gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          update("q", draft);
        }}
      >
        <div className="flex min-w-64 flex-1 items-center gap-2 rounded-lg border border-line bg-panel px-3">
          <Search className="size-4 text-muted" />
          <input
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder='LogsQL, e.g. error, _msg:"timeout", k8s.pod.name:gridtie*'
            className="w-full bg-transparent py-2 font-mono text-sm outline-none"
          />
        </div>
        <select value={namespace} onChange={(e) => update("ns", e.target.value)} className="rounded-lg border border-line bg-panel px-2 text-sm">
          <option value="">All namespaces</option>
          {(namespaces.data ?? [])
            .map((v) => v.value)
            .filter(Boolean)
            .sort()
            .map((v) => (
              <option key={v}>{v}</option>
            ))}
        </select>
        <select value={level} onChange={(e) => update("level", e.target.value)} className="rounded-lg border border-line bg-panel px-2 text-sm">
          <option value="">Any level</option>
          <option value="warn">Warnings and errors</option>
          <option value="error">Errors</option>
        </select>
        <button
          type="button"
          onClick={() => setLive(!live)}
          className={`flex items-center gap-1.5 rounded-lg border px-3 text-sm ${live ? "border-accent text-accent" : "border-line bg-panel text-muted hover:text-fg"}`}
        >
          {live ? <Pause className="size-4" /> : <Play className="size-4" />} {live ? "Live" : "Tail"}
        </button>
      </form>
      <p className="font-mono text-xs text-muted">{query}</p>
      <div className="rounded-xl border border-line bg-panel p-3">
        {results.error && !live ? (
          <p className="py-6 text-center text-sm text-bad">{String(results.error)}</p>
        ) : entries.length ? (
          <LogList entries={entries} />
        ) : (
          <p className="py-10 text-center text-sm text-muted">
            {live ? "Waiting for new lines…" : results.isFetching ? "Searching…" : "No logs match"}
          </p>
        )}
      </div>
      {!live && results.data && (
        <p className="text-xs text-muted">{results.data.length >= 500 ? "Showing the newest 500 lines" : `${results.data.length} lines`}</p>
      )}
    </div>
  );
}

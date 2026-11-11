import { X } from "lucide-react";
import { useState } from "react";
import type { Scope } from "../lib/api";
import { useMe } from "../lib/hooks";
import { lookBack } from "../lib/time";
import { button, field, seg } from "./ui";

type Signal = "metrics" | "logs" | "traces";
const signals: { id: Signal; label: string }[] = [
  { id: "metrics", label: "Metrics" },
  { id: "logs", label: "Logs" },
  { id: "traces", label: "Traces" },
];

/** A filter is one "only data with…" choice, written in each signal's own query language. */
export interface Filter {
  label: string;
  metrics?: string;
  logs?: string;
  traces?: string;
}

/** Presets fill in each signal's name for the same label, e.g. a namespace. */
const presets: Record<string, { label: string; make: (v: string) => Filter }> = {
  namespace: {
    label: "Namespace",
    make: (v) => ({
      label: `namespace=${v}`,
      metrics: `{k8s_namespace_name=${JSON.stringify(v)}}`,
      logs: `k8s.namespace.name:=${JSON.stringify(v)}`,
      traces: `"resource_attr:k8s.namespace.name":=${JSON.stringify(v)}`,
    }),
  },
  service: {
    label: "Service",
    make: (v) => ({
      label: `service=${v}`,
      metrics: `{service_name=${JSON.stringify(v)}}`,
      logs: `service.name:=${JSON.stringify(v)}`,
      traces: `"resource_attr:service.name":=${JSON.stringify(v)}`,
    }),
  },
  app: {
    label: "Browser app",
    make: (v) => ({
      label: `browser app=${v}`,
      metrics: `{app=${JSON.stringify(v)}}`,
      logs: `app:=${JSON.stringify(v)}`,
    }),
  },
  host: {
    label: "Host",
    make: (v) => ({
      label: `host=${v}`,
      metrics: `{k8s_node_name=${JSON.stringify(v)}}`,
      logs: `k8s.node.name:=${JSON.stringify(v)}`,
      traces: `"resource_attr:host.name":=${JSON.stringify(v)}`,
    }),
  },
};

export const capabilities: { id: string; label: string; write?: boolean }[] = [
  { id: "alerts", label: "See alerts and their history" },
  { id: "runbooks", label: "Read runbooks" },
  { id: "hosts", label: "See hosts, their pods and maintenance" },
  { id: "dashboards", label: "See dashboards" },
  { id: "storage", label: "See storage use" },
  {
    id: "write:runbook-notes",
    label: "Note how an alert was fixed",
    write: true,
  },
  {
    id: "write:alerts",
    label: "Acknowledge, take and note alerts",
    write: true,
  },
  { id: "write:silences", label: "Silence alerts", write: true },
  { id: "write:maintenance", label: "Set host maintenance", write: true },
  { id: "write:runbooks", label: "Edit runbooks", write: true },
];

type Mode = "none" | "some" | "all";

/** scopeOf turns the form's choices into a scope. */
export function scopeOf(modes: Record<Signal, Mode>, filters: Filter[], allow: string[], maxRange = 0): Scope {
  const scope: Scope = { allow };
  if (maxRange) scope.maxRange = maxRange;
  for (const { id } of signals) {
    if (modes[id] === "all") scope[id] = ["*"];
    if (modes[id] === "some") scope[id] = filters.flatMap((f) => (f[id] ? [f[id]] : []));
  }
  return scope;
}

/** describe says in a line what a scope allows. */
export function describe(scope: Scope) {
  const reads = signals
    .filter((s) => scope[s.id]?.length)
    .map((s) =>
      scope[s.id]?.includes("*") ? `all ${s.id}` : `${s.id} (${scope[s.id]?.length} filter${scope[s.id]?.length === 1 ? "" : "s"})`,
    );
  const rest = (scope.allow ?? []).map((a) => capabilities.find((c) => c.id === a)?.label.toLowerCase() ?? a);
  const range = scope.maxRange ? [`looking back up to ${lookBack(scope.maxRange)}`] : [];
  return [...reads, ...rest, ...range].join(", ") || "nothing";
}

const ranges = [0, 1, 7, 30, 90, 365].map((d) => d * 86400);

const days = [
  { label: "30 days", n: 30 },
  { label: "90 days", n: 90 },
  { label: "1 year", n: 365 },
  { label: "Never", n: 0 },
];

/** AssistantTokenForm picks what an assistant may read, by label, and what else it may do. */
export function AssistantTokenForm({
  onCreate,
  pending,
  error,
}: {
  onCreate: (name: string, scope: Scope, expires?: string) => void;
  pending: boolean;
  error?: Error | null;
}) {
  const [name, setName] = useState("");
  const [modes, setModes] = useState<Record<Signal, Mode>>({
    metrics: "some",
    logs: "some",
    traces: "none",
  });
  const [filters, setFilters] = useState<Filter[]>([]);
  const [kind, setKind] = useState("namespace");
  const [value, setValue] = useState("");
  const [custom, setCustom] = useState<Filter>({ label: "custom" });
  const [allow, setAllow] = useState<string[]>(["alerts", "runbooks", "hosts"]);
  const [expiry, setExpiry] = useState(90);
  const [maxRange, setMaxRange] = useState(0);
  const most = useMe().data?.limits?.assistantMaxRange ?? 0;

  const scope = scopeOf(modes, filters, allow, maxRange);
  const empty = signals.filter((s) => modes[s.id] === "some" && !scope[s.id]?.length);
  const add = () => {
    const f =
      kind === "custom"
        ? {
            ...custom,
            label: [custom.metrics, custom.logs, custom.traces].filter(Boolean).join(" · "),
          }
        : presets[kind].make(value.trim());
    setFilters([...filters, f]);
    setValue("");
    setCustom({ label: "custom" });
  };
  const canAdd = kind === "custom" ? Boolean(custom.metrics || custom.logs || custom.traces) : value.trim() !== "";

  return (
    <form
      className="space-y-4 rounded-xl border border-line bg-panel p-4"
      onSubmit={(e) => {
        e.preventDefault();
        const expires = expiry ? new Date(Date.now() + expiry * 864e5).toISOString() : undefined;
        onCreate(name.trim(), scope, expires);
      }}
    >
      <label className="block space-y-1 text-sm">
        <span className="font-medium">Name</span>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. claude-code"
          maxLength={100}
          className={`${field} w-full`}
        />
      </label>

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">Data it can read</legend>
        {signals.map((s) => (
          <div key={s.id} className="flex flex-wrap items-center gap-2 text-sm">
            <span className="w-16">{s.label}</span>
            <div className={seg.wrap}>
              {(["none", "some", "all"] as Mode[]).map((m) => (
                <button
                  key={m}
                  type="button"
                  aria-pressed={modes[s.id] === m}
                  className={seg.item}
                  onClick={() => setModes({ ...modes, [s.id]: m })}
                >
                  {m === "none" ? "Nothing" : m === "some" ? "Matching" : "Everything"}
                </button>
              ))}
            </div>
          </div>
        ))}
        {signals.some((s) => modes[s.id] === "some") && (
          <div className="space-y-2 pt-1">
            <p className="text-xs text-muted">
              "Matching" reads only data with one of these labels. tremolo enforces it on the server, whatever the assistant asks.
            </p>
            {filters.length > 0 && (
              <ul className="flex flex-wrap gap-1.5">
                {filters.map((f, i) => (
                  <li key={i} className="inline-flex items-center gap-1 rounded-full border border-line px-2.5 py-0.5 font-mono text-xs">
                    {f.label}
                    <button type="button" aria-label={`Remove ${f.label}`} onClick={() => setFilters(filters.filter((_, j) => j !== i))}>
                      <X className="size-3 text-muted" />
                    </button>
                  </li>
                ))}
              </ul>
            )}
            <div className="flex flex-wrap items-center gap-2">
              <select value={kind} onChange={(e) => setKind(e.target.value)} className={field}>
                {Object.entries(presets).map(([id, p]) => (
                  <option key={id} value={id}>
                    {p.label}
                  </option>
                ))}
                <option value="custom">Custom selector</option>
              </select>
              {kind !== "custom" && (
                <input
                  value={value}
                  onChange={(e) => setValue(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      if (canAdd) add();
                    }
                  }}
                  placeholder={
                    kind === "namespace" ? "angelorooms" : kind === "service" ? "gridtie" : kind === "host" ? "gpu01" : "angelorooms"
                  }
                  className={`${field} min-w-0 flex-1`}
                />
              )}
              <button type="button" className={button("secondary")} disabled={!canAdd} onClick={add}>
                Add
              </button>
            </div>
            {kind === "custom" && (
              <div className="grid gap-2 sm:grid-cols-3">
                <input
                  value={custom.metrics ?? ""}
                  onChange={(e) =>
                    setCustom({
                      ...custom,
                      metrics: e.target.value || undefined,
                    })
                  }
                  placeholder={'Metrics: {job="x"}'}
                  className={`${field} font-mono`}
                />
                <input
                  value={custom.logs ?? ""}
                  onChange={(e) => setCustom({ ...custom, logs: e.target.value || undefined })}
                  placeholder="Logs: field:=value"
                  className={`${field} font-mono`}
                />
                <input
                  value={custom.traces ?? ""}
                  onChange={(e) =>
                    setCustom({
                      ...custom,
                      traces: e.target.value || undefined,
                    })
                  }
                  placeholder="Traces: field:=value"
                  className={`${field} font-mono`}
                />
              </div>
            )}
          </div>
        )}
      </fieldset>

      <fieldset className="space-y-1.5">
        <legend className="text-sm font-medium">What else it can do</legend>
        <div className="grid gap-x-4 gap-y-1 sm:grid-cols-2">
          {capabilities.map((c) => (
            <label key={c.id} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={allow.includes(c.id)}
                onChange={(e) => setAllow(e.target.checked ? [...allow, c.id] : allow.filter((a) => a !== c.id))}
              />
              <span className={c.write ? "text-warn-ink" : ""}>{c.label}</span>
            </label>
          ))}
        </div>
        <p className="text-xs text-muted">
          Never more than your own role allows. Assistant tokens can't see users, tokens or the audit log.
        </p>
      </fieldset>

      <div className="space-y-1">
        <label className="flex flex-wrap items-center gap-2 text-sm">
          <span className="font-medium">Looks back up to</span>
          <select value={maxRange} onChange={(e) => setMaxRange(Number(e.target.value))} className={field}>
            {ranges
              .filter((r) => r === 0 || !most || r < most)
              .map((r) => (
                <option key={r} value={r}>
                  {r === 0 ? (most ? `${lookBack(most)} (the most assistants may)` : "As far as the data goes") : lookBack(r)}
                </option>
              ))}
          </select>
        </label>
        <p className="text-xs text-muted">
          A query over a longer time range is refused with why, so the assistant can narrow it. The most is set by{" "}
          <code className="font-mono">query.assistant.maxRange</code> in the chart.
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="font-medium">Expires</span>
        <div className={seg.wrap}>
          {days.map((d) => (
            <button key={d.n} type="button" aria-pressed={expiry === d.n} className={seg.item} onClick={() => setExpiry(d.n)}>
              {d.label}
            </button>
          ))}
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <button type="submit" className={button("primary")} disabled={!name.trim() || pending || empty.length > 0}>
          Create assistant token
        </button>
        {empty.length > 0 && (
          <span className="text-xs text-muted">
            Add a filter for {empty.map((s) => s.label.toLowerCase()).join(", ")}, or pick Nothing.
          </span>
        )}
        {error && <span className="text-sm text-bad">{error.message}</span>}
      </div>
    </form>
  );
}

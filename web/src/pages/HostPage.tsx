import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Wrench } from "lucide-react";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { KindBadge, sleepNote, sleeping, STATE, untilNote } from "../components/HostTiles";
import { PanelGrid } from "../components/PanelGrid";
import { PodTable } from "../components/PodTable";
import { api, type Host } from "../lib/api";
import { ago, formatValue } from "../lib/format";
import { useHosts, useRange } from "../lib/hooks";

export function HostPage() {
  const { name = "" } = useParams();
  const hosts = useHosts();
  const range = useRange();
  const dash = useQuery({ queryKey: ["dashboard", "host"], queryFn: ({ signal }) => api.dashboard("host", signal) });
  const node = hosts.data?.find((n) => n.name === name);
  const s = node ? STATE[node.state] : undefined;

  return (
    <div className="space-y-6">
      <div>
        <Link to="/" className="inline-flex items-center gap-1 text-sm text-muted hover:text-fg">
          <ArrowLeft className="size-4" /> Hosts
        </Link>
        <h1 className="mt-2 flex items-center gap-3 text-2xl font-semibold tracking-tight">
          {name}
          {s && <span className={`rounded-full border border-line px-2 py-0.5 text-xs font-medium ${s.text}`}>{s.label}</span>}
          {node && (
            <span className="text-base font-normal">
              <KindBadge host={node} />
            </span>
          )}
        </h1>
        {node && (
          <p className="text-sm text-muted">
            {[
              node.kind === "kubernetes" ? `Kubernetes node${node.cluster ? ` in ${node.cluster}` : ""}` : "Server",
              node.source && `via ${node.source}`,
              node.roles?.join(", "),
              ...Object.entries(node.details ?? {}).map(([k, v]) => `${node.stateBy ?? ""} ${k} ${v}`.trim()),
              node.arch,
              node.cpu && `${node.cpu} cores`,
              node.memoryBytes && formatValue(node.memoryBytes, "bytes"),
              node.address,
              node.kubelet,
              node.lastSeen && `metrics ${ago(node.lastSeen)} ago`,
              node.since && `${s?.label.toLowerCase()} for ${ago(node.since)}`,
            ]
              .filter(Boolean)
              .join(" · ")}
          </p>
        )}
        {node && sleeping(node.state) && (
          <p className="mt-3 rounded-lg border border-line bg-panel px-3 py-2 text-sm">
            {sleepNote(node.state)}
            {node.stateBy && node.stateBy !== "kubernetes" ? ` by ${node.stateBy}` : ""}
            {node.details?.group ? ` (group ${node.details.group})` : ""}. It wakes when it's needed; charts show a gap while it sleeps.
          </p>
        )}
        {node && <Maintenance host={node} />}
      </div>
      {node?.kind === "kubernetes" && node.state !== "off" && <PodTable host={name} />}
      <StateHistory host={name} />
      {dash.data && <PanelGrid panels={dash.data.panels} vars={{ host: name }} rangeSeconds={range.seconds} />}
    </div>
  );
}

/** stateQuery is the LogsQL for one host's state changes. */
export function stateQuery(host: string) {
  return `stream:="tremolo-hosts" host:=${JSON.stringify(host)}`;
}

/** StateHistory lists a host's state changes over the last month, newest first. */
function StateHistory({ host }: { host: string }) {
  const [now] = useState(() => Math.floor(Date.now() / 1000));
  const [open, setOpen] = useState(false);
  const changes = useQuery({
    queryKey: ["host-states", host, now],
    queryFn: ({ signal }) => api.logs(stateQuery(host), now - 30 * 86400, now + 60, 200, signal),
  });
  const rows = changes.data ?? [];
  const shown = open ? rows : rows.slice(0, 5);
  return (
    <section className="space-y-2">
      <h2 className="font-semibold">State changes</h2>
      <ul className="divide-y divide-line/60 rounded-xl border border-line bg-panel">
        {shown.map((e, i) => {
          const st = STATE[e.state as Host["state"]];
          return (
            <li key={i} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 px-3 py-1.5 text-sm">
              <span className="w-40 shrink-0 font-mono text-xs text-muted">{new Date(e._time).toLocaleString()}</span>
              <span>
                {e.from && <span className="text-muted">{STATE[e.from as Host["state"]]?.label.toLowerCase() ?? e.from} → </span>}
                <span className={`font-medium ${st?.text ?? ""}`}>{st?.label.toLowerCase() ?? e.state}</span>
              </span>
              {e.state_by && <span className="text-xs text-muted">by {e.state_by}</span>}
              {e.reason && <span className="text-xs text-muted">{e.reason}</span>}
            </li>
          );
        })}
        {!rows.length && (
          <li className="px-3 py-3 text-sm text-muted">
            {changes.isLoading ? "Loading…" : "No changes recorded in the last 30 days. tremolo records each host's state as it changes."}
          </li>
        )}
      </ul>
      {rows.length > 5 && (
        <button type="button" onClick={() => setOpen(!open)} className="text-sm text-accent">
          {open ? "Fewer" : `All ${rows.length}`}
        </button>
      )}
    </section>
  );
}

const DURATIONS: { label: string; hours?: number }[] = [
  { label: "1 hour", hours: 1 },
  { label: "4 hours", hours: 4 },
  { label: "1 day", hours: 24 },
  { label: "1 week", hours: 168 },
  { label: "Until I end it" },
];

/** Maintenance shows a host's maintenance window and lets a person start or end one. */
function Maintenance({ host }: { host: Host }) {
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const [hours, setHours] = useState<number | undefined>(1);
  const [note, setNote] = useState("");
  const done = () => {
    setOpen(false);
    void client.invalidateQueries({ queryKey: ["hosts"] });
  };
  const start = useMutation({
    mutationFn: () => api.setMaintenance(host.name, hours ? new Date(Date.now() + hours * 3600_000).toISOString() : undefined, note.trim()),
    onSuccess: done,
  });
  const end = useMutation({ mutationFn: () => api.clearMaintenance(host.name), onSuccess: done });
  const error = start.error ?? end.error;

  if (host.state === "maintenance") {
    const manual = host.stateBy === "maintenance";
    return (
      <div className="mt-3 flex flex-wrap items-center gap-3 rounded-lg border border-line bg-panel px-3 py-2 text-sm">
        <Wrench className="size-4 text-sleep" />
        <span>
          In maintenance {manual ? untilNote(host.until) : `(${host.reason || host.stateBy})`}
          {manual && host.reason ? `: ${host.reason}` : ""}.
        </span>
        {manual && (
          <button
            type="button"
            className="ml-auto rounded-md border border-line px-2 py-0.5 text-xs hover:border-accent"
            onClick={() => end.mutate()}
          >
            {end.isPending ? "Ending…" : "End maintenance"}
          </button>
        )}
        {error && <span className="w-full text-xs text-bad">{String(error)}</span>}
      </div>
    );
  }
  if (!open) {
    return (
      <button
        type="button"
        className="mt-3 inline-flex items-center gap-1.5 rounded-md border border-line px-2 py-1 text-xs text-muted hover:border-accent hover:text-fg"
        onClick={() => setOpen(true)}
      >
        <Wrench className="size-3.5" /> Start maintenance…
      </button>
    );
  }
  return (
    <form
      className="mt-3 flex flex-wrap items-center gap-2 rounded-lg border border-line bg-panel px-3 py-2 text-sm"
      onSubmit={(e) => {
        e.preventDefault();
        start.mutate();
      }}
    >
      <Wrench className="size-4 text-muted" />
      <select
        value={hours ?? ""}
        onChange={(e) => setHours(e.target.value ? Number(e.target.value) : undefined)}
        className="rounded-md border border-line bg-panel-2 px-2 py-1 text-xs"
        aria-label="How long"
      >
        {DURATIONS.map((d) => (
          <option key={d.label} value={d.hours ?? ""}>
            {d.label}
          </option>
        ))}
      </select>
      <input
        value={note}
        onChange={(e) => setNote(e.target.value)}
        maxLength={500}
        placeholder="What's happening (optional)"
        className="min-w-48 flex-1 rounded-md border border-line bg-panel-2 px-2 py-1 text-xs"
      />
      <button
        type="submit"
        disabled={start.isPending}
        className="rounded-md bg-accent px-2.5 py-1 text-xs font-medium text-white disabled:opacity-60"
      >
        {start.isPending ? "Starting…" : "Start"}
      </button>
      <button type="button" className="text-xs text-muted hover:text-fg" onClick={() => setOpen(false)}>
        Cancel
      </button>
      {error && <span className="w-full text-xs text-bad">{String(error)}</span>}
    </form>
  );
}

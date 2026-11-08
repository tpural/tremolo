import { useQuery } from "@tanstack/react-query";
import { Boxes, Server } from "lucide-react";
import { useState } from "react";
import { HostTiles, sleeping } from "../components/HostTiles";
import { PanelGrid } from "../components/PanelGrid";
import { api, type Host } from "../lib/api";
import { useHosts, useRange } from "../lib/hooks";
import { HOST_CPU, HOST_LABEL, HOST_MEM } from "../lib/queries";

function byHost(query: string) {
  return {
    queryKey: ["instant", query],
    queryFn: async ({ signal }: { signal: AbortSignal }) =>
      new Map((await api.instant(query, undefined, signal)).map((s) => [s.metric[HOST_LABEL], Number(s.value[1])])),
    refetchInterval: 15_000,
  };
}

/** A host needs attention when it is down and nothing says that is on purpose. */
export function needsAttention(h: Host) {
  return h.state === "notready" || h.state === "unreachable";
}

/** groups puts Kubernetes nodes under their cluster, then servers. */
export function groups(hosts: Host[]): { key: string; title: string; kind: Host["kind"]; hosts: Host[] }[] {
  const clusters = new Map<string, Host[]>();
  const servers: Host[] = [];
  for (const h of hosts) {
    if (h.kind === "kubernetes") clusters.set(h.cluster ?? "", [...(clusters.get(h.cluster ?? "") ?? []), h]);
    else servers.push(h);
  }
  const out = [...clusters]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, list]) => ({
      key: `k8s/${name}`,
      title: name ? `Kubernetes · ${name}` : "Kubernetes",
      kind: "kubernetes" as Host["kind"],
      hosts: list,
    }));
  if (servers.length) out.push({ key: "servers", title: "Servers", kind: "server", hosts: servers });
  return out;
}

type Filter = "all" | "kubernetes" | "server";

export function Overview() {
  const hosts = useHosts();
  const range = useRange();
  const [filter, setFilter] = useState<Filter>("all");
  const cpu = useQuery(byHost(HOST_CPU));
  const mem = useQuery(byHost(HOST_MEM));
  const dash = useQuery({ queryKey: ["dashboard", "overview"], queryFn: ({ signal }) => api.dashboard("overview", signal), retry: false });

  const list = hosts.data ?? [];
  const trouble = list.filter(needsAttention);
  const shown = groups(list.filter((h) => filter === "all" || h.kind === filter));
  const kinds = new Set(list.map((h) => h.kind));

  return (
    <div className="space-y-6">
      <Status loading={!hosts.data} error={hosts.error} hosts={list} trouble={trouble} />
      {kinds.size > 1 && (
        <div className="flex gap-1 text-sm">
          {(["all", "kubernetes", "server"] as const).map((f) => (
            <button
              key={f}
              type="button"
              onClick={() => setFilter(f)}
              className={`rounded-lg px-3 py-1 ${filter === f ? "bg-panel-2 font-medium text-fg" : "text-muted hover:text-fg"}`}
            >
              {f === "all" ? "All" : f === "kubernetes" ? "Kubernetes" : "Servers"}
            </button>
          ))}
        </div>
      )}
      {shown.map((g) => (
        <section key={g.key} className="space-y-3">
          <h2 className="flex items-center gap-2 text-sm font-medium text-muted">
            {g.kind === "kubernetes" ? <Boxes className="size-4" /> : <Server className="size-4" />}
            {g.title}
            <span className="text-xs">({g.hosts.length})</span>
          </h2>
          <HostTiles hosts={g.hosts} cpu={cpu.data ?? new Map()} mem={mem.data ?? new Map()} />
        </section>
      ))}
      {dash.data && <PanelGrid panels={dash.data.panels} vars={{}} rangeSeconds={range.seconds} />}
    </div>
  );
}

function Status({ loading, error, hosts, trouble }: { loading: boolean; error: unknown; hosts: Host[]; trouble: Host[] }) {
  const total = hosts.length;
  const calling = trouble.length > 0;
  let title = "All quiet";
  const asleep = hosts.filter((h) => sleeping(h.state)).length;
  const maintenance = hosts.filter((h) => h.state === "maintenance").length;
  let detail = [`${total} host${total === 1 ? "" : "s"}`, asleep && `${asleep} sleeping`, maintenance && `${maintenance} in maintenance`]
    .filter(Boolean)
    .join(", ")
    .concat(", none need attention.");
  if (loading) [title, detail] = ["Listening…", "Loading hosts."];
  else if (error) [title, detail] = ["Can't reach tremolo's storage", String(error)];
  else if (!total) [title, detail] = ["No hosts yet", "Send host metrics to tremolo, or install tremolo-kubernetes-collector."];
  else if (calling) {
    title = `${trouble.length} host${trouble.length === 1 ? " needs" : "s need"} attention`;
    detail = trouble.map((h) => `${h.name} (${h.state === "notready" ? "not ready" : h.state})`).join(", ");
  }
  return (
    <div className={`flex items-center gap-4 rounded-2xl border p-4 ${calling ? "border-bad/40 bg-bad/5" : "border-line bg-panel"}`}>
      <div className="flex size-16 shrink-0 items-center justify-center rounded-xl bg-[#0b1020]">
        <img src={calling ? "/loon-calling.png" : "/loon-quiet.png"} alt="" className="w-14" />
      </div>
      <div className="min-w-0">
        <h1 className={`text-xl font-semibold tracking-tight ${calling ? "text-bad" : ""}`}>{title}</h1>
        <p className="truncate text-sm text-muted">{detail}</p>
      </div>
    </div>
  );
}

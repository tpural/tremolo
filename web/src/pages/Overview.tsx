import { useQuery } from "@tanstack/react-query";
import { HostTiles } from "../components/HostTiles";
import { PanelGrid } from "../components/PanelGrid";
import { api } from "../lib/api";
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

export function Overview() {
  const hosts = useHosts();
  const range = useRange();
  const cpu = useQuery(byHost(HOST_CPU));
  const mem = useQuery(byHost(HOST_MEM));
  const dash = useQuery({ queryKey: ["dashboard", "overview"], queryFn: ({ signal }) => api.dashboard("overview", signal), retry: false });
  const counts = new Map<string, number>();
  for (const n of hosts.data ?? []) counts.set(n.state, (counts.get(n.state) ?? 0) + 1);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Hosts</h1>
        <p className="text-sm text-muted">
          {hosts.data
            ? hosts.data.length
              ? [...counts].map(([s, n]) => `${n} ${s === "notready" ? "not ready" : s}`).join(" · ")
              : "No hosts yet. Send host metrics to tremolo, or install tremolo-kubernetes-collector."
            : hosts.error
              ? String(hosts.error)
              : "Loading hosts…"}
        </p>
      </div>
      {hosts.data && <HostTiles hosts={hosts.data} cpu={cpu.data ?? new Map()} mem={mem.data ?? new Map()} />}
      {dash.data && <PanelGrid panels={dash.data.panels} vars={{}} rangeSeconds={range.seconds} />}
    </div>
  );
}

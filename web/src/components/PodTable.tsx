import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { api, type Pod } from "../lib/api";
import { ago, formatValue } from "../lib/format";
import { podCPU, podMemory } from "../lib/queries";

function usage(query: string) {
  return {
    queryKey: ["instant", query],
    queryFn: async ({ signal }: { signal: AbortSignal }) =>
      new Map((await api.instant(query, undefined, signal)).map((s) => [`${s.metric.namespace}/${s.metric.pod}`, Number(s.value[1])])),
    refetchInterval: 15_000,
  };
}

/** cores formats CPU cores as millicores, like Kubernetes requests. */
function cores(v?: number) {
  if (v == null) return "–";
  const m = v * 1000;
  return m >= 1000 ? `${(m / 1000).toFixed(m >= 10_000 ? 0 : 1)}` : `${m < 1 ? m.toFixed(1) : m.toFixed(0)}m`;
}

/** podHealth is how a pod is doing, for its status dot. */
export function podHealth(p: Pod): "good" | "warn" | "bad" | "done" {
  if (p.phase === "Succeeded") return "done";
  if (p.phase === "Failed" || p.reason === "CrashLoopBackOff" || p.reason === "ImagePullBackOff" || p.reason === "ErrImagePull") return "bad";
  if (p.phase !== "Running" || p.ready < p.containers) return "warn";
  return "good";
}

const DOT = { good: "bg-good", warn: "bg-warn", bad: "bg-bad", done: "bg-muted" };

function logsLink(p: Pod) {
  return `/logs?q=${encodeURIComponent(`k8s.pod.name:"${p.name}"`)}&ns=${encodeURIComponent(p.namespace)}`;
}

export function PodTable({ host }: { host: string }) {
  const [filter, setFilter] = useState("");
  const pods = useQuery({ queryKey: ["pods", host], queryFn: ({ signal }) => api.pods(host, signal), refetchInterval: 15_000 });
  const cpu = useQuery(usage(podCPU(host)));
  const mem = useQuery(usage(podMemory(host)));
  if (pods.error) return null;
  const list = (pods.data ?? []).filter((p) => !filter || `${p.namespace}/${p.name} ${p.owner ?? ""}`.includes(filter));
  const unhealthy = (pods.data ?? []).filter((p) => podHealth(p) === "bad" || podHealth(p) === "warn").length;

  return (
    <section className="rounded-xl border border-line bg-panel p-4">
      <div className="mb-3 flex flex-wrap items-center gap-3">
        <h3 className="text-sm font-medium text-muted">Pods {pods.data && `(${pods.data.length}${unhealthy ? `, ${unhealthy} unhealthy` : ""})`}</h3>
        <input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter"
          className="ml-auto w-48 rounded-lg border border-line-strong/70 bg-panel px-2 py-1 text-sm focus:border-accent focus:outline-none"
        />
      </div>
      {!pods.data ? (
        <div className="h-24 animate-pulse rounded-lg bg-panel-2" />
      ) : !list.length ? (
        <p className="py-6 text-center text-sm text-muted">No pods</p>
      ) : (
        <div className="max-h-[28rem] overflow-auto">
          <table className="w-full table-fixed text-sm">
            <colgroup>
              <col className="w-[40%] md:w-[34%]" />
              <col className="hidden w-[22%] md:table-column" />
              <col className="w-[18%] md:w-[12%]" />
              <col className="w-16" />
              <col className="w-20" />
              <col className="hidden w-20 lg:table-column" />
              <col className="hidden w-24 lg:table-column" />
              <col className="w-20" />
            </colgroup>
            <thead className="sticky top-0 bg-panel text-left text-xs text-muted">
              <tr>
                <th className="py-1.5 pr-3 font-medium">Pod</th>
                <th className="hidden py-1.5 pr-3 font-medium md:table-cell">Workload</th>
                <th className="py-1.5 pr-3 font-medium">Status</th>
                <th className="py-1.5 pr-3 text-right font-medium">Ready</th>
                <th className="py-1.5 pr-3 text-right font-medium">Restarts</th>
                <th className="hidden py-1.5 pr-3 text-right font-medium lg:table-cell" title="Live use; hover a value for the request">
                  CPU
                </th>
                <th className="hidden py-1.5 pr-3 text-right font-medium lg:table-cell" title="Working set; hover a value for the request">
                  Memory
                </th>
                <th className="py-1.5 text-right font-medium">Age</th>
              </tr>
            </thead>
            <tbody>
              {list.map((p) => {
                const health = podHealth(p);
                const key = `${p.namespace}/${p.name}`;
                const usedCPU = cpu.data?.get(key);
                const usedMem = mem.data?.get(key);
                return (
                  <tr key={`${p.namespace}/${p.name}`} className="border-t border-line">
                    <td className="py-1.5 pr-3">
                      <Link to={logsLink(p)} className="flex min-w-0 items-center gap-2 hover:text-accent" title="Show logs">
                        <span className={`size-2 shrink-0 rounded-full ${DOT[health]}`} />
                        <span className="truncate" title={`${p.namespace}/${p.name}`}>
                          <span className="text-muted">{p.namespace}/</span>
                          {p.name}
                        </span>
                      </Link>
                    </td>
                    <td className="hidden truncate py-1.5 pr-3 text-muted md:table-cell" title={p.owner}>
                      {p.owner}
                    </td>
                    <td className={`truncate py-1.5 pr-3 ${health === "bad" ? "text-bad" : health === "warn" ? "text-warn" : "text-muted"}`}>
                      {p.reason || p.phase}
                    </td>
                    <td className="py-1.5 pr-3 text-right font-mono tabular-nums">
                      {p.ready}/{p.containers}
                    </td>
                    <td className={`py-1.5 pr-3 text-right font-mono tabular-nums ${p.restarts > 0 ? "text-warn" : ""}`}>{p.restarts}</td>
                    <td
                      className="hidden py-1.5 pr-3 text-right font-mono tabular-nums lg:table-cell"
                      title={p.cpuRequestMillis ? `requests ${p.cpuRequestMillis}m` : "no request"}
                    >
                      {cores(usedCPU)}
                    </td>
                    <td
                      className="hidden py-1.5 pr-3 text-right font-mono tabular-nums lg:table-cell"
                      title={p.memoryRequestBytes ? `requests ${formatValue(p.memoryRequestBytes, "bytes")}` : "no request"}
                    >
                      {usedMem == null ? "–" : formatValue(usedMem, "bytes")}
                    </td>
                    <td className="py-1.5 text-right text-muted tabular-nums">{ago(p.started)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { Link, useParams } from "react-router";
import { STATE } from "../components/HostTiles";
import { PanelGrid } from "../components/PanelGrid";
import { api } from "../lib/api";
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
        </h1>
        {node && (
          <p className="text-sm text-muted">
            {[
              node.roles?.join(", "),
              node.group && `possum group ${node.group}`,
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
        {node?.state === "asleep" && (
          <p className="mt-3 rounded-lg border border-line bg-panel px-3 py-2 text-sm">
            Suspended to save power. It wakes on demand; charts show a gap while it sleeps.
          </p>
        )}
      </div>
      {dash.data && <PanelGrid panels={dash.data.panels} vars={{ host: name }} rangeSeconds={range.seconds} />}
    </div>
  );
}

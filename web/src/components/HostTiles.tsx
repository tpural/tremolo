import { Boxes, CloudOff, Moon, Server, TriangleAlert, Zap } from "lucide-react";
import { Link } from "react-router";
import type { Host, HostState } from "../lib/api";
import { ago, formatValue } from "../lib/format";

/** sleeping is true for hosts a power manager has suspended or switched off on purpose. */
export function sleeping(state: HostState) {
  return state === "asleep" || state === "off";
}

/** sleepNote says how a sleeping host is sleeping. */
export function sleepNote(state: HostState) {
  return state === "asleep" ? "Suspended to save power" : state === "off" ? "Powered off to save power" : "";
}

export const STATE: Record<HostState, { label: string; dot: string; text: string; icon: typeof Server }> = {
  ready: { label: "Ready", dot: "bg-good", text: "text-good", icon: Server },
  notready: { label: "Not ready", dot: "bg-bad", text: "text-bad", icon: TriangleAlert },
  // Both are a power manager's doing, so they read as intentional rather than broken.
  asleep: { label: "Sleeping", dot: "bg-sleep", text: "text-sleep", icon: Moon },
  off: { label: "Sleeping", dot: "bg-sleep", text: "text-sleep", icon: Moon },
  booting: { label: "Waking", dot: "bg-warn", text: "text-warn", icon: Zap },
  unreachable: { label: "Unreachable", dot: "bg-bad", text: "text-bad", icon: CloudOff },
};

export function HostTiles({ hosts, cpu, mem }: { hosts: Host[]; cpu: Map<string, number>; mem: Map<string, number> }) {
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(13rem,1fr))] gap-3">
      {hosts.map((n) => {
        const s = STATE[n.state];
        const Icon = s.icon;
        const live = n.state === "ready";
        return (
          <Link
            key={n.name}
            to={`/hosts/${encodeURIComponent(n.name)}`}
            className={`group rounded-xl border border-line bg-panel p-3 transition hover:border-accent ${live ? "" : "opacity-80"}`}
          >
            <div className="flex items-center gap-2">
              <Icon className={`size-4 ${s.text}`} />
              <span className="truncate font-medium">{n.name}</span>
              <KindBadge host={n} compact />
              <span className={`size-2 rounded-full ${s.dot}`} title={s.label} />
            </div>
            <p className="mt-1 text-xs text-muted">
              {s.label}
              {!live && n.since ? ` for ${ago(n.since)}` : ""}
              {n.group ? ` · ${n.group}` : ""}
              {n.source ? ` · via ${n.source}` : ""}
              {n.roles?.length ? ` · ${n.roles.join(", ")}` : ""}
            </p>
            {live ? (
              <div className="mt-3 space-y-1.5">
                <Meter label="CPU" value={cpu.get(n.name)} />
                <Meter label="Mem" value={mem.get(n.name)} />
                <p className="pt-1 text-xs text-muted">
                  {n.kind === "kubernetes" && n.cpu
                    ? `${n.arch} · ${n.cpu} cores · ${formatValue(n.memoryBytes ?? 0, "bytes")} · ${n.pods} pods`
                    : `seen ${ago(n.lastSeen)} ago`}
                </p>
              </div>
            ) : (
              <p className="mt-3 text-xs text-muted">
                {n.reason || sleepNote(n.state) || (n.state === "unreachable" ? `No metrics for ${ago(n.lastSeen)}` : "")}
              </p>
            )}
          </Link>
        );
      })}
    </div>
  );
}

/** KindBadge shows how a host is collected. */
export function KindBadge({ host, compact }: { host: Host; compact?: boolean }) {
  const k8s = host.kind === "kubernetes";
  const Icon = k8s ? Boxes : Server;
  return (
    <span
      className={`ml-auto inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide ${
        k8s ? "border-accent/40 text-accent" : "border-line text-muted"
      }`}
      title={k8s ? `Kubernetes node${host.cluster ? ` in ${host.cluster}` : ""}` : "Server pushing its own metrics"}
    >
      <Icon className="size-3" />
      {!compact && (k8s ? "k8s" : "server")}
    </span>
  );
}

function Meter({ label, value }: { label: string; value?: number }) {
  const pct = value == null ? 0 : Math.min(100, Math.max(0, value * 100));
  const color = pct >= 90 ? "bg-bad" : pct >= 75 ? "bg-warn" : "bg-accent";
  return (
    <div className="flex items-center gap-2 text-xs">
      <span className="w-8 text-muted">{label}</span>
      <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-panel-2">
        <div className={`h-full rounded-full ${color}`} style={{ width: `${pct}%` }} />
      </div>
      <span className="w-9 text-right font-mono tabular-nums">{value == null ? "–" : `${pct.toFixed(0)}%`}</span>
    </div>
  );
}

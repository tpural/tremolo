import type { Panel as PanelSpec } from "../lib/api";
import { Panel } from "./Panel";

export function PanelGrid({ panels, vars, rangeSeconds }: { panels: PanelSpec[]; vars: Record<string, string>; rangeSeconds: number }) {
  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-12 [&>section]:max-lg:!col-span-1">
      {panels.map((p, i) => (
        <Panel key={`${i}-${p.title}`} panel={p} vars={vars} rangeSeconds={rangeSeconds} />
      ))}
    </div>
  );
}

import { useQuery } from "@tanstack/react-query";
import { useParams, useSearchParams } from "react-router";
import { PanelGrid } from "../components/PanelGrid";
import { api } from "../lib/api";
import { useRange } from "../lib/hooks";

export function DashboardPage() {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const range = useRange();
  const dash = useQuery({ queryKey: ["dashboard", id], queryFn: ({ signal }) => api.dashboard(id, signal) });
  if (dash.error) return <p className="text-bad">{String(dash.error)}</p>;
  if (!dash.data) return null;
  const vars = Object.fromEntries((dash.data.variables ?? []).map((v) => [v, params.get(v) ?? ""]));
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{dash.data.title}</h1>
        {dash.data.description && <p className="text-sm text-muted">{dash.data.description}</p>}
      </div>
      <PanelGrid panels={dash.data.panels} vars={vars} rangeSeconds={range.seconds} />
    </div>
  );
}

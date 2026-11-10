import { useQuery } from "@tanstack/react-query";
import { useParams, useSearchParams } from "react-router";
import { PanelGrid } from "../components/PanelGrid";
import { api } from "../lib/api";
import { useRange } from "../lib/hooks";
import { ErrorNote, PageHeader, skeleton } from "../components/ui";

export function DashboardPage() {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const range = useRange();
  const dash = useQuery({ queryKey: ["dashboard", id], queryFn: ({ signal }) => api.dashboard(id, signal) });
  if (dash.error) return <ErrorNote error={dash.error} />;
  if (!dash.data) {
    return (
      <div className="space-y-6" aria-label="Loading dashboard">
        <div className={`${skeleton} h-9 w-1/3`} />
        <div className="grid gap-4 lg:grid-cols-2">
          {[0, 1, 2, 3].map((i) => (
            <div key={i} className={`${skeleton} h-56`} />
          ))}
        </div>
      </div>
    );
  }
  const vars = Object.fromEntries((dash.data.variables ?? []).map((v) => [v, params.get(v) ?? ""]));
  return (
    <div className="space-y-6">
      <PageHeader title={dash.data.title} sub={dash.data.description} />
      <PanelGrid panels={dash.data.panels} vars={vars} rangeSeconds={range.seconds} />
    </div>
  );
}

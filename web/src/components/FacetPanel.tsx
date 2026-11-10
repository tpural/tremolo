import { useQuery } from "@tanstack/react-query";
import { Minus, Plus, X } from "lucide-react";
import { useState } from "react";
import { api } from "../lib/api";
import { formatValue } from "../lib/format";
import { type FieldFilter } from "../lib/logs";

const LABELS: Record<string, string> = {
  "service.name": "Service",
  "k8s.namespace.name": "Namespace",
  "k8s.container.name": "Container",
  "k8s.pod.name": "Pod",
  "host.name": "Host",
  source: "Sender",
  app: "App",
  job: "Job",
  "k8s.object.kind": "Object kind",
  severity_text: "Severity",
};

export function facetLabel(field: string) {
  return LABELS[field] ?? field;
}

/** One facet: the top values of a field among matching lines, with counts. */
function Facet({
  field,
  query,
  start,
  end,
  filters,
  onFilter,
  onRemove,
}: {
  field: string;
  query: string;
  start: number;
  end: number;
  filters: FieldFilter[];
  onFilter: (f: FieldFilter) => void;
  onRemove?: () => void;
}) {
  const [more, setMore] = useState(false);
  const values = useQuery({
    queryKey: ["facet", field, query, start, end],
    queryFn: ({ signal }) => api.fieldValues(query, field, start, end, signal, 30),
    staleTime: 30_000,
    placeholderData: (prev) => prev,
  });
  const list = (values.data ?? []).filter((v) => v.value !== "").sort((a, b) => b.hits - a.hits);
  if (!list.length && !filters.some((f) => f.field === field)) return null;
  const max = Math.max(1, ...list.map((v) => v.hits));
  const shown = more ? list : list.slice(0, 6);
  const active = (v: string) => filters.find((f) => f.field === field && f.value === v);

  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between">
        <h4 className="text-xs font-medium uppercase tracking-wide text-muted" title={field}>
          {facetLabel(field)}
        </h4>
        {onRemove && (
          <button type="button" onClick={onRemove} title="Remove facet" className="text-muted hover:text-fg">
            <X className="size-3" />
          </button>
        )}
      </div>
      {shown.map((v) => {
        const a = active(v.value);
        return (
          <div key={v.value} className="group relative flex items-center gap-1 text-xs">
            <div
              className={`absolute inset-y-0 left-0 rounded ${a ? (a.negate ? "bg-bad/15" : "bg-accent/20") : "bg-panel-2"}`}
              style={{ width: `${(v.hits / max) * 100}%` }}
            />
            <button
              type="button"
              onClick={() => onFilter({ field, value: v.value })}
              className={`relative min-w-0 flex-1 truncate px-1.5 py-0.5 text-left ${a?.negate ? "line-through text-muted" : ""}`}
              title={`Only ${v.value}`}
            >
              {v.value}
            </button>
            <span className="relative tabular-nums text-muted group-hover:hidden">{formatValue(v.hits)}</span>
            <span className="relative hidden gap-0.5 group-focus-within:flex group-hover:flex [@media(hover:none)]:flex">
              <button
                type="button"
                title={`Only ${v.value}`}
                onClick={() => onFilter({ field, value: v.value })}
                className="text-muted hover:text-accent"
              >
                <Plus className="size-3.5" />
              </button>
              <button
                type="button"
                title={`Hide ${v.value}`}
                onClick={() => onFilter({ field, value: v.value, negate: true })}
                className="text-muted hover:text-bad"
              >
                <Minus className="size-3.5" />
              </button>
            </span>
          </div>
        );
      })}
      {list.length > 6 && (
        <button type="button" onClick={() => setMore(!more)} className="px-1.5 text-xs text-accent">
          {more ? "Fewer" : `${list.length - 6} more`}
        </button>
      )}
    </div>
  );
}

export function FacetPanel({
  fields,
  available,
  queryFor,
  start,
  end,
  filters,
  onFilter,
  onAddField,
  onRemoveField,
  custom,
}: {
  fields: string[];
  available: string[];
  queryFor: (field: string) => string;
  start: number;
  end: number;
  filters: FieldFilter[];
  onFilter: (f: FieldFilter) => void;
  onAddField: (f: string) => void;
  onRemoveField: (f: string) => void;
  custom: string[];
}) {
  const addable = available.filter((f) => !fields.includes(f)).sort();
  return (
    <div className="space-y-4">
      {fields.map((f) => (
        <Facet
          key={f}
          field={f}
          query={queryFor(f)}
          start={start}
          end={end}
          filters={filters}
          onFilter={onFilter}
          onRemove={custom.includes(f) ? () => onRemoveField(f) : undefined}
        />
      ))}
      {addable.length > 0 && (
        <select
          value=""
          onChange={(e) => e.target.value && onAddField(e.target.value)}
          className="w-full rounded-lg border border-line-strong/70 bg-panel px-2 py-1 text-xs text-muted"
        >
          <option value="">+ Facet on another field…</option>
          {addable.map((f) => (
            <option key={f} value={f}>
              {f}
            </option>
          ))}
        </select>
      )}
    </div>
  );
}

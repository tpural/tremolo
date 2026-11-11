import { useQuery } from "@tanstack/react-query";
import { Globe } from "lucide-react";
import { Link, useParams } from "react-router";
import { PanelGrid } from "../components/PanelGrid";
import { api, type FrontendApp } from "../lib/api";
import { ago } from "../lib/format";
import { useFrontendApps, useRange } from "../lib/hooks";
import { rate, show, VITALS, type Rating } from "../lib/vitals";
import { badge, badgeTone, card, EmptyState, ErrorNote, PageHeader, skeleton } from "../components/ui";

const tone: Record<Rating, string> = { good: badgeTone.good, "needs work": badgeTone.warn, poor: badgeTone.bad };

/** Vitals shows an app's p75 Web Vitals over 7 days, each rated against Google's thresholds. */
function Vitals({ app }: { app: FrontendApp }) {
  return (
    <dl className="flex flex-wrap gap-2">
      {VITALS.map((v) => {
        const m = app.vitals[v.name];
        return (
          <div key={v.name} className="min-w-24 rounded-lg border border-line px-2.5 py-1.5">
            <dt className="text-xs text-muted">{v.label} p75</dt>
            {m ? (
              <dd className="flex items-center gap-1.5">
                <span className="font-mono text-sm font-semibold">{show(v.name, m.p75)}</span>
                <span className={`${badge} ${tone[rate(v.name, m.p75)]}`}>{rate(v.name, m.p75)}</span>
              </dd>
            ) : (
              <dd className="text-sm text-muted">no data</dd>
            )}
            {m && (
              <dd className="text-[11px] text-muted">
                from {m.samples} visit{m.samples === 1 ? "" : "s"}
              </dd>
            )}
          </div>
        );
      })}
    </dl>
  );
}

/** FrontendPage lists the browser apps sending data, with today's visits and errors and their Web Vitals. */
export function FrontendPage() {
  const apps = useFrontendApps();
  return (
    <div className="space-y-6">
      <PageHeader title="Frontend" sub="Browser apps sending data through Grafana Faro: visits and errors today, Web Vitals over 7 days" />
      {apps.error && <ErrorNote error={apps.error} />}
      {!apps.data && !apps.error && <div className={`${skeleton} h-40`} />}
      {apps.data?.length === 0 && (
        <EmptyState>
          No browser app has sent data yet. Point a Faro web SDK at <code className="font-mono">/faro/collect</code> on the ingest port,
          through your site's server so the token stays private.
        </EmptyState>
      )}
      <ul className="grid gap-4 lg:grid-cols-2">
        {(apps.data ?? []).map((a) => (
          <li key={a.name}>
            <Link to={`/frontend/${encodeURIComponent(a.name)}`} className={`${card} block space-y-3 p-4 hover:border-accent`}>
              <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                <Globe className="size-4 self-center text-muted" />
                <span className="font-semibold">{a.name}</span>
                <span className="text-xs text-muted">{a.lastSeen ? `last visit ${ago(a.lastSeen)} ago` : "no visits in 7 days"}</span>
              </div>
              <div className="flex gap-6 text-sm">
                <span>
                  <span className="font-mono font-semibold">{a.views}</span> <span className="text-muted">page views today</span>
                </span>
                <span>
                  <span className={`font-mono font-semibold ${a.errors ? "text-bad" : ""}`}>{a.errors}</span>{" "}
                  <span className="text-muted">errors today</span>
                </span>
              </div>
              <Vitals app={a} />
              {a.ignorePages?.length ? <p className="text-xs text-muted">Leaving out {a.ignorePages.join(", ")}</p> : null}
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** FrontendAppPage is one app's dashboard, made by tremolo from its browser data. */
export function FrontendAppPage() {
  const { app = "" } = useParams();
  const range = useRange();
  const apps = useFrontendApps();
  const dash = useQuery({ queryKey: ["frontend", app], queryFn: ({ signal }) => api.frontendDashboard(app, signal) });
  const summary = apps.data?.find((a) => a.name === app);
  if (dash.error) return <ErrorNote error={dash.error} />;
  return (
    <div className="space-y-6">
      <PageHeader
        title={app}
        back={{ to: "/frontend", label: "Frontend" }}
        sub={
          summary?.namespace
            ? `Browser data, and its server in namespace ${summary.namespace}`
            : "Browser data: visits, errors and Web Vitals"
        }
      />
      {summary && <Vitals app={summary} />}
      {summary?.ignorePages?.length ? <p className="text-xs text-muted">Leaving out {summary.ignorePages.join(", ")}.</p> : null}
      {dash.data ? (
        <PanelGrid panels={dash.data.panels} vars={{}} rangeSeconds={range.seconds} />
      ) : (
        <div className="grid gap-4 lg:grid-cols-2">
          {[0, 1, 2, 3].map((i) => (
            <div key={i} className={`${skeleton} h-56`} />
          ))}
        </div>
      )}
    </div>
  );
}

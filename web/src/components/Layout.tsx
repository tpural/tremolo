import { Bell, CalendarClock, CircleUser, LayoutDashboard, LogOut, Monitor, Moon, ScrollText, Server, Sun, Users, Waypoints } from "lucide-react";
import { Navigate, NavLink, Outlet, useLocation, useNavigate } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, can } from "../lib/api";
import { useAlerts, useDashboards, useMe, useRange, useTheme } from "../lib/hooks";
import { RANGES } from "../lib/time";
import { ThemeContext } from "./theme";

function navClass({ isActive }: { isActive: boolean }) {
  return `flex items-center gap-2 rounded-lg px-3 py-1.5 text-sm ${isActive ? "bg-panel-2 text-fg font-medium" : "text-muted hover:text-fg"}`;
}

/** Layout is the signed-in app: everything but signing in and ack links. */
export function Layout() {
  const me = useMe();
  const loc = useLocation();
  if (me.isLoading) return null;
  if (!me.data) return <Navigate to={`/login?next=${encodeURIComponent(loc.pathname + loc.search)}`} replace />;
  return <App />;
}

function UserMenu() {
  const me = useMe();
  const client = useQueryClient();
  const navigate = useNavigate();
  const logout = useMutation({
    mutationFn: api.logout,
    onSettled: () => {
      client.clear();
      navigate("/login", { replace: true });
    },
  });
  return (
    <nav className="flex flex-col gap-0.5">
      <NavLink to="/profile" className={navClass}>
        <CircleUser className="size-4" /> {me.data?.displayName || me.data?.name}
      </NavLink>
      {can(me.data?.role, "admin") && (
        <NavLink to="/users" className={navClass}>
          <Users className="size-4" /> Users and teams
        </NavLink>
      )}
      <button type="button" onClick={() => logout.mutate()} className={navClass({ isActive: false })}>
        <LogOut className="size-4" /> Sign out
      </button>
    </nav>
  );
}

function App() {
  const dashboards = useDashboards();
  const theme = useTheme();
  const range = useRange();
  const loc = useLocation();
  const version = useQuery({ queryKey: ["version"], queryFn: api.version, staleTime: Infinity });
  const alerts = useAlerts();
  // Info alerts stay on the Alerts page; only warn and page show in the nav.
  const firing = (alerts.data?.alerts ?? []).filter((a) => a.state === "firing" && !a.silenced && !a.acked && a.severity !== "info");
  const loud = firing.some((a) => a.severity === "page");
  const badge = firing.length > 0 && (
    <span className={`ml-auto rounded-full px-1.5 text-[10px] font-semibold text-white ${loud ? "bg-bad" : "bg-warn"}`}>{firing.length}</span>
  );
  const groups = new Map<string, { id: string; title: string }[]>();
  for (const d of dashboards.data ?? []) {
    const g = d.group ?? "Dashboards";
    groups.set(g, [...(groups.get(g) ?? []), d]);
  }
  const keep = (to: string) => `${to}${range.label !== "1h" ? `?range=${range.label}` : ""}`;
  const ThemeIcon = theme.theme === "dark" ? Moon : theme.theme === "light" ? Sun : Monitor;
  const nextTheme = theme.theme === "system" ? "dark" : theme.theme === "dark" ? "light" : "system";

  return (
    <ThemeContext.Provider value={theme.effective}>
      <div className="flex min-h-dvh">
        <aside className="sticky top-0 hidden h-dvh w-56 shrink-0 flex-col gap-4 overflow-y-auto border-r border-line bg-panel px-3 py-4 md:flex">
          <div className="flex items-center gap-2 px-3">
            <img src="/loon-calling.png" alt="" className="h-7" />
            <span className="text-lg font-semibold tracking-tight">tremolo</span>
          </div>
          <nav className="flex flex-col gap-0.5">
            <NavLink to={keep("/")} end className={navClass}>
              <Server className="size-4" /> Hosts
            </NavLink>
            <NavLink to={keep("/logs")} className={navClass}>
              <ScrollText className="size-4" /> Logs
            </NavLink>
            {version.data?.traces && (
              <NavLink to={keep("/traces")} className={navClass}>
                <Waypoints className="size-4" /> Traces
              </NavLink>
            )}
            {version.data?.alerts && (
              <>
                <NavLink to={keep("/alerts")} className={navClass}>
                  <Bell className="size-4" /> Alerts {badge}
                </NavLink>
                <NavLink to="/oncall" className={navClass}>
                  <CalendarClock className="size-4" /> On-call
                </NavLink>
              </>
            )}
          </nav>
          {[...groups].map(([g, list]) => (
            <nav key={g} className="flex flex-col gap-0.5">
              <p className="px-3 pb-1 text-xs font-medium uppercase tracking-wide text-muted">{g}</p>
              {list.map((d) => (
                <NavLink key={d.id} to={keep(`/d/${d.id}`)} className={navClass}>
                  <LayoutDashboard className="size-4" /> {d.title}
                </NavLink>
              ))}
            </nav>
          ))}
          <div className="mt-auto space-y-2">
            <UserMenu />
            <p className="px-3 text-xs text-muted">{version.data?.version}</p>
          </div>
        </aside>
        <div className="flex min-w-0 flex-1 flex-col">
          <header className="sticky top-0 z-20 flex items-center gap-3 border-b border-line bg-bg/85 px-4 py-2 backdrop-blur md:px-6">
            <span className="font-semibold md:hidden">tremolo</span>
            <nav className="flex gap-1 text-sm md:hidden">
              <NavLink to={keep("/")} end className={navClass}>
                Hosts
              </NavLink>
              <NavLink to={keep("/logs")} className={navClass}>
                Logs
              </NavLink>
              {version.data?.traces && (
                <NavLink to={keep("/traces")} className={navClass}>
                  Traces
                </NavLink>
              )}
              {version.data?.alerts && (
                <>
                  <NavLink to={keep("/alerts")} className={navClass}>
                    Alerts {badge}
                  </NavLink>
                  <NavLink to="/oncall" className={navClass}>
                    On-call
                  </NavLink>
                </>
              )}
            </nav>
            <div className="ml-auto flex items-center gap-1 rounded-lg border border-line bg-panel p-0.5">
              {RANGES.map((r) => (
                <button
                  key={r.label}
                  type="button"
                  onClick={() => range.set(r.label)}
                  className={`rounded-md px-2 py-0.5 text-xs ${range.label === r.label ? "bg-accent text-white dark:text-bg" : "text-muted hover:text-fg"}`}
                >
                  {r.label}
                </button>
              ))}
            </div>
            <button
              type="button"
              onClick={() => theme.choose(nextTheme)}
              className="rounded-lg border border-line bg-panel p-1.5 text-muted hover:text-fg"
              title={`Theme: ${theme.theme}`}
            >
              <ThemeIcon className="size-4" />
            </button>
            <NavLink to="/profile" className="rounded-lg border border-line bg-panel p-1.5 text-muted hover:text-fg md:hidden" title="You">
              <CircleUser className="size-4" />
            </NavLink>
          </header>
          <main key={loc.pathname} className="mx-auto w-full max-w-[1600px] flex-1 px-4 py-6 md:px-6">
            <Outlet />
          </main>
        </div>
      </div>
    </ThemeContext.Provider>
  );
}

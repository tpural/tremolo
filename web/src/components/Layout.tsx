import { LayoutDashboard, Monitor, Moon, ScrollText, Server, Sun } from "lucide-react";
import { NavLink, Outlet, useLocation } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useDashboards, useRange, useTheme } from "../lib/hooks";
import { RANGES } from "../lib/time";
import { ThemeContext } from "./theme";

function navClass({ isActive }: { isActive: boolean }) {
  return `flex items-center gap-2 rounded-lg px-3 py-1.5 text-sm ${isActive ? "bg-panel-2 text-fg font-medium" : "text-muted hover:text-fg"}`;
}

export function Layout() {
  const dashboards = useDashboards();
  const theme = useTheme();
  const range = useRange();
  const loc = useLocation();
  const version = useQuery({ queryKey: ["version"], queryFn: api.version, staleTime: Infinity });
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
          <p className="mt-auto px-3 text-xs text-muted">{version.data?.version}</p>
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
          </header>
          <main key={loc.pathname} className="mx-auto w-full max-w-[1600px] flex-1 px-4 py-6 md:px-6">
            <Outlet />
          </main>
        </div>
      </div>
    </ThemeContext.Provider>
  );
}

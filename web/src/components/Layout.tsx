import {
  Bell,
  BellRing,
  BookOpen,
  Bot,
  ChevronDown,
  ChevronRight,
  FileCode,
  History,
  CircleUser,
  Database,
  Globe,
  LayoutDashboard,
  LogOut,
  Menu,
  Monitor,
  Moon,
  ScrollText,
  Server,
  ShieldCheck,
  Sun,
  Users,
  Waypoints,
  Wrench,
  X,
} from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Navigate, NavLink, Outlet, useLocation, useNavigate } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, can } from "../lib/api";
import { useAlerts, useDashboards, useFrontendApps, useMe, useRange, useTheme, useVersion } from "../lib/hooks";
import { LONG, RANGES, usesRange } from "../lib/time";
import { ThemeContext } from "./theme";
import { btn, field, fieldSm, seg } from "./ui";

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
      <button type="button" onClick={() => logout.mutate()} className={navClass({ isActive: false })}>
        <LogOut className="size-4" /> Sign out
      </button>
    </nav>
  );
}

/** Section is one titled group of the sidebar. */
function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <nav className="flex flex-col gap-0.5" aria-label={title}>
      <p className="px-3 pb-1 text-xs font-medium uppercase tracking-wide text-muted">{title}</p>
      {children}
    </nav>
  );
}

/** Dashboards lists a group of dashboards; long groups start folded. */
function Dashboards({ title, list, keep }: { title: string; list: { id: string; title: string }[]; keep: (to: string) => string }) {
  const loc = useLocation();
  const [open, setOpen] = useState(list.length <= 8 || list.some((d) => loc.pathname === `/d/${d.id}`));
  const Icon = open ? ChevronDown : ChevronRight;
  return (
    <nav className="flex flex-col gap-0.5" aria-label={title}>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="flex items-center gap-1 px-3 pb-1 text-left text-xs font-medium uppercase tracking-wide text-muted hover:text-fg"
      >
        <Icon className="size-3" /> {title}
        <span className="ml-auto font-normal normal-case">{list.length}</span>
      </button>
      {open &&
        list.map((d) => (
          <NavLink key={d.id} to={keep(`/d/${d.id}`)} className={navClass}>
            <LayoutDashboard className="size-4" /> {d.title}
          </NavLink>
        ))}
    </nav>
  );
}

function App() {
  const dashboards = useDashboards();
  const frontend = useFrontendApps();
  const theme = useTheme();
  const range = useRange();
  const loc = useLocation();
  const version = useVersion();
  const alerts = useAlerts();
  // Info alerts stay on the Alerts page; only warn and page show in the nav.
  const firing = (alerts.data?.alerts ?? []).filter((a) => a.state === "firing" && !a.silenced && !a.acked && a.severity !== "info");
  const loud = firing.some((a) => a.severity === "page");
  const badge = firing.length > 0 && (
    <span
      className={`ml-auto rounded-full px-1.5 text-[10px] font-semibold ${loud ? "bg-bad text-white dark:text-bg" : "bg-warn text-slate-950"}`}
    >
      {firing.length}
    </span>
  );
  const groups = new Map<string, { id: string; title: string }[]>();
  for (const d of dashboards.data ?? []) {
    const g = d.group ?? "Dashboards";
    groups.set(g, [...(groups.get(g) ?? []), d]);
  }
  const me = useMe();
  const editor = can(me.data?.role, "editor");
  const admin = can(me.data?.role, "admin");
  const showRange = usesRange(loc.pathname);
  const keep = (to: string) => `${to}${range.label !== "1h" ? `?range=${range.label}` : ""}`;
  const ThemeIcon = theme.theme === "dark" ? Moon : theme.theme === "light" ? Sun : Monitor;
  const nextTheme = theme.theme === "system" ? "dark" : theme.theme === "dark" ? "light" : "system";

  const [more, setMore] = useState(false);
  const header = useRef<HTMLElement>(null);
  // Sticky parts of pages sit under the header, whose height changes on phones.
  useEffect(() => {
    const el = header.current;
    if (!el) return;
    const set = () => document.documentElement.style.setProperty("--header-h", `${el.offsetHeight}px`);
    set();
    const ro = new ResizeObserver(set);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  useEffect(() => setMore(false), [loc.pathname]);

  const observe = (
    <>
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
      {(frontend.data?.length ?? 0) > 0 && (
        <NavLink to="/frontend" className={navClass}>
          <Globe className="size-4" /> Frontend
        </NavLink>
      )}
      {version.data?.agents && version.data?.traces && (
        <NavLink to="/agents/runs" className={navClass}>
          <Bot className="size-4" /> Agents
        </NavLink>
      )}
    </>
  );
  const respond = version.data?.alerts && (
    <>
      <NavLink to="/alerts" className={navClass}>
        <Bell className="size-4" /> Alerts {badge}
      </NavLink>
      {version.data?.agents && (
        <NavLink to="/investigations" className={navClass}>
          <Bot className="size-4" /> Investigations
        </NavLink>
      )}
      <NavLink to="/runbooks" className={navClass}>
        <BookOpen className="size-4" /> Runbooks
      </NavLink>
      <NavLink to="/maintenance" className={navClass}>
        <Wrench className="size-4" /> Maintenance
      </NavLink>
    </>
  );
  const configure = (editor || admin) && (
    <Section title="Configure">
      {version.data?.alerts && (
        <>
          <NavLink to="/rules" className={navClass}>
            <FileCode className="size-4" /> Rules
          </NavLink>
          <NavLink to="/notifications" className={navClass}>
            <BellRing className="size-4" /> Notifications
          </NavLink>
          {version.data?.agents && (
            <NavLink to="/agents" end className={navClass}>
              <ShieldCheck className="size-4" /> Agent controls
            </NavLink>
          )}
        </>
      )}
      {admin && (
        <>
          <NavLink to="/users" className={navClass}>
            <Users className="size-4" /> Users and teams
          </NavLink>
          <NavLink to="/activity" className={navClass}>
            <History className="size-4" /> Activity
          </NavLink>
        </>
      )}
      <NavLink to={keep("/storage")} className={navClass}>
        <Database className="size-4" /> Storage
      </NavLink>
    </Section>
  );
  const dashboardList = [...groups].map(([g, list]) => <Dashboards key={g} title={g} list={list} keep={keep} />);
  const bottom = (
    <div className="mt-auto space-y-2">
      <UserMenu />
      <p className="px-3 text-xs text-muted">{version.data?.version}</p>
    </div>
  );

  return (
    <ThemeContext.Provider value={theme.effective}>
      <div className="flex min-h-dvh">
        <aside className="sticky top-0 hidden h-dvh w-56 shrink-0 flex-col gap-4 overflow-y-auto border-r border-line bg-panel px-3 py-4 md:flex">
          <div className="flex items-center gap-2 px-3">
            <img src="/loon-calling.png" alt="" className="h-7" />
            <span className="text-lg font-semibold tracking-tight">tremolo</span>
          </div>
          <Section title="Observe">{observe}</Section>
          {dashboardList}
          {respond && <Section title="Respond">{respond}</Section>}
          {configure}
          {bottom}
        </aside>
        {more && (
          <div
            className="fixed inset-0 z-40 flex flex-col gap-4 overflow-y-auto bg-panel p-4 md:hidden"
            role="dialog"
            aria-modal="true"
            aria-label="More"
          >
            <div className="flex items-center gap-2">
              <img src="/loon-calling.png" alt="" className="h-7" />
              <span className="text-lg font-semibold tracking-tight">tremolo</span>
              <button
                type="button"
                onClick={() => setMore(false)}
                className={`${btn.base} ${btn.quiet} ${btn.icon} ml-auto`}
                aria-label="Close menu"
                autoFocus
              >
                <X className="size-5" />
              </button>
            </div>
            {dashboardList}
            {configure}
            {bottom}
          </div>
        )}
        <div className="flex min-w-0 flex-1 flex-col">
          <header
            ref={header}
            className="sticky top-0 z-20 flex flex-col gap-2 border-b border-line bg-bg/85 px-4 py-2 backdrop-blur md:flex-row md:items-center md:px-6"
          >
            <div className="flex items-center gap-2 md:contents">
              <span className="flex items-center gap-1.5 font-semibold md:hidden">
                <img src="/loon-calling.png" alt="" className="h-5" /> tremolo
              </span>
              <div className="ml-auto flex items-center gap-1.5">
                {showRange && range.seconds > LONG && (
                  <span
                    className="text-xs text-warn-ink"
                    title="Queries over more than a month can be slow; any that run out of time stop with why"
                  >
                    {loc.pathname.startsWith("/logs") ? "Long range: filter by namespace or service first" : "Long range: may be slow"}
                  </span>
                )}
                {showRange && (
                  <>
                    <select
                      value={range.label}
                      onChange={(e) => range.set(e.target.value)}
                      className={`${field} ${fieldSm} sm:hidden`}
                      aria-label="Time range"
                    >
                      {RANGES.map((r) => (
                        <option key={r.label}>{r.label}</option>
                      ))}
                    </select>
                    <div className="hidden sm:block">
                      <div className={seg.wrap} role="group" aria-label="Time range">
                        {RANGES.map((r) => (
                          <button
                            key={r.label}
                            type="button"
                            onClick={() => range.set(r.label)}
                            aria-pressed={range.label === r.label}
                            className={seg.item}
                          >
                            {r.label}
                          </button>
                        ))}
                      </div>
                    </div>
                  </>
                )}
                <button
                  type="button"
                  onClick={() => theme.choose(nextTheme)}
                  className={`${btn.base} ${btn.secondary} ${btn.icon}`}
                  title={`Theme: ${theme.theme}`}
                  aria-label={`Theme: ${theme.theme}`}
                >
                  <ThemeIcon className="size-4" />
                </button>
                <button
                  type="button"
                  onClick={() => setMore(true)}
                  className={`${btn.base} ${btn.secondary} ${btn.icon} md:hidden`}
                  aria-label="More: dashboards, settings, profile, sign out"
                >
                  <Menu className="size-4" />
                </button>
              </div>
            </div>
            <nav
              className="-mx-4 flex gap-1 overflow-x-auto px-4 text-sm whitespace-nowrap [scrollbar-width:none] md:hidden"
              aria-label="Main"
            >
              {observe}
              {respond}
            </nav>
          </header>
          <main key={loc.pathname} className="mx-auto w-full max-w-[1600px] flex-1 px-4 py-6 md:px-6">
            <Outlet />
          </main>
        </div>
      </div>
    </ThemeContext.Provider>
  );
}

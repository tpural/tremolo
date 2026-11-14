import { useQuery } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { useEffect, useState } from "react";
import { api } from "./api";
import { rangeSeconds } from "./time";

/** useRange is the page's time range, kept in the URL as ?range=1h. */
export function useRange() {
  const [params, setParams] = useSearchParams();
  const label = params.get("range") ?? "1h";
  const set = (r: string) =>
    setParams(
      (p) => {
        p.set("range", r);
        return p;
      },
      { replace: true },
    );
  return { label, seconds: rangeSeconds(label), set };
}

export function useHosts() {
  return useQuery({ queryKey: ["hosts"], queryFn: ({ signal }) => api.hosts(signal), refetchInterval: 15_000 });
}

export function useAlerts() {
  return useQuery({ queryKey: ["alerts"], queryFn: ({ signal }) => api.alerts(signal), refetchInterval: 15_000, retry: false });
}

/** useRunbookProposals are agents' runbook edits waiting for a person. */
export function useRunbookProposals() {
  return useQuery({
    queryKey: ["runbook-proposals"],
    queryFn: ({ signal }) => api.runbookProposals(signal),
    refetchInterval: 60_000,
    retry: false,
  });
}

/** useMe is the signed-in user; its error is a 401 when nobody is. */
export function useMe() {
  return useQuery({ queryKey: ["me"], queryFn: ({ signal }) => api.me(signal), staleTime: 60_000, retry: false });
}

/** useFrontendApps lists the browser apps sending data; the Frontend tab shows once there's one. */
export function useFrontendApps() {
  return useQuery({ queryKey: ["frontend"], queryFn: ({ signal }) => api.frontend(signal), staleTime: 60_000, retry: false });
}

export function useDashboards() {
  return useQuery({ queryKey: ["dashboards"], queryFn: ({ signal }) => api.dashboards(signal), staleTime: 60_000 });
}

/** useTheme follows the system unless the user picked light or dark. */
export function useTheme() {
  const [theme, setTheme] = useState<string>(() => document.documentElement.dataset.theme ?? "system");
  const [systemDark, setSystemDark] = useState(() => matchMedia("(prefers-color-scheme: dark)").matches);
  useEffect(() => {
    const mq = matchMedia("(prefers-color-scheme: dark)");
    const on = () => setSystemDark(mq.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);
  const choose = (t: string) => {
    if (t === "system") delete document.documentElement.dataset.theme;
    else document.documentElement.dataset.theme = t;
    try {
      if (t === "system") localStorage.removeItem("tremolo-theme");
      else localStorage.setItem("tremolo-theme", t);
    } catch {
      // storage unavailable: the choice lasts until reload
    }
    setTheme(t);
  };
  const effective = theme === "system" ? (systemDark ? "dark" : "light") : theme;
  return { theme, effective, choose };
}

export function useVersion() {
  return useQuery({ queryKey: ["version"], queryFn: api.version, staleTime: Infinity });
}

/** useOpenInvestigations is the agents' open work, when agents are on. */
export function useOpenInvestigations() {
  const version = useVersion();
  return useQuery({
    queryKey: ["investigations", "open,escalated"],
    queryFn: ({ signal }) => api.investigations({ status: "open,escalated" }, signal),
    enabled: Boolean(version.data?.agents),
    refetchInterval: 15_000,
  });
}

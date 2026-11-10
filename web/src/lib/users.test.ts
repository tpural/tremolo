import { describe, expect, it } from "vitest";
import { can, cleanParams, type Alert, type CalendarView, type Replay } from "./api";
import { goMinutes } from "./format";
import { mine, when } from "../pages/AlertsPage";
import { safeNext } from "../pages/LoginPage";
import { browser } from "../pages/ProfilePage";
import { firingAt, summarize, validName } from "../pages/RulePage";
import { errorLine, tokenize } from "./starlark";
import { monthGrid, on, scope, toInput, weekBars } from "../pages/MaintenancePage";
import { stateQuery } from "../pages/HostPage";

const alert = (over: Partial<Alert>): Alert => ({
  id: "r/k",
  rule: "r",
  summary: "s",
  severity: "page",
  state: "firing",
  since: "",
  silenced: false,
  ...over,
});

describe("users", () => {
  it("orders roles", () => {
    expect(can("admin", "editor")).toBe(true);
    expect(can("viewer", "editor")).toBe(false);
    expect(can(undefined, "viewer")).toBe(false);
  });
  it("reads Go durations as minutes", () => {
    expect(goMinutes("0s")).toBe(0);
    expect(goMinutes("10m0s")).toBe(10);
    expect(goMinutes("1h30m0s")).toBe(90);
  });
  it("keeps redirects on this site", () => {
    expect(safeNext("/alerts?x=1")).toBe("/alerts?x=1");
    expect(safeNext("//evil.example")).toBe("/");
    expect(safeNext("https://evil.example")).toBe("/");
    expect(safeNext(null)).toBe("/");
  });
  it("knows which alerts are mine", () => {
    const me = { name: "tim", teams: ["platform"] };
    expect(mine(alert({ assignee: "tim" }), me)).toBe(true);
    expect(mine(alert({ assignee: "platform" }), me)).toBe(true);
    expect(mine(alert({ assignee: "alex" }), me)).toBe(false);
    expect(mine(alert({ plan: [{ key: "push:t", channel: "push", person: "tim", after: 0 }] }), me)).toBe(true);
    expect(mine(alert({ plan: [{ key: "push", channel: "push", after: 0 }] }), me)).toBe(false);
  });
});

describe("history helpers", () => {
  it("drops empty params", () => {
    expect(cleanParams({ q: "", rule: undefined, before: 5, limit: 50 })).toEqual({ before: "5", limit: "50" });
  });
  it("shows the date for events before today", () => {
    const now = new Date(2026, 10, 8, 12);
    expect(when(new Date(2026, 10, 8, 9).toISOString(), now)).toBe(new Date(2026, 10, 8, 9).toLocaleTimeString());
    expect(when(new Date(2026, 10, 6, 9).toISOString(), now)).toBe(new Date(2026, 10, 6, 9).toLocaleString());
  });
});

describe("browser", () => {
  it("names common browsers briefly", () => {
    expect(browser("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36")).toBe("Chrome on Linux");
    expect(
      browser(
        "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
      ),
    ).toBe("Safari on iOS");
    expect(browser("Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0")).toBe("Firefox on Windows");
    expect(browser("curl/8.5.0")).toBe("A script");
    expect(browser("")).toBe("A browser");
  });
});

describe("rule editor", () => {
  it("accepts the names the server does", () => {
    expect(validName("api-errors")).toBe(true);
    expect(validName("disk_full2")).toBe(true);
    expect(validName("Bad Name")).toBe(false);
    expect(validName("-leading")).toBe(false);
    expect(validName("")).toBe(false);
  });
  it("counts a replay's alerts", () => {
    const ep = (key: string, resolved?: string) => ({ key, summary: "", severity: "warn" as const, pending: "", fired: "", resolved });
    const run = { episodes: [ep("a", "x"), ep("a", "y"), ep("b")] } as Replay;
    expect(summarize(run)).toEqual({ fired: 3, still: 1, keys: 2 });
  });
  it("lines up what really fired with the runs", () => {
    const run = { from: "2026-03-01T00:00:00Z", to: "2026-03-01T00:04:30Z", step: 60e9, runs: 6 } as Replay;
    const ep = (fired: string, resolved?: string) => ({ key: "a", summary: "", severity: "warn" as const, pending: "", fired, resolved });
    const real = [ep("2026-03-01T00:01:00Z", "2026-03-01T00:03:00Z"), ep("2026-03-01T00:02:30Z")];
    expect(firingAt(run, real)).toEqual([0, 1, 1, 1, 1, 1]);
    expect(firingAt(run, [ep("2026-03-01T00:04:10Z")])).toEqual([0, 0, 0, 0, 0, 1]);
  });
});

describe("host state history", () => {
  it("quotes the host name in LogsQL", () => {
    expect(stateQuery("pi-1")).toBe('stream:="tremolo-hosts" host:="pi-1"');
    expect(stateQuery('we"ird')).toBe('stream:="tremolo-hosts" host:="we\\"ird"');
  });
});

describe("maintenance calendar", () => {
  it("lays a month out in six weeks from Monday", () => {
    const g = monthGrid(new Date(2026, 2, 15));
    expect(g).toHaveLength(42);
    expect(g[0].toDateString()).toBe(new Date(2026, 1, 23).toDateString());
    expect(g[6].getDay()).toBe(0);
  });
  it("blends back-to-back maintenance into one bar, but not nightly windows", () => {
    const week = monthGrid(new Date(2026, 2, 1)).slice(7, 14); // Mar 2 to 8
    const days = [2, 3, 4, 5, 6, 7, 8];
    const occ = (id: string, h0: number, h1: number) =>
      days.map((d) => ({
        period: { id, title: id, start: "", every: "day" as const, rules: [], hosts: [] },
        start: new Date(2026, 2, d, h0).toISOString(),
        end: new Date(2026, 2, d, h1).toISOString(),
      }));
    const nightly = weekBars(week, { timezone: "UTC", maintenance: occ("backups", 1, 2) } as CalendarView);
    expect(nightly.map((b) => [b.start, b.end])).toEqual(days.map((_, i) => [i, i]));
    expect(nightly[0].label).toMatch(/backups$/);
    const allDay = weekBars(week, { timezone: "UTC", maintenance: occ("freeze", 0, 24) } as CalendarView);
    expect(allDay.map((b) => [b.start, b.end, b.label])).toEqual([[0, 6, "freeze"]]);
    // Together, the long one goes on top and the nightly one keeps one row.
    const both = weekBars(week, { timezone: "UTC", maintenance: [...occ("backups", 1, 2), ...occ("freeze", 0, 24).slice(2, 5)] } as CalendarView);
    expect(new Set(both.filter((b) => b.period === "backups").map((b) => b.lane))).toEqual(new Set([1]));
    expect(both.find((b) => b.period === "freeze")?.lane).toBe(0);
  });
  it("describes what maintenance holds", () => {
    expect(scope({ rules: [], hosts: [] })).toBe("all alerts");
    expect(scope({ rules: ["disk-full"], hosts: ["nas"] })).toBe("disk-full on nas");
    expect(scope({ rules: [], hosts: ["nas", "pi-1"] })).toBe("on nas, pi-1");
  });
  it("keeps open-ended maintenance on every later day", () => {
    const o = { period: { id: "x", title: "x", start: "", rules: [], hosts: [] }, start: new Date(2026, 2, 1, 12).toISOString() };
    expect(on(o, new Date(2026, 2, 1))).toBe(true);
    expect(on(o, new Date(2026, 5, 1))).toBe(true);
    expect(on(o, new Date(2026, 1, 28))).toBe(false);
  });
  it("round-trips local times for inputs", () => {
    const d = new Date(2026, 2, 8, 9, 30);
    expect(toInput(d)).toBe("2026-03-08T09:30");
    expect(new Date(toInput(d)).getTime()).toBe(d.getTime());
  });
});

describe("rule editor highlighting", () => {
  it("keeps the source exactly", () => {
    const src =
      'def check(now):\n    # quiet\n    v = metric("up") * 2.5\n    if v > 3 and not None:\n        return alert("%d down" % v, key = \'x\')\n';
    const tokens = tokenize(src);
    expect(tokens.map((t) => t.text).join("")).toBe(src);
    const kind = (text: string) => tokens.find((t) => t.text === text)?.kind;
    expect(kind("def")).toBe("keyword");
    expect(kind("metric")).toBe("builtin");
    expect(kind('"up"')).toBe("string");
    expect(kind("2.5")).toBe("number");
    expect(kind("# quiet")).toBe("comment");
    expect(kind("None")).toBe("constant");
  });
  it("only colours builtins that are called", () => {
    expect(tokenize("logs = 1").find((t) => t.text.includes("logs"))?.kind).toBe("plain");
  });
  it("handles an unclosed string while typing", () => {
    const src = 'x = "abc\ny = 1';
    expect(
      tokenize(src)
        .map((t) => t.text)
        .join(""),
    ).toBe(src);
  });
  it("finds the line an error points at in the rule's own file", () => {
    const tb = "Traceback (most recent call last):\n  lib/util.star:3:9: in double\n  api.star:12:19: in check\nError: boom";
    expect(errorLine(tb, "api.star")).toBe(12);
    expect(errorLine("draft.star:4:15: got newline, want primary expression", "draft.star")).toBe(4);
    expect(errorLine("only util.star:2:1: oops", "api.star")).toBe(2);
    expect(errorLine("no location", "api.star")).toBeUndefined();
  });
});

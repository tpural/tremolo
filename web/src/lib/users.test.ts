import { describe, expect, it } from "vitest";
import { can, cleanParams, type Alert } from "./api";
import { goMinutes } from "./format";
import { mine, when } from "../pages/AlertsPage";
import { safeNext } from "../pages/LoginPage";
import { browser } from "../pages/ProfilePage";

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

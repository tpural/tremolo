import { describe, expect, it } from "vitest";
import { lookBack, rangeSeconds, step } from "./time";

describe("time", () => {
  it("has ranges past a month", () => {
    expect(rangeSeconds("1y")).toBe(365 * 86400);
    expect((365 * 86400) / step(rangeSeconds("1y"))).toBeLessThan(400);
  });
  it("writes look-backs in days", () => {
    expect(lookBack(86400)).toBe("1 day");
    expect(lookBack(7 * 86400)).toBe("7 days");
    expect(lookBack(365 * 86400)).toBe("1 year");
  });
});

describe("ranges in links", () => {
  it("shows the picker only where the range is read", async () => {
    const { usesRange, withRange } = await import("./time");
    expect(usesRange("/")).toBe(true);
    expect(usesRange("/hosts/gpu01")).toBe(true);
    expect(usesRange("/alerts")).toBe(false);
    expect(usesRange("/frontend")).toBe(false);
    expect(usesRange("/frontend/angelorooms")).toBe(true);
    expect(withRange("/", "7d")).toBe("/?range=7d");
    expect(withRange("/runbooks", "7d")).toBe("/runbooks");
    expect(withRange("/logs?q=x", "1h")).toBe("/logs?q=x");
  });
});

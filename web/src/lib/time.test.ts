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

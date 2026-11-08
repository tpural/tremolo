import { describe, expect, it } from "vitest";
import { duration, formatValue, legend, substitute } from "./format";
import { parseLogLines } from "./api";
import { step } from "./time";

describe("formatValue", () => {
  it("formats units", () => {
    expect(formatValue(1536, "bytes")).toBe("1.5 KiB");
    expect(formatValue(0.423, "percentunit")).toBe("42%");
    expect(formatValue(7.25, "percent")).toBe("7.3%");
    expect(formatValue(12_500, "short")).toBe("12.5K");
    expect(formatValue(0.25, "seconds")).toBe("250ms");
    expect(formatValue(null)).toBe("–");
  });
});

describe("duration", () => {
  it("uses the two largest units", () => {
    expect(duration(90)).toBe("1m 30s");
    expect(duration(3 * 3600 + 120)).toBe("3h 2m");
    expect(duration(3 * 86400)).toBe("3d");
  });
});

describe("legend", () => {
  it("fills templates", () => {
    expect(legend("{{pod}} on {{k8s.node.name}}", { pod: "a", "k8s.node.name": "n1" })).toBe("a on n1");
  });
  it("falls back to the labels", () => {
    expect(legend(undefined, { __name__: "up", job: "x" })).toBe('up{job="x"}');
  });
});

describe("substitute", () => {
  it("replaces known variables and escapes quotes", () => {
    expect(substitute('up{node="$node"} $other', { node: 'a"b' })).toBe('up{node="a\\"b"} $other');
  });
});

describe("parseLogLines", () => {
  it("skips partial lines", () => {
    expect(parseLogLines('{"_msg":"a"}\n{"_msg":\n')).toEqual([{ _msg: "a" }]);
  });
});

describe("step", () => {
  it("gives about 240 points", () => {
    expect(step(3600)).toBe(15);
    expect(step(24 * 3600)).toBe(600);
  });
});

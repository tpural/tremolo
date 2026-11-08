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

import { groups, needsAttention } from "../pages/Overview";
import type { Host } from "./api";

describe("host groups", () => {
  const h = (name: string, kind: Host["kind"], cluster?: string, state: Host["state"] = "ready"): Host => ({ name, kind, cluster, state, pods: 0 });
  it("puts nodes under clusters and servers last", () => {
    const g = groups([h("nas", "server"), h("pi", "kubernetes", "homelab"), h("gpu", "kubernetes"), h("edge", "kubernetes", "garage")]);
    expect(g.map((x) => x.title)).toEqual(["Kubernetes", "Kubernetes · garage", "Kubernetes · homelab", "Servers"]);
  });
  it("does not alarm on hosts a power manager put to sleep", () => {
    expect(needsAttention(h("gpu", "kubernetes", "", "asleep"))).toBe(false);
    expect(needsAttention(h("pi", "kubernetes", "", "off"))).toBe(false);
    expect(needsAttention(h("nas", "server", "", "unreachable"))).toBe(true);
  });
});

import { podHealth } from "../components/PodTable";

describe("podHealth", () => {
  const pod = { namespace: "a", name: "p", phase: "Running", ready: 1, containers: 1, restarts: 0 };
  it("rates pods", () => {
    expect(podHealth(pod)).toBe("good");
    expect(podHealth({ ...pod, ready: 0 })).toBe("warn");
    expect(podHealth({ ...pod, reason: "CrashLoopBackOff" })).toBe("bad");
    expect(podHealth({ ...pod, phase: "Succeeded", ready: 0 })).toBe("done");
  });
});

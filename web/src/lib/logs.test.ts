import { describe, expect, it } from "vitest";
import { buildQuery, decodeFilter, encodeFilter, facetCandidates, level, levelClause, sourceOf, tokenize } from "./logs";

describe("level", () => {
  it("prefers the severity field", () => {
    expect(level({ _msg: "all fine", _time: "", severity_text: "ERROR" })).toBe("error");
    expect(level({ _msg: "x", _time: "", level: "warning" })).toBe("warn");
  });
  it("reads common message styles", () => {
    const l = (m: string) => level({ _msg: m, _time: "" });
    expect(l('time=x level=error msg="boom"')).toBe("error");
    expect(l('{"level":"warn","msg":"slow"}')).toBe("warn");
    expect(l("2026-11-08 ERROR something broke")).toBe("error");
    expect(l("E1106 18:19:45.204428       1 main.go:70] failed")).toBe("error");
    expect(l("I1106 18:17:45.201713       1 nfd-worker.go:785] done")).toBe("info");
    expect(l("level=debug x=1")).toBe("debug");
    expect(l("hello")).toBe("");
  });
});

describe("buildQuery", () => {
  it("combines text, filters and level", () => {
    const q = buildQuery("timeout", [{ field: "service.name", value: 'ci "runner"' }, { field: "host.name", value: "nas", negate: true }], "");
    expect(q).toBe('timeout service.name:="ci \\"runner\\"" -host.name:="nas"');
  });
  it("matches everything when empty", () => {
    expect(buildQuery("  ", [], "")).toBe("*");
  });
  it("adds a level clause that includes worse levels", () => {
    expect(levelClause("warn")).toContain("warn");
    expect(levelClause("warn")).toContain("error");
    expect(levelClause("error")).not.toContain("warn");
    expect(buildQuery("", [], "error")).toMatch(/^\* \(severity_text:~/);
  });
});

describe("filters in the URL", () => {
  it("round-trips, including values with =", () => {
    for (const f of [{ field: "a.b", value: "x=y" }, { field: "host.name", value: "nas", negate: true }]) {
      expect(decodeFilter(encodeFilter(f))).toEqual({ ...f, negate: !!f.negate });
    }
    expect(decodeFilter("novalue")).toBeNull();
  });
});

describe("sourceOf", () => {
  it("names the most specific source", () => {
    expect(sourceOf({ _msg: "", _time: "", "k8s.namespace.name": "ns", "k8s.pod.name": "p" })).toBe("ns/p");
    expect(sourceOf({ _msg: "", _time: "", "service.name": "ci", "host.name": "runner" })).toBe("ci@runner");
    expect(sourceOf({ _msg: "", _time: "", app: "promtail" })).toBe("promtail");
  });
});

describe("facetCandidates", () => {
  it("drops unique and internal fields", () => {
    expect(facetCandidates([{ value: "_msg", hits: 1 }, { value: "k8s.pod.uid", hits: 1 }, { value: "service.name", hits: 1 }])).toEqual([
      "service.name",
    ]);
  });
});

describe("tokenize", () => {
  it("colors logfmt keys and values", () => {
    const t = tokenize('level=info took=12ms ok=true msg="hello there"');
    expect(t.filter((x) => x.kind === "key").map((x) => x.text)).toEqual(["level=", "took=", "ok=", "msg="]);
    expect(t.find((x) => x.text === "12ms")?.kind).toBe("number");
    expect(t.find((x) => x.text === "true")?.kind).toBe("bool");
  });
  it("flattens JSON lines", () => {
    const t = tokenize('{"level":"warn","n":3}');
    expect(t.map((x) => x.text).join("")).toBe("level=warn n=3");
  });
  it("dims a leading timestamp", () => {
    expect(tokenize("2026-11-08T05:00:00Z started")[0]).toEqual({ kind: "time", text: "2026-11-08T05:00:00Z " });
  });
  it("keeps the whole message", () => {
    const msg = "GET /a?b=c 200 in 3ms";
    expect(
      tokenize(msg)
        .map((x) => x.text)
        .join(""),
    ).toBe(msg);
  });
});

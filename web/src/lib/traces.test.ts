import { describe, expect, it } from "vitest";
import { bounds, colorServices, ms, parseMs, serviceColor, parseServiceStats, parseSummary, searchQuery, servicesQuery, spanFilter, tree, type JaegerSpan, type JaegerTrace } from "./traces";

describe("searchQuery", () => {
  it("sums up every trace when nothing is filtered", () => {
    const q = searchQuery({}, 20);
    expect(q).toMatch(/^span_id:\* \| stats by \(trace_id\) min\(start_time_unix_nano\) start/);
    expect(q).not.toContain("matched");
    expect(q).toMatch(/\| sort by \(start desc\) \| limit 20$/);
  });

  it("keeps traces with a matching span, slow and failed ones", () => {
    const q = searchQuery({ service: "check\"out", operation: "GET /", errorsOnly: true, minMs: 250, filter: "span_attr:http.status_code:=500" }, 50);
    expect(q).toContain(`count() if ("resource_attr:service.name":="check\\"out" name:="GET /" (span_attr:http.status_code:=500)) matched`);
    expect(q).toContain("| filter matched:>0 | math (end - start) as dur | filter dur:>250000000 errors:>0 |");
  });

  it("builds an empty filter from nothing", () => {
    expect(spanFilter({ filter: "  " })).toBe("");
  });

  it("counts services' entry spans for latency", () => {
    expect(servicesQuery).toContain(`quantile(0.95, duration) if (parent_span_id:="" OR kind:=2) p95`);
  });
});

describe("parsing", () => {
  it("reads a summary row", () => {
    const s = parseSummary({
      trace_id: "abc",
      start: "1000000000",
      end: "1420000000",
      spans: "4",
      errors: "1",
      services: '["payments","checkout",""]',
      root: '{"name":"POST /checkout","resource_attr:service.name":"checkout","status_code":"2"}',
    });
    expect(s).toEqual({ id: "abc", start: 1000, duration: 420, spans: 4, errors: 1, services: ["checkout", "payments"], rootName: "POST /checkout", rootService: "checkout", rootError: true });
  });

  it("tolerates broken JSON", () => {
    expect(parseSummary({ trace_id: "x", start: "0", end: "0", services: "[", root: "{" }).services).toEqual([]);
  });

  it("reads service stats in milliseconds", () => {
    expect(parseServiceStats({ "resource_attr:service.name": "a", spans: "3", errors: "0", requests: "1", p95: "2500000" })).toMatchObject({ service: "a", spans: 3, p95: 2.5, p50: undefined });
  });
});

function span(id: string, start: number, duration: number, parent?: string, tags: JaegerSpan["tags"] = []): JaegerSpan {
  return {
    traceID: "t",
    spanID: id,
    operationName: id,
    references: parent ? [{ refType: "CHILD_OF", spanID: parent, traceID: "t" }] : [],
    startTime: start,
    duration,
    tags,
    logs: [],
    processID: id === "pay" ? "p2" : "p1",
  };
}

describe("tree", () => {
  const trace: JaegerTrace = {
    traceID: "t",
    processes: { p1: { serviceName: "checkout", tags: [] }, p2: { serviceName: "payments", tags: [] } },
    spans: [
      span("pay", 70, 330, "root", [{ key: "error", value: "true" }]),
      span("db", 10, 50, "root"),
      span("root", 0, 420),
      span("orphan", 500, 10, "missing"),
    ],
  };

  it("orders depth-first by start and marks errors", () => {
    const rows = tree(trace);
    expect(rows.map((r) => [r.span.spanID, r.depth, r.service])).toEqual([
      ["root", 0, "checkout"],
      ["db", 1, "checkout"],
      ["pay", 1, "payments"],
      ["orphan", 0, "checkout"],
    ]);
    expect(rows[0].children).toBe(2);
    expect(rows[2].error).toBe(true);
    expect(rows[2].ancestors).toEqual(["root"]);
  });

  it("spans the whole trace", () => {
    expect(bounds(trace)).toEqual({ start: 0, end: 510 });
  });
});

describe("ms", () => {
  it("picks a readable unit", () => {
    expect([ms(0.84), ms(2.345), ms(12.34), ms(420), ms(1420), ms(125000)]).toEqual(["840µs", "2.35ms", "12.3ms", "420ms", "1.42s", "2m 5s"]);
  });
});

describe("parseMs", () => {
  it("reads durations", () => {
    expect([parseMs("250ms"), parseMs("1.5s"), parseMs("2m"), parseMs("800us"), parseMs("40"), parseMs(""), parseMs("fast")]).toEqual([250, 1500, 120000, 0.8, 40, undefined, undefined]);
  });
});

describe("serviceColor", () => {
  it("gives services distinct, stable colors", () => {
    const names = ["gateway", "checkout", "payments", "inventory", "catalog", "worker"];
    colorServices(names);
    const colors = names.map(serviceColor);
    expect(new Set(colors).size).toBe(names.length);
    expect(names.map(serviceColor)).toEqual(colors);
  });
});

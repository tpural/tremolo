import { describe, expect, it } from "vitest";
import { parseRuns, steps } from "./agentRuns";
import type { JaegerTrace } from "./traces";

describe("agent runs", () => {
  it("joins runs with their steps", () => {
    const runs = parseRuns(
      [
        {
          trace_id: "t1",
          start_time_unix_nano: "1794454595806598027",
          duration: "43653219643",
          "span_attr:gen_ai.agent.name": "upkeeper",
          "span_attr:tremolo.investigation.id": "6",
          "span_attr:tremolo.investigation.title": "orders down",
          "span_attr:tremolo.outcome": "escalated",
          status_code: "0",
        },
        { trace_id: "t2", "resource_attr:service.name": "claude", "span_attr:tremolo.task": "patrol", status_code: "2" },
      ],
      [
        { trace_id: "t1", "span_attr:gen_ai.operation.name": "chat", n: "9", input: "50000", output: "792" },
        { trace_id: "t1", "span_attr:gen_ai.operation.name": "execute_tool", n: "14", errors: "1", empty: "6" },
      ],
    );
    expect(runs[0]).toMatchObject({
      trace: "t1",
      agent: "upkeeper",
      investigation: 6,
      task: "investigation",
      outcome: "escalated",
      steps: 9,
      tokens: 50792,
      tools: 14,
      toolErrors: 1,
      empty: 6,
      error: false,
    });
    expect(runs[0].duration).toBeCloseTo(43.65, 1);
    expect(runs[1]).toMatchObject({ agent: "claude", task: "patrol", steps: 0, error: true });
  });

  it("reads a trace as steps in time order", () => {
    const kv = (o: Record<string, string>) => Object.entries(o).map(([key, value]) => ({ key, value }));
    const span = (
      id: string,
      start: number,
      tags: Record<string, string>,
      logs: { timestamp: number; fields: Record<string, string> }[] = [],
    ) => ({
      traceID: "t",
      spanID: id,
      operationName: id,
      references: id === "root" ? [] : [{ refType: "CHILD_OF", spanID: "root", traceID: "t" }],
      startTime: start,
      duration: 1_000_000,
      tags: kv(tags),
      logs: logs.map((l) => ({ timestamp: l.timestamp, fields: kv(l.fields) })),
      processID: "p1",
    });
    const trace: JaegerTrace = {
      traceID: "t",
      processes: {},
      spans: [
        span("tool", 3_000_000, {
          "gen_ai.operation.name": "execute_tool",
          "gen_ai.tool.name": "logs",
          "gen_ai.tool.call.arguments": '{"q":"x"}',
          "gen_ai.tool.call.result": "No logs match.",
          "tremolo.tool.found": "false",
        }),
        span("root", 1_000_000, { "gen_ai.operation.name": "invoke_agent" }, [
          { timestamp: 4_000_000, fields: { event: "upkeeper.decision", "upkeeper.decision": "woke the GPU" } },
        ]),
        span("chat", 2_000_000, {
          "gen_ai.operation.name": "chat",
          "upkeeper.step": "1",
          "gen_ai.output.messages": '[{"content":"Looking at logs.","tool_calls":[{"function":{"name":"logs"}}]}]',
          "gen_ai.usage.input_tokens": "100",
        }),
      ],
    };
    const { root, steps: list } = steps(trace);
    expect(root?.spanID).toBe("root");
    expect(list.map((s) => s.kind)).toEqual(["chat", "tool", "event"]);
    expect(list[0]).toMatchObject({ said: "Looking at logs.", calls: ["logs"], tokensIn: 100, at: 1000 });
    expect(list[1]).toMatchObject({ name: "logs", found: false });
    expect(list[2]).toMatchObject({ name: "upkeeper.decision", at: 3000 });
  });
});

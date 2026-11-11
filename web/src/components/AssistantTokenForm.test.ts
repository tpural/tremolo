import { describe as suite, expect, it } from "vitest";
import { describe, scopeOf } from "./AssistantTokenForm";

suite("scopeOf", () => {
  const filters = [
    { label: "namespace=angelorooms", metrics: '{k8s_namespace_name="angelorooms"}', logs: 'k8s.namespace.name:="angelorooms"' },
    { label: "custom", traces: "x:=y" },
  ];
  it("gives each signal its own filters, or everything", () => {
    expect(scopeOf({ metrics: "some", logs: "all", traces: "none" }, filters, ["runbooks"])).toEqual({
      allow: ["runbooks"],
      metrics: ['{k8s_namespace_name="angelorooms"}'],
      logs: ["*"],
    });
  });
  it("describes a scope", () => {
    expect(describe({ metrics: ["a", "b"], logs: ["*"], allow: ["runbooks"] })).toBe("metrics (2 filters), all logs, read runbooks");
    expect(describe({})).toBe("nothing");
  });
});

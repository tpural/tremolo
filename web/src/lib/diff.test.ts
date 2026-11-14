import { describe, expect, it } from "vitest";
import { diffLines } from "./diff";

describe("diffLines", () => {
  it("keeps, adds and removes lines", () => {
    expect(diffLines("# T\nold\nend\n", "# T\nnew\nend\nmore\n")).toEqual([
      { op: " ", text: "# T" },
      { op: "-", text: "old" },
      { op: "+", text: "new" },
      { op: " ", text: "end" },
      { op: "+", text: "more" },
    ]);
  });
  it("treats a new text as all added", () => {
    expect(diffLines("", "a\nb")).toEqual([
      { op: "+", text: "a" },
      { op: "+", text: "b" },
    ]);
  });
});

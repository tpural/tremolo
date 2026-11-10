import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { Markdown } from "./markdown";

const html = (text: string) => renderToStaticMarkup(<Markdown text={text} />);

describe("Markdown", () => {
  it("renders headings, lists, code and inline marks", () => {
    const out = html("# Title\n\nRun `crictl rmi --prune` **first**.\n\n- one\n- two\n\n```\nkubectl get pods\n```");
    expect(out).toContain("<h2");
    expect(out).toContain("Title</h2>");
    expect(out).toContain("<code");
    expect(out).toContain("crictl rmi --prune</code>");
    expect(out).toContain("<strong>first</strong>");
    expect(out).toContain("<li>one</li><li>two</li>");
    expect(out).toContain("kubectl get pods</pre>");
  });

  it("never renders markup or unsafe links from the text", () => {
    const out = html("<script>alert(1)</script> [x](javascript:alert(1)) [ok](https://example.com) [here](/alerts)");
    expect(out).not.toContain("<script>");
    expect(out).toContain("&lt;script&gt;");
    expect(out).not.toContain("javascript:");
    expect(out).toContain('href="https://example.com"');
    expect(out).toContain('href="/alerts"');
  });
});

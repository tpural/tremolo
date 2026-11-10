import type { ReactNode } from "react";

/**
 * Markdown renders the subset runbooks use: headings, paragraphs, lists, code blocks, quotes,
 * inline code, bold, italics and links. It builds elements rather than HTML, so text can't inject markup.
 */
export function Markdown({ text }: { text: string }) {
  return <div className="space-y-3 text-sm leading-6">{blocks(text)}</div>;
}

const HEADING = ["mt-2 text-lg font-semibold", "mt-4 border-b border-line pb-1 text-base font-semibold", "mt-3 font-semibold"];

function blocks(text: string): ReactNode[] {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const out: ReactNode[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) {
      i++;
      continue;
    }
    const fence = line.match(/^```/);
    if (fence) {
      const code: string[] = [];
      i++;
      while (i < lines.length && !lines[i].startsWith("```")) code.push(lines[i++]);
      i++;
      out.push(
        <pre key={out.length} className="overflow-x-auto rounded-lg border border-line bg-panel-2 p-3 font-mono text-xs leading-5">
          {code.join("\n")}
        </pre>,
      );
      continue;
    }
    const h = line.match(/^(#{1,3})\s+(.*)$/);
    if (h) {
      const level = h[1].length;
      const Tag = (["h2", "h3", "h4"] as const)[level - 1];
      out.push(
        <Tag key={out.length} className={HEADING[level - 1]}>
          {inline(h[2])}
        </Tag>,
      );
      i++;
      continue;
    }
    if (/^\s*([-*]|\d+\.)\s+/.test(line)) {
      const ordered = /^\s*\d+\./.test(line);
      const items: string[] = [];
      while (i < lines.length && /^\s*([-*]|\d+\.)\s+/.test(lines[i])) {
        let item = lines[i++].replace(/^\s*([-*]|\d+\.)\s+/, "");
        // Indented lines continue the item.
        while (i < lines.length && /^\s{2,}\S/.test(lines[i]) && !/^\s*([-*]|\d+\.)\s+/.test(lines[i])) item += " " + lines[i++].trim();
        items.push(item);
      }
      const List = ordered ? "ol" : "ul";
      out.push(
        <List key={out.length} className={`space-y-1 pl-5 ${ordered ? "list-decimal" : "list-disc"}`}>
          {items.map((it, j) => (
            <li key={j}>{inline(it)}</li>
          ))}
        </List>,
      );
      continue;
    }
    if (line.startsWith(">")) {
      const quote: string[] = [];
      while (i < lines.length && lines[i].startsWith(">")) quote.push(lines[i++].replace(/^>\s?/, ""));
      out.push(
        <blockquote key={out.length} className="border-l-2 border-line-strong pl-3 text-muted">
          {inline(quote.join(" "))}
        </blockquote>,
      );
      continue;
    }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() && !/^(#{1,3}\s|```|>|\s*([-*]|\d+\.)\s+)/.test(lines[i])) para.push(lines[i++].trim());
    out.push(<p key={out.length}>{inline(para.join(" "))}</p>);
  }
  return out;
}

const INLINE = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*\s][^*]*\*|_[^_\s][^_]*_)|(\[[^\]]+\]\([^)\s]+\))|(https?:\/\/[^\s)]+)/g;

/** inline renders code, bold, italics and links in one line of text. */
export function inline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  for (const m of text.matchAll(INLINE)) {
    if (m.index > last) out.push(text.slice(last, m.index));
    const [all, code, bold, italic, link, bare] = m;
    const key = out.length;
    if (code)
      out.push(
        <code key={key} className="rounded bg-panel-2 px-1 py-0.5 font-mono text-[0.85em]">
          {code.slice(1, -1)}
        </code>,
      );
    else if (bold) out.push(<strong key={key}>{bold.slice(2, -2)}</strong>);
    else if (italic) out.push(<em key={key}>{italic.slice(1, -1)}</em>);
    else if (link) {
      const [, label, href] = link.match(/^\[([^\]]+)\]\(([^)\s]+)\)$/)!;
      out.push(safeLink(key, href, label));
    } else if (bare) out.push(safeLink(key, bare, bare));
    else out.push(all);
    last = m.index + all.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

/** safeLink links only to web addresses and tremolo's own pages; anything else stays text. */
function safeLink(key: number, href: string, label: string) {
  if (!/^(https?:\/\/|\/(?!\/))/.test(href)) return <span key={key}>{label}</span>;
  const external = href.startsWith("http");
  return (
    <a
      key={key}
      href={href}
      className="text-accent underline-offset-2 hover:underline"
      {...(external ? { target: "_blank", rel: "noreferrer" } : {})}
    >
      {label}
    </a>
  );
}

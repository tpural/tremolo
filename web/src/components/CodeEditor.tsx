import { useEffect, useMemo, useRef } from "react";
import { tokenize, type TokenKind } from "../lib/starlark";

const COLOURS: Record<TokenKind, string> = {
  comment: "text-muted italic",
  string: "text-tok-str",
  number: "text-tok-num",
  keyword: "text-accent font-semibold",
  builtin: "text-tok-bool",
  constant: "text-tok-num",
  plain: "",
};

// The textarea and the highlighted copy behind it must lay text out identically.
const TEXT = "m-0 whitespace-pre p-3 font-mono text-xs leading-5 [tab-size:4]";

/**
 * CodeEditor is a Starlark code box: line numbers, highlighting, Tab indents with four spaces.
 * errorLine marks a line red; goTo moves the cursor to a line whenever its n changes.
 */
export function CodeEditor({
  value,
  onChange,
  readOnly,
  errorLine,
  goTo,
  label,
}: {
  value: string;
  onChange: (v: string) => void;
  readOnly: boolean;
  errorLine?: number;
  goTo?: { line: number; n: number };
  label: string;
}) {
  const area = useRef<HTMLTextAreaElement>(null);
  const code = useRef<HTMLPreElement>(null);
  const gutter = useRef<HTMLDivElement>(null);
  const lines = value.split("\n").length;
  const tokens = useMemo(() => tokenize(value), [value]);

  const sync = () => {
    const t = area.current;
    if (!t) return;
    if (code.current) code.current.style.transform = `translate(${-t.scrollLeft}px, ${-t.scrollTop}px)`;
    if (gutter.current) gutter.current.style.transform = `translateY(${-t.scrollTop}px)`;
  };
  useEffect(sync, [value]);

  useEffect(() => {
    const t = area.current;
    if (!goTo || !t) return;
    const at =
      value
        .split("\n")
        .slice(0, goTo.line - 1)
        .join("\n").length + (goTo.line > 1 ? 1 : 0);
    t.focus();
    t.setSelectionRange(at, at);
    t.scrollTop = Math.max(0, (goTo.line - 3) * 20);
    sync();
    // Only when asked to go somewhere, not on every edit.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [goTo?.n]);

  return (
    <div className="flex overflow-hidden rounded-xl border border-line bg-panel-2 focus-within:border-accent/60">
      <div className="relative shrink-0 overflow-hidden border-r border-line/70 bg-panel/60 select-none" aria-hidden>
        <div ref={gutter} className="py-3 font-mono text-xs leading-5 text-muted/70">
          {Array.from({ length: lines }, (_, i) => (
            <div key={i} className={`px-2 text-right tabular-nums ${i + 1 === errorLine ? "bg-bad/15 font-semibold text-bad" : ""}`}>
              {i + 1}
            </div>
          ))}
        </div>
      </div>
      <div className="relative min-w-0 flex-1 overflow-hidden">
        <pre ref={code} aria-hidden className={`pointer-events-none absolute top-0 left-0 min-w-full ${TEXT}`}>
          {errorLine && errorLine <= lines && (
            <span className="absolute inset-x-0 h-5 bg-bad/10" style={{ top: `calc(0.75rem + ${(errorLine - 1) * 20}px)` }} />
          )}
          {tokens.map((t, i) => (
            <span key={i} className={`relative ${COLOURS[t.kind]}`}>
              {t.text}
            </span>
          ))}
          {/* A trailing newline needs a character after it to take up its line. */}
          {"\n "}
        </pre>
        <textarea
          ref={area}
          value={value}
          readOnly={readOnly}
          spellCheck={false}
          wrap="off"
          autoCapitalize="off"
          autoComplete="off"
          onChange={(e) => onChange(e.target.value)}
          onScroll={sync}
          onKeyDown={(e) => {
            if (e.key !== "Tab" || readOnly || e.shiftKey) return;
            e.preventDefault();
            const t = e.currentTarget;
            const { selectionStart: s, selectionEnd: end } = t;
            onChange(value.slice(0, s) + "    " + value.slice(end));
            requestAnimationFrame(() => t.setSelectionRange(s + 4, s + 4));
          }}
          rows={Math.min(40, Math.max(14, lines + 2))}
          className={`relative block w-full resize-y overflow-auto bg-transparent text-transparent caret-fg outline-none selection:bg-accent/25 selection:text-transparent read-only:opacity-90 ${TEXT}`}
          aria-label={label}
        />
      </div>
    </div>
  );
}

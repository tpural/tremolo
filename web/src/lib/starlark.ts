/** Token kinds the rule editor colours. */
export type TokenKind = "comment" | "string" | "number" | "keyword" | "builtin" | "constant" | "plain";

export interface Token {
  kind: TokenKind;
  text: string;
}

const KEYWORDS = new Set([
  "and",
  "break",
  "continue",
  "def",
  "elif",
  "else",
  "for",
  "if",
  "in",
  "lambda",
  "load",
  "not",
  "or",
  "pass",
  "return",
  "while",
]);
const CONSTANTS = new Set(["None", "True", "False"]);
// tremolo's own functions, and the Starlark built-ins rules use most.
const BUILTINS = new Set([
  "alert",
  "ok",
  "metric",
  "metrics",
  "series",
  "logs",
  "log_count",
  "hosts",
  "print",
  "len",
  "str",
  "int",
  "float",
  "dict",
  "list",
  "range",
  "sorted",
  "min",
  "max",
  "sum",
  "any",
  "all",
  "enumerate",
  "fail",
  "type",
]);

const PATTERN =
  /(#[^\n]*)|("""[\s\S]*?(?:"""|$)|'''[\s\S]*?(?:'''|$)|"(?:[^"\\\n]|\\.)*"?|'(?:[^'\\\n]|\\.)*'?)|(\b\d+(?:\.\d+)?(?:e[+-]?\d+)?\b)|([A-Za-z_][A-Za-z0-9_]*)/g;

/** tokenize splits Starlark source into coloured runs; joined, they are the source exactly. */
export function tokenize(src: string): Token[] {
  const out: Token[] = [];
  let last = 0;
  const push = (kind: TokenKind, text: string) => {
    if (!text) return;
    const prev = out[out.length - 1];
    if (prev && prev.kind === kind) prev.text += text;
    else out.push({ kind, text });
  };
  for (const m of src.matchAll(PATTERN)) {
    push("plain", src.slice(last, m.index));
    const [text, comment, string, number, word] = m;
    if (comment) push("comment", text);
    else if (string) push("string", text);
    else if (number) push("number", text);
    else if (word && KEYWORDS.has(word)) push("keyword", text);
    else if (word && CONSTANTS.has(word)) push("constant", text);
    // A builtin only when called, so a variable named `logs` stays plain.
    else if (word && BUILTINS.has(word) && /^\s*\(/.test(src.slice(m.index + text.length))) push("builtin", text);
    else push("plain", text);
    last = m.index + text.length;
  }
  push("plain", src.slice(last));
  return out;
}

/**
 * errorLine finds the line of the rule's own file that an error points at: the last
 * `file.star:LINE:COL` in a traceback that names it, or in any file when none does.
 */
export function errorLine(message: string | undefined, file: string): number | undefined {
  if (!message) return undefined;
  const all = [...message.matchAll(/([\w.-]+\.star):(\d+):\d+/g)];
  const own = all.filter((m) => m[1] === file);
  const hit = (own.length ? own : all).at(-1);
  return hit ? Number(hit[2]) : undefined;
}

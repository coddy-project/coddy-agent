/**
 * Formula delimiters the Markdown parser does not know. Many models write
 * `\(x\)` and `\[x\]` (LaTeX's own delimiters) rather than `$x$` and `$$x$$`,
 * and Markdown reads `\(` as an escaped parenthesis, so the formula would come
 * out as plain text with its backslashes gone. This pass rewrites them to the
 * dollar forms remark-math parses, outside fenced code and code spans, and only
 * when the pair closes within the same paragraph.
 */

import remarkGfm from "remark-gfm";
import remarkParse from "remark-parse";
import { unified } from "unified";

/** A fence opener after any quote markers and any indentation (a list nests it deeper). */
const FENCE_OPEN = /^((?:[ \t]*>)*[ \t]*)(`{3,}|~{3,})/;
const BLANK_LINE = /\n[ \t\r]*\n/;
const BLANK = /^[ \t\r]*$/;
/** Indented code: four spaces or a tab, after a blank line. */
const INDENTED = /^(?: {4}|\t)/;

export function normalizeMathDelimiters(text: string): string {
  if (!text.includes("\\(") && !text.includes("\\[") && !text.includes("$"))
    return text;
  const lines = text.split("\n");
  const out: string[] = [];
  let prose: string[] = [];
  let fence: { char: string; size: number } | null = null;
  let indented = false;
  let previousBlank = true;
  const flush = () => {
    if (prose.length)
      out.push(rewriteProse(escapeLiteralDollars(prose.join("\n"))));
    prose = [];
  };
  for (const line of lines) {
    if (fence) {
      out.push(line);
      const close = line.match(FENCE_OPEN);
      if (
        close &&
        close[2]![0] === fence.char &&
        close[2]!.length >= fence.size &&
        line.slice(close[0].length).trim() === ""
      ) {
        fence = null;
      }
      previousBlank = false;
      continue;
    }
    const open = line.match(FENCE_OPEN);
    // A backtick fence's info string may not hold a backtick; such a line is
    // inline code, not a fence.
    if (
      open &&
      !(open[2]![0] === "`" && line.slice(open[0].length).includes("`"))
    ) {
      flush();
      out.push(line);
      fence = { char: open[2]![0]!, size: open[2]!.length };
      indented = false;
      previousBlank = false;
      continue;
    }
    // Indented code (or a list's indented paragraph, which is left alone too:
    // passing it through untouched is the safe side of the ambiguity).
    if (
      INDENTED.test(line) &&
      !BLANK.test(line) &&
      (previousBlank || indented)
    ) {
      flush();
      out.push(line);
      indented = true;
      previousBlank = false;
      continue;
    }
    if (!BLANK.test(line)) indented = false;
    previousBlank = BLANK.test(line);
    prose.push(line);
  }
  flush();
  return out.join("\n");
}

/** What opens a line inside a container: quote markers, a list marker, indentation. */
const CONTAINER_PREFIX = /^(?:[ \t]*>)+|^[ \t]*(?:[-*+]|\d+[.)])[ \t]+|^[ \t]+/;

function linePrefix(text: string, at: number): string {
  const start = text.lastIndexOf("\n", at - 1) + 1;
  return text.slice(start, at);
}

/**
 * Escapes, in a stretch of prose with no fenced code in it, every single
 * dollar that would open a formula remark-math should not see: one whose
 * partner dollar sits inside a code span (`${CODDY_HOME}/x and \`echo $HOME\``
 * would otherwise swallow the opening backtick and break the code), and one
 * whose span is text by the dollar rule (isLiteralDollarSpan). A pair that
 * reads as a formula is left as it is; dollars inside code spans are code.
 */
function escapeLiteralDollars(text: string): string {
  if (!text.includes("$")) return text;
  const spans = codeSpans(text);
  const inCode = (at: number) => spans.some(([a, b]) => at >= a && at < b);
  const escapeAt = new Set<number>();
  let i = 0;
  while (i < text.length) {
    const span = spans.find(([a, b]) => i >= a && i < b);
    if (span) {
      i = span[1];
      continue;
    }
    if (text[i] === "\\") {
      i += 2;
      continue;
    }
    if (text[i] !== "$") {
      i++;
      continue;
    }
    if (text[i + 1] === "$") {
      // A double dollar: display or inline $$...$$, never mistaken for prose.
      let end = i;
      while (text[end] === "$") end++;
      i = end;
      continue;
    }
    const close = nextSingleDollar(text, i + 1);
    if (close < 0) {
      i++;
      continue;
    }
    if (
      inCode(close) ||
      isLiteralDollarSpan(text, i, close + 1, text.slice(i + 1, close))
    ) {
      escapeAt.add(i);
      i++;
      continue;
    }
    // A formula: its content is not prose to scan.
    i = close + 1;
  }
  if (escapeAt.size === 0) return text;
  let out = "";
  for (let k = 0; k < text.length; k++)
    out += escapeAt.has(k) ? "\\$" : text[k];
  return out;
}

/** The next lone `$` after `from` the way remark-math pairs it: past code, not past a blank line. */
function nextSingleDollar(text: string, from: number): number {
  for (let i = from; i < text.length; i++) {
    const ch = text[i];
    if (ch === "\n" && /^\n[ \t\r]*\n/.test(text.slice(i, i + 64))) return -1;
    if (ch === "\\") {
      i++;
      continue;
    }
    if (ch !== "$") continue;
    if (text[i + 1] === "$") {
      while (text[i + 1] === "$") i++;
      continue;
    }
    return i;
  }
  return -1;
}

/** The [start, end) ranges of the code spans in a stretch of prose. */
function codeSpans(text: string): Array<[number, number]> {
  const out: Array<[number, number]> = [];
  let i = 0;
  while (i < text.length) {
    if (text[i] === "\\") {
      i += 2;
      continue;
    }
    if (text[i] !== "`") {
      i++;
      continue;
    }
    const end = codeSpanEnd(text, i);
    let run = i;
    while (text[run] === "`") run++;
    if (end > run) out.push([i, end]);
    i = end;
  }
  return out;
}

/** Rewrites the delimiters of a stretch of prose with no fenced code in it. */
function rewriteProse(text: string): string {
  let out = "";
  let i = 0;
  while (i < text.length) {
    const ch = text[i]!;
    if (ch === "`") {
      const end = codeSpanEnd(text, i);
      out += text.slice(i, end);
      i = end;
      continue;
    }
    if (ch !== "\\") {
      out += ch;
      i++;
      continue;
    }
    const next = text[i + 1];
    if (next === "(" || next === "[") {
      const closer = next === "(" ? "\\)" : "\\]";
      const close = findCloser(text, i + 2, closer);
      if (close >= 0) {
        const inner = text.slice(i + 2, close).trim();
        const before = i > 0 ? text[i - 1]! : "";
        if (inner && !WORD_BEFORE.test(before) && looksLikeFormula(inner)) {
          if (next === "(") {
            out += `$${inner}$`;
          } else if (CONTAINER_PREFIX.test(linePrefix(text, i))) {
            // Inside a quote or a list a block of its own would leave the
            // container: keep the formula on its line, joined into one.
            out += `$$${inner.replace(/\n(?:[ \t]*>)*[ \t]*/g, " ")}$$`;
          } else {
            const lead = out.length === 0 || out.endsWith("\n") ? "" : "\n";
            const afterAt = close + 2;
            const trail =
              afterAt >= text.length || text[afterAt] === "\n" ? "" : "\n";
            out += `${lead}$$\n${inner}\n$$${trail}`;
          }
          i = close + 2;
          continue;
        }
      }
    }
    // Any other backslash escapes the next character: keep the pair as it is,
    // so `\\(` stays a literal backslash before a parenthesis.
    out += text.slice(i, i + 2);
    i += 2;
  }
  return out;
}

const WORD_BEFORE = /[\p{L}\p{N}_]/u;

/**
 * Whether the text between `\(` and `\)` (or `\[` and `\]`) reads as a formula
 * rather than prose a writer escaped: Markdown escapes brackets and parentheses
 * too (`a\[0\]`, `\(see below\)`). A formula has a TeX command, a script, a
 * group or an operator, or is a short name such as `x` or `n`.
 */
function looksLikeFormula(inner: string): boolean {
  return (
    /[\\^_{}=<>+*/|-]/.test(inner) || /^[A-Za-z][A-Za-z0-9]{0,2}$/.test(inner)
  );
}

/**
 * Where the code span opening at `start` ends (just past its closing run), or
 * just past the opening run when nothing closes it: unmatched backticks are text.
 */
function codeSpanEnd(text: string, start: number): number {
  let run = start;
  while (text[run] === "`") run++;
  const size = run - start;
  let at = run;
  while (at < text.length) {
    const found = text.indexOf("`", at);
    if (found < 0) break;
    let end = found;
    while (text[end] === "`") end++;
    if (end - found === size) {
      const blank = text.slice(run, found).search(BLANK_LINE);
      return blank >= 0 ? run : end;
    }
    at = end;
  }
  return run;
}

/**
 * The index of `closer` after `from`, skipping escapes, before any blank line,
 * a new list item or a change of quote depth: a pair does not span containers.
 */
function findCloser(text: string, from: number, closer: string): number {
  const depth = quoteDepth(linePrefix(text, from));
  for (let i = from; i < text.length; i++) {
    const ch = text[i];
    if (ch === "\n") {
      const rest = text.slice(i, i + 64);
      if (/^\n[ \t\r]*\n/.test(rest)) return -1;
      const line = text.slice(
        i + 1,
        text.indexOf("\n", i + 1) < 0 ? text.length : text.indexOf("\n", i + 1),
      );
      if (/^(?:[ \t]*>)*[ \t]*(?:[-*+]|\d+[.)])[ \t]/.test(line)) return -1;
      if (quoteDepth(line) !== depth) return -1;
      continue;
    }
    if (ch !== "\\") continue;
    if (text.startsWith(closer, i)) return i;
    i++;
  }
  return -1;
}

function quoteDepth(line: string): number {
  const m = /^(?:[ \t]*>)*/.exec(line);
  return m ? (m[0].match(/>/g) || []).length : 0;
}

/**
 * Whether a `$...$` match is ordinary text rather than a formula: "costs $5 and
 * $10" parses as math holding "5 and ", and no formula starts or ends with a
 * space (Pandoc's rule for dollar math).
 */
export function isLiteralDollarMath(source: string): boolean {
  return source === "" || /^\s|\s$/.test(source);
}

type MdNode = {
  type: string;
  value?: string;
  children?: MdNode[];
  position?: { start: { offset?: number }; end: { offset?: number } };
};

const WORD_CHAR = /[\p{L}\p{N}_]/u;
/** `${NAME}`: a shell or config reference, which agents quote all the time. */
const VARIABLE_REFERENCE = /^\{[A-Za-z_][A-Za-z0-9_]*\}/;

/**
 * Whether the single-dollar formula `source`, written at [start, end) of
 * `text`, is ordinary text: Pandoc's rule (no space inside either dollar, no
 * digit or letter right outside them), plus a `${NAME}` reference. Prices,
 * `$HOME$PATH`, `$argon2id$v=19` and `${CODDY_HOME}/x and ${CWD}` stay text.
 */
export function isLiteralDollarSpan(
  text: string,
  start: number,
  end: number,
  source: string,
): boolean {
  if (isLiteralDollarMath(source)) return true;
  if (VARIABLE_REFERENCE.test(source)) return true;
  const before = start > 0 ? text[start - 1]! : "";
  const after = end < text.length ? text[end]! : "";
  return WORD_CHAR.test(before) || WORD_CHAR.test(after);
}

/**
 * A remark plugin, after remark-math: an inline formula that is really text
 * goes back to the text it was written as. Double-dollar spans are left alone:
 * nobody writes `$$` around prose by accident outside code.
 */
export function remarkLiteralDollars() {
  return (tree: MdNode, file: { value?: unknown }) => {
    const text =
      typeof file.value === "string" ? file.value : String(file.value ?? "");
    const walk = (node: MdNode) => {
      if (!node.children) return;
      node.children = node.children.flatMap((child) => {
        if (child.type !== "inlineMath") {
          walk(child);
          return [child];
        }
        const start = child.position?.start.offset;
        const end = child.position?.end.offset;
        if (start === undefined || end === undefined) return [child];
        const raw = text.slice(start, end);
        if (raw.startsWith("$$")) return [child];
        if (!isLiteralDollarSpan(text, start, end, child.value ?? ""))
          return [child];
        return reparseWithoutMath(raw);
      });
    };
    walk(tree);
  };
}

/**
 * The inline content of a span remark-math took for a formula, read again as
 * ordinary Markdown: between "$10/month and the **Pro** plan is $" lie a bold
 * word and maybe a link, which must not come out as raw asterisks.
 */
const plainParser = unified().use(remarkParse).use(remarkGfm);

function reparseWithoutMath(raw: string): MdNode[] {
  const root = plainParser.runSync(plainParser.parse(raw)) as unknown as MdNode;
  const first = root.children?.[0];
  if (
    root.children?.length === 1 &&
    first?.type === "paragraph" &&
    first.children
  ) {
    return first.children;
  }
  return [{ type: "text", value: raw }];
}

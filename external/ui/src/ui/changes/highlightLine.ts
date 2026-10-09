import { createLowlight } from "lowlight";
import { syntaxHighlightOptions } from "../markdown/syntaxLanguages";

/**
 * Syntax colouring for the edits window and the Files window.
 *
 * lowlight is highlight.js behind a tree API rather than an HTML string, which
 * is what the markdown renderer already uses through rehype-highlight. Taking
 * the tree means the views render React elements and never have to inject
 * markup, and the `hljs-*` class names line up with the styles already in the
 * stylesheet. The grammars are the chat's own registry (syntaxLanguages.ts),
 * so a language the chat's code blocks colour is coloured here too, and
 * nothing is bundled twice.
 *
 * A diff colours one line at a time (`highlightLine`), so a construct that
 * spans several lines - a block comment, a template literal - is coloured as if
 * it began on the line being drawn. That is the trade every line-addressed diff
 * view makes: the alternative is highlighting whole files the viewer never
 * fetches. An open file colours the lines it holds as one text
 * (`highlightLines`), so the same comment keeps its colour on every line.
 */

const lowlight = createLowlight(syntaxHighlightOptions.languages);
lowlight.registerAlias(syntaxHighlightOptions.aliases);

/** A line longer than this is left plain: minified input can stall a grammar. */
const MAX_LINE_LENGTH = 4096;

/** One run of characters sharing a token class ("" when the token has none). */
export interface HighlightSpan {
  text: string;
  className: string;
}

interface HastText {
  type: "text";
  value: string;
}

interface HastElement {
  type: "element";
  properties?: { className?: unknown };
  children?: HastNode[];
}

type HastNode = HastText | HastElement | { type: string };

// A sub-scoped token carries two classes ("hljs-title class_", "hljs-variable
// language_"): the stylesheet colours the hljs- one, so that is the one kept.
function classOf(node: HastElement): string {
  const raw = node.properties?.className;
  if (Array.isArray(raw) && raw.length > 0) {
    const names = raw.map(String);
    return names.find((name) => name.startsWith("hljs-")) ?? names[0]!;
  }
  return "";
}

function flatten(
  nodes: HastNode[],
  inherited: string,
  out: HighlightSpan[],
): void {
  for (const node of nodes) {
    if (node.type === "text") {
      const text = (node as HastText).value;
      if (text !== "") {
        out.push({ text, className: inherited });
      }
      continue;
    }
    if (node.type === "element") {
      const element = node as HastElement;
      // The innermost class wins: the stylesheet targets single token classes,
      // and a nested pair like string > subst would otherwise render as both.
      const own = classOf(element) || inherited;
      flatten(element.children ?? [], own, out);
    }
  }
}

/** Whether a grammar of that name (or alias) is registered. */
export function isHighlightable(language: string): boolean {
  return language !== "" && lowlight.registered(language);
}

function spansOf(language: string, content: string): HighlightSpan[] | null {
  try {
    const tree = lowlight.highlight(language, content);
    const spans: HighlightSpan[] = [];
    flatten(tree.children as HastNode[], "", spans);
    return spans;
  } catch {
    // A grammar that throws on a fragment must not take the view down with it.
    return null;
  }
}

export function highlightLine(
  content: string,
  language: string,
): HighlightSpan[] | null {
  if (content === "" || !isHighlightable(language)) {
    return null;
  }
  const spans = spansOf(language, content);
  return spans && spans.length > 0 ? spans : null;
}

/**
 * Colours consecutive lines as one text and hands back the spans of each line
 * (an empty line gets none). A line longer than `MAX_LINE_LENGTH` stays plain
 * (null), and the lines on either side of it are coloured as texts of their
 * own. Every entry is null when the language is not one the highlighter knows.
 */
export function highlightLines(
  lines: readonly string[],
  language: string,
): (HighlightSpan[] | null)[] {
  const out: (HighlightSpan[] | null)[] = lines.map(() => null);
  if (!isHighlightable(language)) {
    return out;
  }
  let start = 0;
  for (let i = 0; i <= lines.length; i++) {
    if (i < lines.length && lines[i]!.length <= MAX_LINE_LENGTH) {
      continue;
    }
    if (i > start) {
      colourRun(lines, start, i, language, out);
    }
    start = i + 1;
  }
  return out;
}

/** Colours lines [start, end) as one text and splits the spans back into them. */
function colourRun(
  lines: readonly string[],
  start: number,
  end: number,
  language: string,
  out: (HighlightSpan[] | null)[],
): void {
  const spans = spansOf(language, lines.slice(start, end).join("\n"));
  if (!spans) {
    return;
  }
  let row = start;
  let current: HighlightSpan[] = [];
  for (const span of spans) {
    const parts = span.text.split("\n");
    for (let n = 0; n < parts.length; n++) {
      if (n > 0) {
        out[row] = current;
        row++;
        current = [];
      }
      if (parts[n] !== "") {
        current.push({ text: parts[n]!, className: span.className });
      }
    }
  }
  out[row] = current;
}

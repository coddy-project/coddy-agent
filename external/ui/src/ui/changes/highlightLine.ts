import { createLowlight, common } from "lowlight";

/**
 * Syntax colouring for one diff line.
 *
 * lowlight is highlight.js behind a tree API rather than an HTML string, which
 * is what the markdown renderer already uses through rehype-highlight. Taking
 * the tree means the diff can render React elements and never has to inject
 * markup, and the `hljs-*` class names line up with the styles already in the
 * stylesheet.
 *
 * A line is highlighted on its own, so a construct that spans several lines - a
 * block comment, a template literal - is coloured as if it began on the line
 * being drawn. That is the trade every line-addressed diff view makes: the
 * alternative is highlighting whole files the viewer never fetches.
 */

const lowlight = createLowlight(common);

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

function classOf(node: HastElement): string {
  const raw = node.properties?.className;
  if (Array.isArray(raw) && raw.length > 0) {
    return String(raw[raw.length - 1]);
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

export function highlightLine(
  content: string,
  language: string,
): HighlightSpan[] | null {
  if (content === "" || language === "") {
    return null;
  }
  if (!lowlight.registered(language)) {
    return null;
  }
  try {
    const tree = lowlight.highlight(language, content);
    const spans: HighlightSpan[] = [];
    flatten(tree.children as HastNode[], "", spans);
    return spans.length > 0 ? spans : null;
  } catch {
    // A grammar that throws on a fragment must not take the diff down with it.
    return null;
  }
}

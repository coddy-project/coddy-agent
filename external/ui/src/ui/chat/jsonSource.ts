/**
 * JSON read as the text it is. `JSON.parse` answers what a document means, and
 * printing that back is not what the server sent: an integer past 2^53 (a
 * Discord or Twitter snowflake) comes back rounded, `é` comes back as the
 * letter, and of two equal keys only the last survives. A tool card shows what
 * the server wrote, so `JSON.parse` only validates here and every value is a
 * slice of the original text.
 */

/** A value of a JSON document, with the text it was read from. */
export type JsonNode =
  | { kind: "object"; source: string; entries: JsonEntry[] }
  | { kind: "array"; source: string; items: JsonNode[] }
  /** `value` is the string the literal encodes, `source` the literal itself. */
  | { kind: "string"; source: string; value: string }
  /** A number, true, false or null. */
  | { kind: "literal"; source: string };

/** One key of an object in document order; a repeated key is an entry of its own. */
export type JsonEntry = { key: string; value: JsonNode };

const QUOTE = 34; // "
const BACKSLASH = 92; // \
const COMMA = 44; // ,
const COLON = 58; // :
const OPEN_BRACE = 123; // {
const CLOSE_BRACE = 125; // }
const OPEN_BRACKET = 91; // [
const CLOSE_BRACKET = 93; // ]

function isSpace(c: number): boolean {
  return c === 32 || c === 9 || c === 10 || c === 13;
}

/** The index just past the string literal that opens at `at`. */
function stringEnd(source: string, at: number): number {
  let j = at + 1;
  for (let k = source.charCodeAt(j); k !== QUOTE; k = source.charCodeAt(j)) {
    j += k === BACKSLASH ? 2 : 1;
  }
  return j + 1;
}

/** The index just past the number, true, false or null that starts at `at`. */
function literalEnd(source: string, at: number): number {
  let j = at + 1;
  const n = source.length;
  for (let k = source.charCodeAt(j); j < n; k = source.charCodeAt(++j)) {
    if (isSpace(k) || k === COMMA || k === CLOSE_BRACE || k === CLOSE_BRACKET) break;
  }
  return j;
}

/**
 * The document the text holds, every node carrying its own slice of the text;
 * undefined for anything `JSON.parse` rejects. Leading and trailing whitespace
 * is not part of the document.
 */
export function parseJsonSource(text: string): JsonNode | undefined {
  const source = text.trim();
  if (!source) return undefined;
  try {
    JSON.parse(source);
  } catch {
    return undefined;
  }
  let i = 0;
  const skipSpace = () => {
    while (isSpace(source.charCodeAt(i))) i++;
  };
  // The text parsed, so the walk below meets only well-formed tokens.
  const value = (): JsonNode => {
    skipSpace();
    const start = i;
    const c = source.charCodeAt(i);
    if (c === QUOTE) {
      i = stringEnd(source, i);
      const literal = source.slice(start, i);
      return { kind: "string", source: literal, value: JSON.parse(literal) as string };
    }
    if (c === OPEN_BRACE) {
      i++;
      const entries: JsonEntry[] = [];
      skipSpace();
      if (source.charCodeAt(i) === CLOSE_BRACE) {
        i++;
      } else {
        for (;;) {
          skipSpace();
          const keyStart = i;
          i = stringEnd(source, i);
          const key = JSON.parse(source.slice(keyStart, i)) as string;
          skipSpace();
          i++; // the colon
          entries.push({ key, value: value() });
          skipSpace();
          if (source.charCodeAt(i++) === CLOSE_BRACE) break; // else a comma
        }
      }
      return { kind: "object", source: source.slice(start, i), entries };
    }
    if (c === OPEN_BRACKET) {
      i++;
      const items: JsonNode[] = [];
      skipSpace();
      if (source.charCodeAt(i) === CLOSE_BRACKET) {
        i++;
      } else {
        for (;;) {
          items.push(value());
          skipSpace();
          if (source.charCodeAt(i++) === CLOSE_BRACKET) break; // else a comma
        }
      }
      return { kind: "array", source: source.slice(start, i), items };
    }
    i = literalEnd(source, i);
    return { kind: "literal", source: source.slice(start, i) };
  };
  try {
    return value();
  } catch {
    // Nesting deep enough to exhaust the stack: shown as the text it is.
    return undefined;
  }
}

/**
 * Past a mebibyte a document is shown as it came. Indenting runs on the main
 * thread when the reader opens the row, at about 50ms a megabyte, and the
 * indented text is two thirds longer again for the page to lay out.
 */
export const INDENT_JSON_MAX_CHARS = 1 << 20;

/**
 * A JSON text laid out the way `JSON.stringify(value, null, 2)` lays out its
 * value, with every string and number copied from the text as it is. Text that
 * is not JSON, and a document past {@link INDENT_JSON_MAX_CHARS}, comes back
 * unchanged.
 */
export function indentJson(text: string): string {
  const source = text.trim();
  if (source.length > INDENT_JSON_MAX_CHARS) return text;
  try {
    JSON.parse(source);
  } catch {
    return text;
  }

  const parts: string[] = [];
  const indents: string[] = ["\n"];
  const newline = (depth: number) => {
    while (indents.length <= depth) indents.push(indents[indents.length - 1] + "  ");
    return indents[depth]!;
  };
  const n = source.length;
  let depth = 0;
  let i = 0;
  while (i < n) {
    const c = source.charCodeAt(i);
    if (c === QUOTE) {
      const end = stringEnd(source, i);
      parts.push(source.slice(i, end));
      i = end;
    } else if (c === OPEN_BRACE || c === OPEN_BRACKET) {
      // An empty container stays on its line, as JSON.stringify prints it.
      let j = i + 1;
      while (j < n && isSpace(source.charCodeAt(j))) j++;
      if (source.charCodeAt(j) === (c === OPEN_BRACE ? CLOSE_BRACE : CLOSE_BRACKET)) {
        parts.push(c === OPEN_BRACE ? "{}" : "[]");
        i = j + 1;
      } else {
        depth++;
        parts.push(source[i]! + newline(depth));
        i++;
      }
    } else if (c === CLOSE_BRACE || c === CLOSE_BRACKET) {
      depth--;
      parts.push(newline(depth) + source[i]!);
      i++;
    } else if (c === COMMA) {
      parts.push("," + newline(depth));
      i++;
    } else if (c === COLON) {
      parts.push(": ");
      i++;
    } else if (isSpace(c)) {
      i++;
    } else {
      const end = literalEnd(source, i);
      parts.push(source.slice(i, end));
      i = end;
    }
  }
  return parts.join("");
}

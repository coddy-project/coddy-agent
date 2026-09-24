/**
 * An MCP server answers with whatever its tool returns, and a JSON document comes
 * serialised on one line: wrapped at the card's width it is a wall of text, and
 * unwrapped it is wider than the transcript. A whole document - an object or an
 * array - is shown indented by two spaces, the way `JSON.stringify(value, null, 2)`
 * lays it out. Anything else is returned as it came: a preview cut short, prose that
 * happens to open with a bracket, a bare string or number, and a document longer
 * than {@link INDENT_JSON_MAX_CHARS}.
 *
 * The document is re-indented as text rather than parsed and printed again, so what
 * the server sent survives to the character: a 64-bit id stays exact where a JS
 * number would round it, an escape such as `é` stays an escape, and a key the
 * document repeats is not folded into one. Strings and literals are copied as whole
 * slices.
 */

/**
 * Past a mebibyte the answer is shown as it came. Re-indenting runs on the main
 * thread when the reader opens the row, at about 50ms a megabyte, and the indented
 * text is two thirds longer again for the page to lay out.
 */
export const INDENT_JSON_MAX_CHARS = 1 << 20;

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

export function indentJsonDocument(text: string): string {
  const source = text.trim();
  if (source.length > INDENT_JSON_MAX_CHARS) return text;
  if (!source.startsWith("{") && !source.startsWith("[")) return text;
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
      // The document parsed, so every string closes: copy it whole, escapes and all.
      let j = i + 1;
      for (let k = source.charCodeAt(j); k !== QUOTE; k = source.charCodeAt(j)) {
        j += k === BACKSLASH ? 2 : 1;
      }
      parts.push(source.slice(i, j + 1));
      i = j + 1;
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
      // A number, true, false or null: copy the run up to the next token.
      let j = i + 1;
      for (let k = source.charCodeAt(j); j < n; k = source.charCodeAt(++j)) {
        if (isSpace(k) || k === COMMA || k === CLOSE_BRACE || k === CLOSE_BRACKET) break;
      }
      parts.push(source.slice(i, j));
      i = j;
    }
  }
  return parts.join("");
}

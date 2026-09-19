/**
 * The composer's **`@`** mentions: the grammar that finds them in a text
 * (**`parseMentions`**, the twin of **`internal/mention/grammar.go`** **`Parse`**),
 * the spans the composer chips, and the draft behind the **`@`** picker.
 *
 * Both twins of the grammar are held to the literals of
 * **`internal/mention/testdata/grammar_cases.json`**, so the composer highlights
 * exactly the references the server resolves.
 */

import { blockquoteLine, inMarkdownFenceBeforeCaret } from "./draftSlash";

/**
 * A meta reference: **`@session:<id>`**, **`@rule:<name>`**, **`@agent:<name>`**,
 * **`@coddy:<page>#<section>`** (a page of the built-in documentation).
 */
export type MentionScheme = "session" | "rule" | "agent" | "coddy";

/** The meta schemes, in the order a picker offers them (**`mention.Schemes`**). */
const MENTION_SCHEMES: readonly MentionScheme[] = ["session", "rule", "agent", "coddy"];

/** One way to read a path token (**`mention.PathReading`**). */
export type MentionReading = {
  path: string;
  /** 1-based inclusive line range a **`:N-M`** or **`#L`** suffix narrowed it to. */
  lines?: { start: number; end: number };
};

/** One **`@`** reference in a text (**`mention.Token`**). */
export type MentionToken = {
  /** Index of the **`@`**. */
  start: number;
  /** Just past the longest reading (or the reference): the span a surface highlights. */
  end: number;
  kind: "path" | "scheme" | "url";
  /** Set for a meta reference (**`@session:sess_1`**). */
  scheme?: MentionScheme;
  ref?: string;
  /** Set for a web page (**`@https://example.com/page`**). */
  url?: string;
  /** A path written as **`@"a path with spaces"`**. */
  quoted?: boolean;
  /** The readings of a path token, longest first; empty for a scheme or a URL. */
  readings: MentionReading[];
};

type LineRange = { start: number; end: number };

/** What may come right before an **`@`** that opens a reference, besides whitespace. */
const MENTION_OPENERS = `([{"'`;

/**
 * Go's **`unicode.IsSpace`**. It is not JavaScript's **`\s`**: U+0085 is a
 * space to Go and U+FEFF is not.
 */
function isGoSpace(cp: number): boolean {
  return (
    (cp >= 0x09 && cp <= 0x0d) ||
    cp === 0x20 ||
    cp === 0x85 ||
    cp === 0xa0 ||
    cp === 0x1680 ||
    (cp >= 0x2000 && cp <= 0x200a) ||
    cp === 0x2028 ||
    cp === 0x2029 ||
    cp === 0x202f ||
    cp === 0x205f ||
    cp === 0x3000
  );
}

const LETTER_RE = /^\p{L}$/u;
const NUMBER_RE = /^\p{N}$/u;

/** Go's **`unicode.IsLetter`** over one code point. */
function isLetter(cp: number): boolean {
  if (cp < 0x80) {
    return (cp >= 0x41 && cp <= 0x5a) || (cp >= 0x61 && cp <= 0x7a);
  }
  return LETTER_RE.test(String.fromCodePoint(cp));
}

/** Go's **`unicode.IsNumber`** over one code point. */
function isNumber(cp: number): boolean {
  if (cp < 0x80) {
    return cp >= 0x30 && cp <= 0x39;
  }
  return NUMBER_RE.test(String.fromCodePoint(cp));
}

function isASCIILetter(cp: number): boolean {
  return (cp >= 0x41 && cp <= 0x5a) || (cp >= 0x61 && cp <= 0x7a);
}

/** UTF-16 width of a code point: two units for one outside the BMP. */
function cpWidth(cp: number): number {
  return cp > 0xffff ? 2 : 1;
}

/** Go's **`strings.TrimSpace`**. */
function goTrimSpace(s: string): string {
  let a = 0;
  let b = s.length;
  while (a < b && isGoSpace(s.charCodeAt(a))) {
    a++;
  }
  while (b > a && isGoSpace(s.charCodeAt(b - 1))) {
    b--;
  }
  return s.slice(a, b);
}

function trimRightDots(s: string): string {
  let end = s.length;
  while (end > 0 && s.charCodeAt(end - 1) === 0x2e) {
    end--;
  }
  return s.slice(0, end);
}

function isMentionScheme(s: string): s is MentionScheme {
  return (MENTION_SCHEMES as readonly string[]).includes(s);
}

/**
 * Returns the **`@`** references of **`text`** in document order, mirroring
 * **`mention.Parse`**: a reference starts at an **`@`** that opens the text or
 * follows whitespace, an opening bracket or a quote; one inside a fenced code
 * block, an inline code span or a blockquote line is prose. Offsets are
 * JavaScript string indices.
 */
export function parseMentions(text: string): MentionToken[] {
  const out: MentionToken[] = [];
  let inFence = false;
  // The next "@" at or after the current line: a line without one is not read.
  let nextAt = text.indexOf("@");
  for (let lineStart = 0; lineStart <= text.length; ) {
    const nl = text.indexOf("\n", lineStart);
    const lineEnd = nl < 0 ? text.length : nl;
    let lead = lineStart;
    while (lead < lineEnd && (text[lead] === " " || text[lead] === "\t")) {
      lead++;
    }
    if (text.startsWith("```", lead)) {
      // A fence line opens or closes a code block and holds no reference.
      inFence = !inFence;
    } else if (
      !inFence &&
      text[lead] !== ">" &&
      nextAt >= 0 &&
      nextAt < lineEnd
    ) {
      // Code and quoted text are prose; everything else is read line by line.
      parseLine(text, lineStart, lineEnd, out);
    }
    lineStart = lineEnd + 1;
    if (nextAt >= 0 && nextAt < lineStart) {
      nextAt = text.indexOf("@", lineStart);
    }
  }
  return out;
}

/** Reads the references of one line: no form of reference crosses a line break. */
function parseLine(
  text: string,
  lineStart: number,
  lineEnd: number,
  out: MentionToken[],
): void {
  // Backticks seen between lineStart and tickPos: an odd count is an open
  // inline code span ("`@Override`" is code).
  let ticks = 0;
  let tickPos = lineStart;
  let i = lineStart;
  while (i < lineEnd) {
    const j = text.indexOf("@", i);
    if (j < 0 || j >= lineEnd) {
      return;
    }
    i = j + 1;
    if (j > 0) {
      const prev = text[j - 1]!;
      if (!isGoSpace(prev.charCodeAt(0)) && !MENTION_OPENERS.includes(prev)) {
        continue;
      }
    }
    for (; tickPos < j; tickPos++) {
      if (text.charCodeAt(tickPos) === 0x60) {
        ticks++;
      }
    }
    if (ticks % 2 !== 0) {
      continue;
    }
    const tok = parseAt(text, j);
    if (tok) {
      out.push(tok);
      if (tok.end > i) {
        i = tok.end;
      }
    }
  }
}

function parseAt(text: string, at: number): MentionToken | null {
  const rest = at + 1;
  if (text.startsWith('"', rest)) {
    return parseQuoted(text, at);
  }
  if (text.startsWith("https://", rest) || text.startsWith("http://", rest)) {
    return parseURL(text, at);
  }
  return parseScheme(text, at) ?? parsePathRun(text, at);
}

/** Reads **`@"a path"`** with an optional range suffix after the quote. */
function parseQuoted(text: string, at: number): MentionToken | null {
  const start = at + 2;
  let close = start;
  while (close < text.length && text[close] !== '"' && text[close] !== "\n") {
    close++;
  }
  if (close >= text.length || text[close] !== '"') {
    return null;
  }
  const path = text.slice(start, close);
  if (goTrimSpace(path) === "") {
    return null;
  }
  const after = close + 1;
  const range = parseAtLineRangeSuffix(text, after);
  const reading: MentionReading = range
    ? { path, lines: { start: range.start, end: range.end } }
    : { path };
  return {
    start: at,
    end: range ? range.next : after,
    kind: "path",
    quoted: true,
    readings: [reading],
  };
}

/**
 * Reads **`@https://...`** up to the next whitespace. Punctuation that closes a
 * sentence is not part of the address, nor is a closing bracket the address
 * never opened: **`(see @https://x.dev/a)`** names **`https://x.dev/a`**.
 */
function parseURL(text: string, at: number): MentionToken | null {
  const start = at + 1;
  let end = start;
  while (end < text.length) {
    const cp = text.codePointAt(end)!;
    if (
      isGoSpace(cp) ||
      cp === 0x22 /* " */ ||
      cp === 0x3c /* < */ ||
      cp === 0x3e /* > */ ||
      cp === 0x60 /* ` */
    ) {
      break;
    }
    end += cpWidth(cp);
  }
  while (end > start) {
    const c = text[end - 1]!;
    let trim = ".,;:!?'".includes(c);
    if (
      (c === ")" && !text.slice(start, end - 1).includes("(")) ||
      (c === "]" && !text.slice(start, end - 1).includes("[")) ||
      (c === "}" && !text.slice(start, end - 1).includes("{"))
    ) {
      trim = true;
    }
    if (!trim) {
      break;
    }
    end--;
  }
  const url = text.slice(start, end);
  let host = url.startsWith("https://") ? url.slice("https://".length) : url;
  host = host.startsWith("http://") ? host.slice("http://".length) : host;
  if (host === "" || host.startsWith("/")) {
    return null;
  }
  return { start: at, end, kind: "url", url, readings: [] };
}

/** Drops the trailing **`.`**, **`/`** and **`#`** of a documentation page reference. */
function trimRightPageRef(s: string): string {
  let end = s.length;
  while (end > 0 && "./#".includes(s[end - 1]!)) {
    end--;
  }
  return s.slice(0, end);
}

/** A character of a meta reference: letters, digits, **`_`**, **`-`** and **`.`** (ASCII). */
function isRefChar(c: number): boolean {
  return (
    c === 0x5f ||
    c === 0x2d ||
    c === 0x2e ||
    (c >= 0x30 && c <= 0x39) ||
    (c >= 0x61 && c <= 0x7a) ||
    (c >= 0x41 && c <= 0x5a)
  );
}

/**
 * Reads **`@session:<ref>`**, **`@rule:<ref>`**, **`@agent:<ref>`** and
 * **`@coddy:<ref>`**. A documentation page also takes **`/`** and **`#`**
 * (**`features/mentions#completion`**). A trailing **`.`**, and for a page a
 * trailing **`/`** or **`#`**, is the end of a sentence or of an unfinished
 * reference, not part of the name.
 */
function parseScheme(text: string, at: number): MentionToken | null {
  const rest = at + 1;
  const scheme = MENTION_SCHEMES.find((sc) => text.startsWith(`${sc}:`, rest));
  if (scheme === undefined) {
    return null;
  }
  const page = scheme === "coddy";
  const refStart = rest + scheme.length + 1;
  let k = refStart;
  while (k < text.length) {
    const c = text.charCodeAt(k);
    if (!isRefChar(c) && !(page && (c === 0x2f || c === 0x23))) {
      break;
    }
    k++;
  }
  const ref = page ? trimRightPageRef(text.slice(refStart, k)) : trimRightDots(text.slice(refStart, k));
  if (ref === "") {
    return null;
  }
  return {
    start: at,
    end: refStart + ref.length,
    kind: "scheme",
    scheme,
    ref,
    readings: [],
  };
}

/**
 * Whether **`r`** continues a path run. **`prev`** is the code point before it in
 * the run (-1 at the start) and **`runLen`** the units read so far.
 */
function isPathRune(
  r: number,
  prev: number,
  runLen: number,
  next: number,
): boolean {
  switch (r) {
    case 0x2e: // .
    case 0x2f: // /
    case 0x5c: // \
    case 0x5f: // _
    case 0x2d: // -
    case 0x7e: // ~
    case 0x2b: // +
      return true;
    case 0x40: // @
      // A scoped package folder: node_modules/@types/node.
      return prev === 0x2f || prev === 0x5c;
    case 0x3a: // :
      // A drive letter: C:\Users or C:/Users. Anywhere else a colon ends the
      // path, so a ":21-31" suffix stays a line range.
      return (
        runLen === 1 && isASCIILetter(prev) && (next === 0x2f || next === 0x5c)
      );
  }
  return isLetter(r) || isNumber(r);
}

/**
 * Whether the word at **`from`** (right after a space) still looks like part of
 * a path (**`notes draft.md`**): a word that carries a **`/`** or a **`.`**.
 */
function continuesPath(text: string, from: number): boolean {
  let end = from;
  while (end < text.length) {
    const r = text.codePointAt(end)!;
    const wordish = isLetter(r) || isNumber(r) || r === 0x5f;
    if (end === from && !wordish) {
      return false;
    }
    if (wordish || r === 0x2d || r === 0x2e || r === 0x2f) {
      end += cpWidth(r);
      continue;
    }
    break;
  }
  const word = trimRightDots(text.slice(from, end));
  return word !== "" && (word.includes("/") || word.includes("."));
}

/**
 * Drops readings that name nothing a user means: an empty token, the bare
 * root, a lone **`~`**, or dots alone (**`@.`** and **`@..`** in prose).
 */
function validPathReading(p: string): boolean {
  switch (goTrimSpace(p)) {
    case "":
    case "/":
    case "\\":
    case "~":
    case ".":
    case "..":
      return false;
  }
  return /[^.]/.test(p);
}

/**
 * Reads an unquoted path, with the space-continued and punctuation-trimmed
 * readings it may also stand for, longest first.
 */
function parsePathRun(text: string, at: number): MentionToken | null {
  const n = text.length;
  let k = at + 1;
  let prev = -1;
  // Offsets where a space continuation began: every one of them is also the
  // end of a shorter reading.
  const cuts: number[] = [];
  while (k < n) {
    const r = text.codePointAt(k)!;
    const size = cpWidth(r);
    const next = k + size < n ? text.codePointAt(k + size)! : -1;
    if (isPathRune(r, prev, k - (at + 1), next)) {
      prev = r;
      k += size;
      continue;
    }
    if (
      (r === 0x20 || r === 0x09) &&
      k > at + 1 &&
      continuesPath(text, k + size)
    ) {
      cuts.push(k);
      prev = r;
      k += size;
      continue;
    }
    break;
  }
  if (k === at + 1) {
    return null;
  }
  const readings: (MentionReading & { end: number })[] = [];
  const seen = new Set<string>();
  const add = (path: string, lines: LineRange | null, end: number) => {
    if (!validPathReading(path)) {
      return;
    }
    const key = lines ? `${path}#${lines.start}-${lines.end}` : path;
    if (seen.has(key)) {
      return;
    }
    seen.add(key);
    readings.push(lines ? { path, lines, end } : { path, end });
  };
  const full = text.slice(at + 1, k);
  let fullEnd = k;
  const range = parseAtLineRangeSuffix(text, k);
  if (range) {
    add(full, { start: range.start, end: range.end }, range.next);
    fullEnd = range.next;
  }
  const ends = [k, ...cuts.slice().reverse()];
  ends.forEach((end, idx) => {
    if (idx === 0 && range) {
      // The ranged reading is the only one of the full run: a range the file
      // cannot honour must never widen into the whole file.
      return;
    }
    const p = text.slice(at + 1, end);
    add(p, null, end);
    const trimmed = trimRightDots(p);
    if (trimmed !== p) {
      add(trimmed, null, at + 1 + trimmed.length);
    }
  });
  const first = readings[0];
  if (!first) {
    return null;
  }
  return {
    start: at,
    end: Math.max(fullEnd, first.end),
    kind: "path",
    readings: readings.map((r) =>
      r.lines ? { path: r.path, lines: r.lines } : { path: r.path },
    ),
  };
}

/** Reads 1 to 9 ASCII digits at **`p`**; a longer run is not a line number. */
function lineDigits(
  text: string,
  p: number,
): { value: number; next: number } | null {
  let q = p;
  while (
    q < text.length &&
    text.charCodeAt(q) >= 0x30 &&
    text.charCodeAt(q) <= 0x39
  ) {
    q++;
  }
  if (q === p || q - p > 9) {
    return null;
  }
  return { value: Number(text.slice(p, q)), next: q };
}

/**
 * Reads a line range right after a path: **`:<start>-<end>`**,
 * **`#L<start>-<end>`**, **`#L<start>-L<end>`**, **`#L<line>`**,
 * **`#<start>-<end>`** or **`#<line>`**. A range counts only when
 * 1 <= start <= end and the token ends there - the next character is not a
 * letter, a digit or **`-`** - so **`:21-31x`** and **`:21`** stay prose.
 * Mirrors **`parseRangeSuffix`** in **`internal/mention/grammar.go`**.
 */
function parseAtLineRangeSuffix(
  text: string,
  k: number,
): { start: number; end: number; next: number } | null {
  const n = text.length;
  if (k >= n) {
    return null;
  }
  if (text[k] === ":") {
    const first = lineDigits(text, k + 1);
    if (!first || first.next >= n || text[first.next] !== "-") {
      return null;
    }
    const second = lineDigits(text, first.next + 1);
    if (!second) {
      return null;
    }
    return finishLineRange(text, first.value, second.value, second.next);
  }
  if (text[k] === "#") {
    // "#L10-20", "#L10-L20" and "#L10" (GitHub, Claude Code), or "#10-20"
    // and "#10" (OpenCode).
    let p = k + 1;
    if (p < n && text[p] === "L") {
      p++;
    }
    const first = lineDigits(text, p);
    if (!first) {
      return null;
    }
    let last = first.value;
    let q = first.next;
    if (q < n && text[q] === "-") {
      q++;
      if (q < n && text[q] === "L") {
        q++;
      }
      const second = lineDigits(text, q);
      if (!second) {
        return null;
      }
      last = second.value;
      q = second.next;
    }
    return finishLineRange(text, first.value, last, q);
  }
  return null;
}

function finishLineRange(
  text: string,
  start: number,
  end: number,
  next: number,
): { start: number; end: number; next: number } | null {
  if (next < text.length) {
    const r = text.codePointAt(next)!;
    if (isLetter(r) || isNumber(r) || r === 0x2d) {
      return null;
    }
  }
  if (start < 1 || end < start) {
    return null;
  }
  return { start, end, next };
}

/** One completed **`@`** mention the composer chips. */
export type AtPathSpan = {
  start: number;
  end: number;
  /**
   * What the mention names: the first reading of a path token, **`session:<id>`**
   * (or **`rule:`**, **`agent:`**) for a meta reference, the address of a web page.
   */
  path: string;
  /** 1-based inclusive line range from a **`:N-M`** or **`#L`** suffix (**`@f.go:21-31`**). */
  lines?: { start: number; end: number };
};

/**
 * Spans of the **`@`** mentions of **`text`** in document order, one per token
 * of **`parseMentions`**: a path (a folder too - it attaches a listing), a
 * meta reference or a web page. A range suffix joins the span, so the mirror
 * chips **`@f.go:21-31`** as one token.
 */
export function listAtPathSpans(text: string): AtPathSpan[] {
  const out: AtPathSpan[] = [];
  for (const tok of parseMentions(text)) {
    if (tok.kind === "scheme") {
      out.push({
        start: tok.start,
        end: tok.end,
        path: `${tok.scheme}:${tok.ref}`,
      });
      continue;
    }
    if (tok.kind === "url") {
      out.push({ start: tok.start, end: tok.end, path: tok.url ?? "" });
      continue;
    }
    const first = tok.readings[0];
    if (!first) {
      continue;
    }
    out.push({
      start: tok.start,
      end: tok.end,
      path: first.path,
      ...(first.lines ? { lines: first.lines } : {}),
    });
  }
  return out;
}

/** One file an **`@path`** mention names. */
export type AtFileAttachment = {
  path: string;
  startLine?: number;
  endLine?: number;
};

/**
 * The files the **`@`** mentions of **`text`** name (the first reading of each
 * path token), for the recent-mentions list. A folder (**`@src/`**) is not a
 * file and is skipped, and so are meta references and web pages. Deduped by
 * path plus line range, so **`@f.go`** and **`@f.go:2-4`** are two entries.
 */
export function extractAtFileAttachments(text: string): AtFileAttachment[] {
  const out: AtFileAttachment[] = [];
  const seen = new Set<string>();
  for (const tok of parseMentions(text)) {
    const first = tok.kind === "path" ? tok.readings[0] : undefined;
    if (!first || first.path.endsWith("/") || first.path.endsWith("\\")) {
      continue;
    }
    const key = first.lines
      ? `${first.path}#L${first.lines.start}-${first.lines.end}`
      : first.path;
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);
    out.push({
      path: first.path,
      ...(first.lines
        ? { startLine: first.lines.start, endLine: first.lines.end }
        : {}),
    });
  }
  return out;
}

export type AtMenuDraft =
  | { open: false }
  | {
      open: true;
      lineStart: number;
      atIdx: number;
      caret: number;
      prefix: string;
    };

/**
 * Characters of an unquoted picker filter: path characters plus the space the
 * legacy space continuation allows (**`@notes draft.md`**).
 */
const MENU_PATH_CHAR = /^[\p{L}\p{N}_.\\/ ~+\-]$/u;

/**
 * When the menu prefix already ends in a dotted file segment and is followed by
 * optional spaces then text whose first word does not look like a path segment,
 * the user is typing the message body, not extending the picker filter.
 */
function prefixClosedAfterFileExtensionForAtMenu(prefix: string): boolean {
  const m = /^(.+\.[\p{L}\p{N}]{1,16})\s+(\S[\s\S]*)$/u.exec(prefix);
  if (!m) {
    return false;
  }
  const cont = (m[2] ?? "").trim();
  if (cont === "") {
    return false;
  }
  const word = /^([\p{L}\p{N}_][\p{L}\p{N}_.\-]*)/u.exec(cont);
  const w = word ? (word[1] ?? "") : "";
  if (w.includes("/") || w.includes(".")) {
    return false;
  }
  return true;
}

/** Whether the text typed after an **`@`** keeps the picker open. */
function atMenuPrefixOpen(prefix: string): boolean {
  if (prefix.startsWith('"')) {
    // An open quote holds anything up to its closing quote; once it closes
    // the mention is finished.
    return !prefix.includes('"', 1);
  }
  const colon = prefix.indexOf(":");
  if (colon >= 0) {
    // Only a meta scheme keeps its colon ("@session:" lists sessions).
    // Anywhere else a colon starts a line range, and that picker takes over.
    if (!isMentionScheme(prefix.slice(0, colon))) {
      return false;
    }
    // A reference never holds a space: one after it ends the mention. A
    // documentation page also names its section after "#".
    const page = prefix.slice(0, colon) === "coddy";
    for (const ch of prefix.slice(colon + 1)) {
      if (ch === " " || (!MENU_PATH_CHAR.test(ch) && !(page && ch === "#"))) {
        return false;
      }
    }
    return true;
  }
  let prev = "";
  for (const ch of prefix) {
    // An "@" right after a slash is part of the path ("node_modules/@types"),
    // as the grammar reads it; anywhere else it is not a path character.
    if (!MENU_PATH_CHAR.test(ch) && !(ch === "@" && prev === "/")) {
      return false;
    }
    prev = ch;
  }
  return !prefixClosedAfterFileExtensionForAtMenu(prefix);
}

export function draftExtendsFailedAtPrefix(
  draft: AtMenuDraft,
  failed: { atIdx: number; prefix: string },
): boolean {
  if (!draft.open || draft.atIdx !== failed.atIdx) {
    return false;
  }
  if (failed.prefix === "") {
    return true;
  }
  return (
    draft.prefix === failed.prefix || draft.prefix.startsWith(failed.prefix)
  );
}

/**
 * The **`@`** picker draft at the caret: the nearest **`@`** on the caret's line
 * that may open a mention (line start, whitespace, or one of **`( [ { " '`**
 * before it, outside an inline code span), followed up to the caret by a
 * filter still being typed - a path, an open quote (**`@"my no`**) or a meta
 * scheme (**`@session:`**, **`@rule:dep`**).
 */
export function atMenuDraftAtCaret(text: string, caret: number): AtMenuDraft {
  if (caret < 0 || caret > text.length) {
    return { open: false };
  }
  if (inMarkdownFenceBeforeCaret(text, caret)) {
    return { open: false };
  }
  const lineStart = caret === 0 ? 0 : text.lastIndexOf("\n", caret - 1) + 1;
  const lineEndIdx = text.indexOf("\n", caret);
  const lineEnd = lineEndIdx < 0 ? text.length : lineEndIdx;
  const line = text.slice(lineStart, lineEnd);
  // A blockquote line is prose, and a fence line holds no reference.
  if (blockquoteLine(line) || /^[ \t]*```/.test(line)) {
    return { open: false };
  }
  const beforeCaret = line.slice(0, caret - lineStart);

  for (let i = beforeCaret.length - 1; i >= 0; i--) {
    if (beforeCaret[i] !== "@") {
      continue;
    }
    if (i > 0) {
      const prev = beforeCaret[i - 1]!;
      if (!isGoSpace(prev.charCodeAt(0)) && !MENTION_OPENERS.includes(prev)) {
        continue;
      }
    }
    let ticks = 0;
    for (let c = 0; c < i; c++) {
      if (beforeCaret.charCodeAt(c) === 0x60) {
        ticks++;
      }
    }
    if (ticks % 2 !== 0) {
      // Inside an inline code span: "`@Override`" is code.
      continue;
    }
    const prefix = beforeCaret.slice(i + 1);
    if (!atMenuPrefixOpen(prefix)) {
      continue;
    }
    return { open: true, lineStart, atIdx: lineStart + i, caret, prefix };
  }
  return { open: false };
}

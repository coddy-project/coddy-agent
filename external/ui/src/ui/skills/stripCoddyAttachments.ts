import { parseMentions, type MentionToken } from "./draftAt";

/**
 * Decodes the entities of an attribute value: the five XML names plus numeric
 * references, which is what Go's **`encoding/xml.EscapeText`** writes
 * (**`&#34;`** for a quote, **`&#xA;`** for a newline). One pass, so
 * **`&amp;lt;`** stays **`&lt;`**.
 */
function decodeXmlAttrValue(s: string): string {
  return s.replace(
    /&(?:#([0-9]{1,7})|#[xX]([0-9a-fA-F]{1,6})|(lt|gt|amp|quot|apos));/g,
    (
      whole,
      dec: string | undefined,
      hex: string | undefined,
      name: string | undefined,
    ) => {
      if (name !== undefined) {
        switch (name) {
          case "lt":
            return "<";
          case "gt":
            return ">";
          case "amp":
            return "&";
          case "quot":
            return '"';
          case "apos":
            return "'";
        }
        return whole;
      }
      const cp = dec !== undefined ? Number(dec) : parseInt(hex ?? "", 16);
      if (
        !Number.isFinite(cp) ||
        cp === 0 ||
        cp > 0x10ffff ||
        (cp >= 0xd800 && cp <= 0xdfff)
      ) {
        return "\uFFFD";
      }
      return String.fromCodePoint(cp);
    },
  );
}

const ATTACHMENT_OPEN_TAG = "<coddy_attachment";
const ATTACHMENT_CLOSE_TAG = "</coddy_attachment>";
const CDATA_OPEN = "<![CDATA[";
const CDATA_CLOSE = "]]>";

const ATTR_PATH_RE = /\bpath="([^"]*)"/;
const ATTR_LINES_RE = /\blines="([0-9]{1,9})-([0-9]{1,9})"/;
const ATTR_MENTION_RE = /\bmention="([^"]*)"/;
const ATTR_KIND_RE = /\bkind="([^"]*)"/;

type LineRange = { start: number; end: number };

/** One **`<coddy_attachment>`** element of a message (**`mention.Block`**). */
type AttachmentBlock = {
  start: number;
  end: number;
  path: string;
  /** The **`mention`** attribute: the mention as the user wrote it, when it differs from the path. */
  typed: string;
  /** The **`kind`** attribute, **`"file"`** when the element has none. */
  kind: string;
  lines: LineRange | null;
};

/**
 * Index just past the closing tag of the body that starts at **`from`**, or -1
 * when the block is unterminated. CDATA sections are walked before the closing
 * tag is looked for, so a file that itself contains **`</coddy_attachment>`**
 * cannot end its block early.
 */
function attachmentBodyEnd(s: string, from: number): number {
  let i = from;
  for (;;) {
    let j = i;
    while (
      j < s.length &&
      (s[j] === " " || s[j] === "\t" || s[j] === "\n" || s[j] === "\r")
    ) {
      j++;
    }
    if (s.startsWith(CDATA_OPEN, j)) {
      const k = s.indexOf(CDATA_CLOSE, j + CDATA_OPEN.length);
      if (k < 0) {
        return -1;
      }
      i = k + CDATA_CLOSE.length;
      continue;
    }
    if (s.startsWith(ATTACHMENT_CLOSE_TAG, j)) {
      return j + ATTACHMENT_CLOSE_TAG.length;
    }
    const k = s.indexOf(ATTACHMENT_CLOSE_TAG, i);
    if (k < 0) {
      return -1;
    }
    return k + ATTACHMENT_CLOSE_TAG.length;
  }
}

/**
 * The attachment elements of **`s`** in order, mirroring **`mention.Blocks`**. An
 * opening tag without a well-formed block is ordinary text.
 */
function attachmentBlocks(s: string): AttachmentBlock[] {
  const out: AttachmentBlock[] = [];
  let from = 0;
  while (from < s.length) {
    const start = s.indexOf(ATTACHMENT_OPEN_TAG, from);
    if (start < 0) {
      break;
    }
    const attrsStart = start + ATTACHMENT_OPEN_TAG.length;
    const tagEnd = s.indexOf(">", attrsStart);
    const first = s[attrsStart];
    if (
      tagEnd < 0 ||
      (first !== ">" && first !== " " && first !== "\t" && first !== "\n")
    ) {
      from = attrsStart;
      continue;
    }
    const end = attachmentBodyEnd(s, tagEnd + 1);
    if (end < 0) {
      from = attrsStart;
      continue;
    }
    const attrs = s.slice(attrsStart, tagEnd);
    const kind = ATTR_KIND_RE.exec(attrs)?.[1];
    const lm = ATTR_LINES_RE.exec(attrs);
    let lines: LineRange | null = null;
    if (lm) {
      const lo = Number(lm[1]);
      const hi = Number(lm[2]);
      // Same bounds as the mention grammar: a malformed label (zero,
      // inverted) never becomes a ranged mention.
      if (lo >= 1 && hi >= lo) {
        lines = { start: lo, end: hi };
      }
    }
    out.push({
      start,
      end,
      path: decodeXmlAttrValue(ATTR_PATH_RE.exec(attrs)?.[1] ?? ""),
      typed: decodeXmlAttrValue(ATTR_MENTION_RE.exec(attrs)?.[1] ?? ""),
      kind: kind ? decodeXmlAttrValue(kind) : "file",
      lines,
    });
    from = end;
  }
  return out;
}

/** A path as mentions compare it: backslashes as slashes, no trailing slash. */
function comparablePath(p: string): string {
  const slashed = p.replace(/\\/g, "/");
  return slashed.endsWith("/") ? slashed.slice(0, -1) : slashed;
}

function sameLines(a: LineRange | undefined, b: LineRange | null): boolean {
  if (!a || !b) {
    return !a && !b;
  }
  return a.start === b.start && a.end === b.end;
}

/**
 * Whether the typed text (its **`parseMentions`** tokens) already holds a
 * mention of **`label`** with the same range, in any of the spellings the
 * grammar reads. A plain mention does not cover a ranged attachment of the
 * same path, and vice versa.
 */
function mentionedBefore(
  tokens: MentionToken[],
  label: string,
  lines: LineRange | null,
): boolean {
  const want = comparablePath(label);
  for (const tok of tokens) {
    if (tok.kind === "scheme") {
      if (`${tok.scheme}:${tok.ref}` === label) {
        return true;
      }
      continue;
    }
    if (tok.kind === "url") {
      // A page attachment is labelled with the address it was read from.
      if (tok.url === label) {
        return true;
      }
      continue;
    }
    for (const r of tok.readings) {
      if (comparablePath(r.path) === want && sameLines(r.lines, lines)) {
        return true;
      }
    }
  }
  return false;
}

/**
 * Parses **<coddy_session_assets>** block from user message content and returns
 * reconstructed file chip metadata for display.  Used after reload when the
 * original File objects are no longer available.
 */
export function parseSessionAssetFiles(
  content: string,
): { name: string; mimeType: string }[] {
  const m = /<coddy_session_assets>([\s\S]*?)<\/coddy_session_assets>/i.exec(content);
  if (!m) return [];
  const files: { name: string; mimeType: string }[] = [];
  for (const line of (m[1] ?? "").split("\n")) {
    const t = line.trim();
    if (!t.startsWith("- /")) continue;
    const body = t.slice(2); // remove "- "
    const parenIdx = body.indexOf(" (");
    let name: string;
    if (parenIdx >= 0 && body.endsWith(")")) {
      name = body.slice(parenIdx + 2, -1);
    } else {
      name = body.split("/").pop() || "file";
    }
    files.push({ name, mimeType: "application/octet-stream" });
  }
  return files;
}

/**
 * Extracts the raw **<coddy_session_assets>** XML block from user message content,
 * or returns an empty string if none is present.  Used in the edit flow so the
 * block can be re-appended to the edited message before sending.
 */
export function extractSessionAssetsXml(content: string): string {
  const m = /<coddy_session_assets>[\s\S]*?<\/coddy_session_assets>/i.exec(content);
  return m ? m[0] : "";
}

/**
 * What the transcript shows for a persisted user message, the twin of
 * **`mention.ForDisplay`**: every **<coddy_attachment>** element collapsed to the
 * mention that brought it (**`@src/app.go:21-31`**, the **`mention`** attribute
 * when the user wrote it differently from the path), or dropped when the text
 * before the first element already carries that mention. A rule a mentioned
 * path pulled in and the body of an invoked **`/skill`** are dropped too:
 * nothing the user typed is missing. Also strips **<coddy_session_assets>**
 * blocks and the legacy bracket annotation entirely.
 */
export function stripCoddyAttachmentsForUserDisplay(raw: string): string {
  // Strip <coddy_session_assets> blocks — backend-injected, not for display.
  let s = raw.replace(/\n*<coddy_session_assets>[\s\S]*?<\/coddy_session_assets>/gi, "");
  // Strip legacy bracket annotation from older sessions.
  s = s.replace(
    /\n\n\[Uploaded files saved to session assets \(read-only\):\n[\s\S]*?You can read these files directly or copy them to the workspace as needed\.\]/g,
    "",
  );

  const blocks = attachmentBlocks(s);
  const firstBlock = blocks[0];
  if (!firstBlock) {
    return s;
  }
  const typed = parseMentions(s.slice(0, firstBlock.start));
  let rebuilt = "";
  let last = 0;
  for (const blk of blocks) {
    rebuilt += s.slice(last, blk.start);
    last = blk.end;
    if ((blk.typed === "" && blk.kind === "rule") || blk.kind === "skill") {
      continue;
    }
    const label = blk.typed !== "" ? blk.typed : blk.path;
    if (label === "" || mentionedBefore(typed, label, blk.lines)) {
      continue;
    }
    rebuilt += blk.lines
      ? `@${label}:${blk.lines.start}-${blk.lines.end}`
      : `@${label}`;
  }
  rebuilt += s.slice(last);
  // Dropped elements leave their separators behind; one blank line is enough.
  return rebuilt.replace(/\n{3,}/g, "\n\n");
}

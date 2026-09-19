/**
 * The pages of the documentation a text points at with **`@coddy:<page>#<section>`**,
 * read with the one grammar of mentions (**`parseMentions`**), so a sent
 * message, an answer and the composer agree on what is a mention.
 */

import { parseMentions } from "../skills/draftAt";

export type DocMentionPart =
  | { type: "text"; value: string }
  | { type: "doc"; literal: string; ref: string };

/** Splits a text into prose and **`@coddy:`** mentions, in order. */
export function splitDocMentions(text: string): DocMentionPart[] {
  if (!text.includes("@coddy:")) {
    return [{ type: "text", value: text }];
  }
  const parts: DocMentionPart[] = [];
  let at = 0;
  for (const tok of parseMentions(text)) {
    if (tok.kind !== "scheme" || tok.scheme !== "coddy" || !tok.ref) {
      continue;
    }
    if (tok.start > at) {
      parts.push({ type: "text", value: text.slice(at, tok.start) });
    }
    parts.push({ type: "doc", literal: text.slice(tok.start, tok.end), ref: tok.ref });
    at = tok.end;
  }
  if (at < text.length) {
    parts.push({ type: "text", value: text.slice(at) });
  }
  return parts;
}

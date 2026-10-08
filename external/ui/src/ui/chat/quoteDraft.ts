/**
 * Quotes in a prompt (issue #342). A passage picked in the transcript goes into
 * the draft as Markdown quote lines - the form the documentation reader's
 * "Ask about the selection" uses and the model reads as a quote - after what
 * is already written, so quotes pile up in the order they were taken. The text
 * stays the only record of the draft: what keeps, queues or restores a draft
 * keeps its quotes too, and removing one is editing the text.
 */

/** The passage as Markdown quote lines; blank lines are a bare marker. */
export function quoteMarkdown(text: string): string {
  const body = text
    .replace(/\r\n?/g, "\n")
    .trim()
    .replace(/\n\s*\n(\s*\n)+/g, "\n\n");
  if (!body) return "";
  return body
    .split("\n")
    .map((line) => (line.trim() ? `> ${line}` : ">"))
    .join("\n");
}

/**
 * The draft with a quote after what is already written, a blank line on both
 * sides of it and an empty line under it to write on.
 */
export function appendQuoteToDraft(draft: string, quote: string): string {
  if (!quote) return draft;
  const before = draft.replace(/\s+$/, "");
  return before ? `${before}\n\n${quote}\n\n` : `${quote}\n\n`;
}

/** A run of a prompt's lines: a quote (its markers dropped) or text. */
export type QuoteBlock = { quote: boolean; text: string };

/** A line the prompt quotes: a marker at its start, then a space or nothing. */
const QUOTE_LINE = /^> ?|^>$/;

/**
 * A sent prompt cut into quotes and text for the bubble. Only a marker that
 * stands alone or is followed by a space opens a quote, so `>=5` stays text.
 * The blank lines that only separate a quote from the text around it are
 * dropped: the quote is a block of its own on screen.
 */
export function splitQuoteBlocks(text: string): QuoteBlock[] {
  const blocks: QuoteBlock[] = [];
  for (const line of text.split("\n")) {
    const quote = line === ">" || line.startsWith("> ");
    const body = quote ? line.replace(QUOTE_LINE, "") : line;
    const last = blocks[blocks.length - 1];
    if (last && last.quote === quote) {
      last.text += `\n${body}`;
    } else {
      blocks.push({ quote, text: body });
    }
  }
  if (blocks.length === 1) return blocks;
  return blocks
    .map((b) =>
      b.quote ? b : { ...b, text: b.text.replace(/^\n+|\n+$/g, "") },
    )
    .filter((b) => b.quote || b.text.trim() !== "");
}

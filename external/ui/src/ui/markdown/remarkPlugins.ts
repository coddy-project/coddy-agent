import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import { remarkLiteralDollars } from "./mathDelimiters";
import { remarkDocMentions } from "./remarkDocMentions";
import { remarkSoftBreaks } from "./remarkSoftBreaks";

/**
 * The remark passes every Markdown body goes through, in order: what is a
 * formula, a table or a documentation mention is decided here, before React
 * renders anything (Markdown.tsx), so a test can read the same tree.
 */
export const REMARK_PLUGINS = [
  remarkGfm,
  remarkMath,
  remarkLiteralDollars,
  remarkDocMentions,
];

/**
 * The same passes for a body read the way Obsidian reads it, a single newline
 * being a line break (Markdown's breaks, used by the canvas cards). The split
 * comes last, after every pass that makes or splits text.
 */
export const REMARK_PLUGINS_WITH_BREAKS = [...REMARK_PLUGINS, remarkSoftBreaks];

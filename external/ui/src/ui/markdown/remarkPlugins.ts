import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import { remarkLiteralDollars } from "./mathDelimiters";
import { remarkDocMentions } from "./remarkDocMentions";

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

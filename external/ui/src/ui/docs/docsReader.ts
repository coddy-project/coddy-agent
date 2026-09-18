/**
 * Pure pieces of the documentation reader, kept out of the component so they
 * are tested on their own.
 */

import { appNavHrefDocs } from "../scheduler/hashRoute";
import type { DocsFragment, DocsHeading } from "./api";

/**
 * The composer draft "Ask the agent" opens a chat with: the selected text
 * quoted, then a mention of the page (or the section the selection sits in),
 * so the agent gets the page attached and knows which part the question is
 * about.
 */
export function askDraftFor(
  slug: string,
  anchor: string | null,
  selection: string,
): string {
  const ref = anchor ? `${slug}#${anchor}` : slug;
  const text = selection.trim();
  const quote = text
    ? `${text
        .split(/\r?\n/)
        .map((line) => (line.trim() ? `> ${line}` : ">"))
        .join("\n")}\n\n`
    : "";
  return `${quote}@coddy:${ref} `;
}

/** The headings an "On this page" list shows: the sections, two levels deep. */
export function outlineHeadings(headings: DocsHeading[]): DocsHeading[] {
  return headings.filter((h) => h.level === 2 || h.level === 3);
}

/**
 * Gives the headings of a rendered page the anchors the server computed, in
 * document order. Both skip what a fenced block holds, so the n-th heading
 * element is the n-th heading of the page.
 */
export function assignHeadingIds(root: HTMLElement, headings: DocsHeading[]): void {
  const els = root.querySelectorAll("h1, h2, h3, h4, h5, h6");
  els.forEach((el, i) => {
    const h = headings[i];
    if (h) {
      el.id = h.anchor;
    }
  });
}

/**
 * Adds a "#" link to every section heading, GitHub style: the reader's own
 * address of that section, to follow, copy or open in a new tab.
 */
export function addHeadingLinks(
  root: HTMLElement,
  slug: string,
  label: (heading: string) => string,
): void {
  root.querySelectorAll(".docs-heading-anchor").forEach((a) => a.remove());
  root.querySelectorAll("h2[id], h3[id], h4[id]").forEach((el) => {
    const a = document.createElement("a");
    a.className = "docs-heading-anchor";
    a.href = appNavHrefDocs(slug, el.id);
    a.setAttribute("aria-label", label(el.textContent || el.id));
    a.textContent = "#";
    el.appendChild(a);
  });
}

/**
 * The anchor of the section a node sits in: the last heading with an id
 * before it in the page. Null above the first section.
 */
export function sectionAnchorAt(root: HTMLElement, node: Node): string | null {
  let found: string | null = null;
  const els = root.querySelectorAll("h2[id], h3[id], h4[id]");
  for (const el of Array.from(els)) {
    if (el === node || el.contains(node)) {
      return el.id;
    }
    // FOLLOWING: node comes after this heading.
    if (el.compareDocumentPosition(node) & Node.DOCUMENT_POSITION_FOLLOWING) {
      found = el.id;
    } else {
      break;
    }
  }
  return found;
}

/** The snippet of a search hit as plain text, for a title attribute. */
export function snippetText(snippet: DocsFragment[]): string {
  return snippet.map((f) => f.text).join("");
}

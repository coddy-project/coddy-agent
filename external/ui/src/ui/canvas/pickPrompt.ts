import type { PickedElement } from "./bridgeProtocol";

/** One element the person pointed at, with what they said about it. */
export interface CanvasPick {
  id: string;
  frameId: string;
  /** The page the element is on, relative to the workspace. */
  page: string;
  /** "desktop 1280x800", "390x844". */
  size: string;
  element: PickedElement;
  note: string;
}

/** A short name of the element for the chip: tag, id or first class. */
export function pickLabel(p: Pick<CanvasPick, "element">): string {
  const e = p.element;
  if (e.id) return `${e.tag}#${e.id}`;
  if (e.classes[0]) return `${e.tag}.${e.classes[0]}`;
  return e.tag || "element";
}

function fenced(html: string): string {
  // A fence longer than any run of backticks inside keeps the snippet whole.
  let fence = "```";
  while (html.includes(fence)) fence += "`";
  return `${fence}html\n${html}\n${fence}`;
}

/**
 * The prompt the picks become: plain Markdown the person sees in the
 * transcript and the model reads as it is - the page and its size, each
 * element's selector and a snippet of its HTML, what the person said about
 * it, then what they typed in the bar. A path and a snippet, not the file:
 * the agent reads the file itself when it needs more.
 */
export function composeCanvasPrompt(picks: CanvasPick[], text: string): string {
  const body = text.trim();
  if (picks.length === 0) return body;
  const parts: string[] = [];
  const pages = new Map<string, CanvasPick[]>();
  for (const p of picks) {
    const key = `${p.page}\u0000${p.size}`;
    pages.set(key, [...(pages.get(key) ?? []), p]);
  }
  const numbered = picks.length > 1;
  let n = 0;
  for (const group of pages.values()) {
    const first = group[0]!;
    parts.push(`On the canvas, \`${first.page}\` (${first.size}):`);
    for (const p of group) {
      n++;
      const head = `${numbered ? `${n}. ` : ""}Element \`${p.element.selector || p.element.tag}\`:`;
      const lines = [head];
      if (p.element.html) lines.push(fenced(p.element.html));
      if (p.note.trim()) lines.push(p.note.trim());
      parts.push(lines.join("\n\n"));
    }
  }
  if (body) parts.push(body);
  return parts.join("\n\n");
}

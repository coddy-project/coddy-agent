import type { ExtensionAPI } from "./runtime";
import type { Owned } from "./contributions";

/** One transcript row an extension may draw: a host item type the renderer
 * matched, or `ext_frame` for a live frame the host has no row for. */
export type ExtensionMessageItem = Readonly<{
  sessionId: string;
  itemId: string;
  /** Host item type (`compaction`, `system_notice`, ...) or `ext_frame`. */
  type: string;
  /** SSE event name of an `ext_frame` row; "" otherwise. */
  event: string;
  /** The item as JSON text, or the raw frame payload. */
  data: string;
}>;
export type MessageView = {
  update?: (item: ExtensionMessageItem) => void;
  dispose?: () => void;
};
export type MessageRenderer = {
  id: string;
  /** Item types to draw: exact type names, `ext_frame:<event>`, or `*`. */
  match: string | string[];
  mount: (
    element: HTMLElement,
    item: ExtensionMessageItem,
    api: ExtensionAPI,
  ) => void | (() => void) | MessageView;
};

export function validateMessageRenderer(renderer: MessageRenderer) {
  const matches = Array.isArray(renderer.match)
    ? renderer.match
    : [renderer.match];
  if (
    typeof renderer.mount !== "function" ||
    !matches.length ||
    matches.some((m) => typeof m !== "string" || !m)
  )
    throw new Error("Invalid message renderer");
}

/** Exact `ext_frame:<event>` beats `ext_frame`, which beats an exact item
 * type, which beats `*`; ties go to the more specific entry, then owner id,
 * then registration order. `*` covers `ext_frame` rows only: a host row is
 * drawn by the host unless a renderer names its type. */
export function selectMessageRenderer(
  rows: Owned<MessageRenderer>[],
  type: string,
  event: string,
) {
  const score = (row: MessageRenderer) =>
    Math.max(
      ...([].concat(row.match as never) as string[]).map((m) => {
        if (m === `ext_frame:${event}` && type === "ext_frame") return 4;
        if (m === "ext_frame" && type === "ext_frame") return 3;
        if (m === type && type !== "ext_frame") return 3;
        if (m === "*") return type === "ext_frame" ? 1 : -1;
        return -1;
      }),
    );
  return rows
    .filter((row) => score(row) >= 0)
    .sort(
      (a, b) =>
        score(b) - score(a) ||
        (a.owner < b.owner ? -1 : a.owner > b.owner ? 1 : 0),
    )[0];
}

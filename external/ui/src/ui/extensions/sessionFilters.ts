import type { ExtensionAPI } from "./runtime";
import type { ToolView } from "./contributions";

/** The row a session filter sees: the listing's own metadata, frozen. */
export type SessionFilterInfo = Readonly<{
  id: string;
  title: string;
  cwd: string;
  repoRoot: string;
  archived: boolean;
  pinned: boolean;
  origin: string;
  tags: readonly string[];
  updatedAt: string;
}>;
export type SessionFilter = {
  id: string;
  /** Chip label in the sessions filter bar. */
  title: string;
  /** Return false to hide a row. Every registered filter must pass. */
  filter?: (row: SessionFilterInfo) => boolean;
  /** Optional chip control beside the host's own filters. */
  mount?: (
    element: HTMLElement,
    api: ExtensionAPI,
  ) => void | (() => void) | ToolView;
};
export type SessionFilterHandle = {
  update: (patch: Partial<Omit<SessionFilter, "id">>) => void;
  remove: () => void;
};

export function validateSessionFilter(filter: SessionFilter) {
  if (
    !filter.title?.trim() ||
    (filter.filter !== undefined && typeof filter.filter !== "function") ||
    (filter.mount !== undefined && typeof filter.mount !== "function")
  )
    throw new Error("A session filter needs a title and a filter or mount");
}

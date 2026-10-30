import type { ExtensionAPI } from "./runtime";
import type { ExtensionToolCall, Owned, ToolView } from "./contributions";

/**
 * A decoration of the host's permission card: the view sits under the host's
 * own preview of the pending call and cannot answer the prompt - the options
 * and the choice stay the host's.
 */
export type PermissionPreview = {
  id: string;
  /** Tool name (`server__tool`, `mcp__server__tool` or a built-in name),
   * `"*"`, or omitted for every prompt. */
  tool?: string;
  mount: (
    element: HTMLElement,
    call: ExtensionToolCall,
    api: ExtensionAPI,
  ) => void | (() => void) | ToolView;
};

export function validatePermissionPreview(preview: PermissionPreview) {
  if (
    typeof preview.mount !== "function" ||
    (preview.tool !== undefined && !preview.tool)
  )
    throw new Error("Invalid permission preview");
}

export function selectPermissionPreview(
  rows: Owned<PermissionPreview>[],
  names: readonly string[],
) {
  // A named tool beats `*`, which beats a preview with no tool of its own.
  // A tool may be named in any spelling of the call: the raw name, its
  // `server__tool` form or just the tool part.
  const score = (row: PermissionPreview) =>
    row.tool && row.tool !== "*" ? 2 : row.tool === "*" ? 1 : 0;
  return rows
    .filter((row) => !row.tool || row.tool === "*" || names.includes(row.tool))
    .sort(
      (a, b) =>
        score(b) - score(a) ||
        (a.owner < b.owner ? -1 : a.owner > b.owner ? 1 : 0),
    )[0];
}

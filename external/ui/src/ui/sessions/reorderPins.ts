/**
 * Moving one pin to another place in the list.
 *
 * The whole resulting order is what travels to the server, not "this one moved":
 * a list rewritten from what the operator was looking at cannot interleave with
 * a concurrent change into an order nobody asked for.
 */
export function reorderPins(
  ids: readonly string[],
  from: number,
  to: number,
): string[] {
  const out = [...ids];
  if (
    from === to ||
    from < 0 ||
    to < 0 ||
    from >= out.length ||
    to >= out.length
  ) {
    return out;
  }
  const [moved] = out.splice(from, 1);
  if (moved === undefined) {
    return [...ids];
  }
  out.splice(to, 0, moved);
  return out;
}

/**
 * Which slot a pointer at `y` is over, given the rows' rectangles. A pointer
 * past the middle of a row belongs to the slot after it, so a drag reads the
 * way the drop will look.
 */
export function pinDropIndex(
  rects: readonly { top: number; height: number }[],
  y: number,
): number {
  for (let i = 0; i < rects.length; i++) {
    const rect = rects[i];
    if (!rect) {
      continue;
    }
    if (y < rect.top + rect.height / 2) {
      return i;
    }
  }
  return Math.max(0, rects.length - 1);
}

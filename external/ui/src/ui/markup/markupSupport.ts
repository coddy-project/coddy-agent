/**
 * Whether this browser can draw the markup editor at all (jsdom cannot). It
 * lives apart from the editor so the composer can ask without loading it:
 * the editor is a chunk of its own, fetched when a picture first opens.
 */
export function markupSupported(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof (window as { CanvasRenderingContext2D?: unknown })
      .CanvasRenderingContext2D === "function"
  );
}

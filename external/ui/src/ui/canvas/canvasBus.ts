/**
 * Asks App to open the canvas window of the chat on screen, on a frame when
 * one is named: the canvas_show card in the transcript and the Files window's
 * Open on canvas. Module-level pub/sub in the shape of files/fileBus.ts.
 */
const listeners = new Set<(frameId: string) => void>();

export function openCanvas(frameId = ""): void {
  for (const listener of [...listeners]) listener(frameId);
}

export function onOpenCanvas(listener: (frameId: string) => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

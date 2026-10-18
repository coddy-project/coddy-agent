/**
 * Asks App to open a .canvas file of the chat on screen in the Files window,
 * on a frame when one is named: the canvas_show card of the transcript and the
 * Files window's Open on canvas. Module-level pub/sub in the shape of
 * files/fileBus.ts.
 */
type Listener = (file: string, frameId: string) => void;

const listeners = new Set<Listener>();

export function openCanvas(file: string, frameId = ""): void {
  for (const listener of [...listeners]) listener(file, frameId);
}

export function onOpenCanvas(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

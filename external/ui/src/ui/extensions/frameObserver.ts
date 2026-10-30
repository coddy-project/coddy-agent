/**
 * Observation of the live turn stream at frame granularity. The incoming
 * transforms rewrite payloads; this passes every frame - including the ones
 * the host does not know - to listeners unchanged, so an extension can react
 * to a frame kind that shipped after this SDK. A frame the host has no row
 * for becomes one transcript item of type `ext_frame`, which a registered
 * message renderer draws or the host drops unread.
 */
export type FrameContext = Readonly<{
  sessionId: string;
  transport: "post" | "relay";
  /** SSE event name, or "" for a default-data frame. */
  event: string;
  id: string;
  ageMs?: number;
}>;
export type FrameListener = (
  frame: Readonly<FrameContext & { data: string }>,
) => void;
export type FrameHandle = { remove: () => void };

type Observer = {
  owner: string;
  listener: FrameListener;
  signal: AbortSignal;
  remove: () => void;
};

export class FrameObserver {
  private observers = new Map<string, Observer>();
  constructor(private onError: (owner: string, message: string) => void) {}

  register(
    owner: string,
    id: string,
    signal: AbortSignal,
    listener: FrameListener,
  ): FrameHandle {
    if (signal.aborted) throw new Error("Extension disposed");
    if (!/^[a-z][a-z0-9-]{0,63}$/.test(id) || typeof listener !== "function")
      throw new Error("A frame listener needs an id and listener function");
    const key = `${owner}/${id}`;
    if (this.observers.has(key)) throw new Error(`Duplicate listener: ${key}`);
    const remove = () => {
      if (this.observers.get(key) !== observer) return;
      this.observers.delete(key);
      signal.removeEventListener("abort", remove);
    };
    const observer: Observer = { owner, listener, signal, remove };
    this.observers.set(key, observer);
    signal.addEventListener("abort", remove, { once: true });
    return { remove };
  }

  /** Delivers one frame to every listener in registration order; a listener
   * that throws is removed and reported, the next one still sees the frame. */
  observe = (frame: FrameContext & { data: string }) => {
    if (!this.observers.size) return;
    const frozen = Object.freeze({ ...frame });
    for (const [key, observer] of [...this.observers.entries()]) {
      if (observer.signal.aborted || this.observers.get(key) !== observer)
        continue;
      try {
        observer.listener(frozen);
      } catch (error) {
        observer.remove();
        this.onError(observer.owner, `Frame listener ${key}: ${String(error)}`);
      }
    }
  };
}

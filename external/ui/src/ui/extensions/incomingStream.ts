export type IncomingContext = Readonly<{
  sessionId: string;
  transport: "post" | "relay";
  event: string;
  id: string;
  ageMs?: number;
}>;
export type IncomingFrame = IncomingContext & { data: string };
export type IncomingTransform = (
  data: string,
  context: IncomingContext,
) => string | void;
export type IncomingRegistration = {
  id: string;
  transform: IncomingTransform;
};
export type IncomingHandle = { remove: () => void };

type Processor = {
  owner: string;
  transform: IncomingTransform;
  signal: AbortSignal;
  remove: () => void;
};

/** Per-tab payload middleware. Transport metadata never enters the return value. */
export class IncomingStream {
  private processors = new Map<string, Processor>();
  constructor(private onError: (owner: string, message: string) => void) {}

  register(
    owner: string,
    id: string,
    signal: AbortSignal,
    transform: IncomingTransform,
  ): IncomingHandle {
    if (signal.aborted) throw new Error("Extension disposed");
    if (!/^[a-z][a-z0-9-]{0,63}$/.test(id) || typeof transform !== "function")
      throw new Error(
        "An incoming processor needs an id and transform function",
      );
    const key = `${owner}/${id}`;
    if (this.processors.has(key))
      throw new Error(`Duplicate processor: ${key}`);
    const remove = () => {
      if (this.processors.get(key) !== processor) return;
      this.processors.delete(key);
      signal.removeEventListener("abort", remove);
    };
    const processor: Processor = { owner, transform, signal, remove };
    this.processors.set(key, processor);
    signal.addEventListener("abort", remove, { once: true });
    return { remove };
  }

  process = (frame: IncomingFrame): string => {
    if (
      !this.processors.size ||
      !frame.data ||
      frame.data === "[DONE]" ||
      frame.event === "desync"
    )
      return frame.data;
    const { data: original, ...metadata } = frame;
    const context = Object.freeze(metadata);
    let data = original;
    // Module imports finish in arbitrary order. Extension ids establish the
    // chain order; Map insertion order preserves registrations within an owner.
    const processors = [...this.processors.entries()].sort(([, a], [, b]) =>
      a.owner < b.owner ? -1 : a.owner > b.owner ? 1 : 0,
    );
    for (const [key, processor] of processors) {
      if (processor.signal.aborted || this.processors.get(key) !== processor)
        continue;
      try {
        const result: unknown = processor.transform(data, context);
        if (result === undefined) continue;
        if (typeof result !== "string") {
          // Consume a rejected promise from an accidentally async callback.
          void Promise.resolve(result).catch(() => {});
          throw new Error(
            "Incoming processors must return a string or undefined synchronously",
          );
        }
        if (result === "[DONE]")
          throw new Error("Cannot synthesize stream termination");
        let json = false;
        try {
          JSON.parse(data);
          json = true;
        } catch {
          /* Text payload. */
        }
        if (json) JSON.parse(result);
        data = result;
      } catch (error) {
        processor.remove();
        this.onError(
          processor.owner,
          `Incoming processor ${key}: ${String(error)}`,
        );
      }
    }
    return data;
  };
}

import { vi } from "vitest";

/** One call a fake 2D context heard: the method and its arguments. */
export type CanvasCall = {
  op: string;
  args: unknown[];
  canvas: HTMLCanvasElement;
};

/**
 * Puts a drawing browser under jsdom, which has none: a 2D context that
 * records what is drawn, toBlob that hands back a small PNG blob, and
 * createImageBitmap that decodes any file to a picture of the given size.
 * The calls are shared by every canvas, in order.
 */
export function installMarkupFakes(size = { width: 40, height: 30 }): {
  calls: CanvasCall[];
  restore: () => void;
} {
  const calls: CanvasCall[] = [];
  const contexts = new WeakMap<HTMLCanvasElement, object>();
  vi.stubGlobal(
    "CanvasRenderingContext2D",
    function CanvasRenderingContext2D() {},
  );
  vi.stubGlobal("createImageBitmap", async () => ({ ...size, close() {} }));
  const getContext = vi
    .spyOn(HTMLCanvasElement.prototype, "getContext")
    .mockImplementation(function (this: HTMLCanvasElement) {
      const canvas = this;
      let ctx = contexts.get(canvas);
      if (!ctx) {
        const props: Record<string, unknown> = {};
        ctx = new Proxy(props, {
          get(target, key) {
            if (typeof key !== "string") return undefined;
            if (key in target) return target[key];
            return (...args: unknown[]) => {
              calls.push({ op: key, args, canvas });
            };
          },
          set(target, key, value) {
            if (typeof key === "string") target[key] = value;
            return true;
          },
        });
        contexts.set(canvas, ctx);
      }
      return ctx as never;
    });
  const toBlob = vi
    .spyOn(HTMLCanvasElement.prototype, "toBlob")
    .mockImplementation(function (this: HTMLCanvasElement, cb: BlobCallback) {
      cb(new Blob([`png ${this.width}x${this.height}`], { type: "image/png" }));
    });
  return {
    calls,
    restore: () => {
      getContext.mockRestore();
      toBlob.mockRestore();
      vi.unstubAllGlobals();
    },
  };
}

import { afterEach, describe, expect, it } from "vitest";
import { installMarkupFakes } from "./markupFakes";
import { renderPicture } from "./markupCanvas";

describe("renderPicture", () => {
  let fakes: ReturnType<typeof installMarkupFakes> | null = null;
  afterEach(() => {
    fakes?.restore();
    fakes = null;
  });

  it("replays the marks, crops and turns onto canvases of the right size", () => {
    fakes = installMarkupFakes();
    const base = {} as CanvasImageSource;
    const out = renderPicture(base, { w: 300, h: 200 }, [
      {
        kind: "shape",
        shape: {
          kind: "rect",
          color: "#ff3b30",
          width: 4,
          a: { x: 1, y: 2 },
          b: { x: 50, y: 60 },
        },
      },
      { kind: "crop", x: 10, y: 20, w: 100, h: 50 },
      { kind: "rotate" },
      {
        kind: "shape",
        shape: {
          kind: "text",
          color: "#ffffff",
          size: 20,
          at: { x: 5, y: 6 },
          text: "Привет",
        },
      },
    ]);
    expect([out.width, out.height]).toEqual([50, 100]);
    const ops = fakes.calls.map((c) => c.op);
    expect(ops).toContain("strokeRect");
    const crop = fakes.calls.find(
      (c) => c.op === "drawImage" && c.args.length === 9,
    );
    expect(crop?.args.slice(1)).toEqual([10, 20, 100, 50, 0, 0, 100, 50]);
    const turn = fakes.calls.find((c) => c.op === "rotate");
    expect(turn?.args[0]).toBeCloseTo(Math.PI / 2);
    expect(fakes.calls.find((c) => c.op === "translate")?.args).toEqual([
      50, 0,
    ]);
    const label = fakes.calls.find((c) => c.op === "fillText");
    expect(label?.args).toEqual(["Привет", 5, 6]);
    expect(label?.canvas).toBe(out);
    // The canvases a crop and a turn replaced give their pixels back.
    const first = fakes.calls.find((c) => c.op === "strokeRect")!.canvas;
    expect([first.width, first.height]).toEqual([0, 0]);
    expect(out.width).toBe(50);
  });
});

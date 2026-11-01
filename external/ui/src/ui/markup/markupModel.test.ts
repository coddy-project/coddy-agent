import { describe, expect, it } from "vitest";
import {
  arrowGeometry,
  cropFor,
  dragDrew,
  fitView,
  penPressure,
  penSegments,
  penWidthFactor,
  sizeAfter,
  strokeWidthFor,
  textSizeFor,
} from "./markupModel";

describe("markup model", () => {
  it("follows the picture's size through crops and quarter turns", () => {
    const base = { w: 300, h: 200 };
    expect(sizeAfter(base, [])).toEqual(base);
    expect(sizeAfter(base, [{ kind: "rotate" }])).toEqual({ w: 200, h: 300 });
    expect(
      sizeAfter(base, [
        { kind: "rotate" },
        { kind: "crop", x: 10, y: 20, w: 100, h: 50 },
        { kind: "rotate" },
      ]),
    ).toEqual({ w: 50, h: 100 });
  });

  it("clips a crop to the picture and refuses one that keeps nothing or everything", () => {
    const size = { w: 100, h: 100 };
    expect(cropFor(size, { x: -10, y: -10 }, { x: 50.4, y: 40 })).toEqual({
      kind: "crop",
      x: 0,
      y: 0,
      w: 50,
      h: 40,
    });
    expect(cropFor(size, { x: 60, y: 60 }, { x: 10, y: 20 })).toEqual({
      kind: "crop",
      x: 10,
      y: 20,
      w: 50,
      h: 40,
    });
    expect(cropFor(size, { x: 200, y: 200 }, { x: 300, y: 300 })).toBeNull();
    expect(cropFor(size, { x: 10, y: 10 }, { x: 11, y: 50 })).toBeNull();
    expect(cropFor(size, { x: -5, y: -5 }, { x: 120, y: 120 })).toBeNull();
  });

  it("counts a click with a shape tool as nothing drawn", () => {
    expect(dragDrew({ x: 10, y: 10 }, { x: 11, y: 11 }, 1)).toBe(false);
    expect(dragDrew({ x: 10, y: 10 }, { x: 14, y: 10 }, 1)).toBe(true);
    // Four picture pixels shown at half size travel two screen pixels.
    expect(dragDrew({ x: 10, y: 10 }, { x: 14, y: 10 }, 0.5)).toBe(false);
  });

  it("puts the arrow's head at its tip, its shaft stopping inside the head", () => {
    const g = arrowGeometry({ x: 0, y: 0 }, { x: 100, y: 0 }, 4);
    expect(g.head[0]).toEqual({ x: 100, y: 0 });
    expect(g.head[1].x).toBeCloseTo(78);
    expect(Math.abs(g.head[1].y)).toBeCloseTo(12.1);
    expect(g.head[2].y).toBeCloseTo(-g.head[1].y);
    expect(g.shaftEnd.x).toBeGreaterThan(78);
    expect(g.shaftEnd.x).toBeLessThan(100);
  });

  it("fits a picture into the stage without enlarging it", () => {
    expect(fitView({ w: 400, h: 200 }, { w: 1000, h: 1000 })).toEqual({
      x: 300,
      y: 400,
      w: 400,
      h: 200,
      k: 1,
    });
    const v = fitView({ w: 4000, h: 1000 }, { w: 1000, h: 1000 });
    expect(v.k).toBe(0.25);
    expect([v.w, v.h, v.x, v.y]).toEqual([1000, 250, 0, 375]);
  });

  it("sizes lines and labels like the console's editor", () => {
    // external/cli/markup/document.go: max(w,h)/240 in [3,14], max(w,h)/36 in [16,96].
    expect(strokeWidthFor({ w: 1200, h: 700 })).toBe(5);
    expect(strokeWidthFor({ w: 100, h: 50 })).toBe(3);
    expect(strokeWidthFor({ w: 8000, h: 50 })).toBe(14);
    expect(textSizeFor({ w: 1800, h: 700 })).toBe(50);
    expect(textSizeFor({ w: 100, h: 50 })).toBe(16);
  });

  it("reads a pressure from a pen only", () => {
    expect(penPressure({ pointerType: "pen", pressure: 0.8 })).toBe(0.8);
    expect(penPressure({ pointerType: "pen", pressure: 0 })).toBeUndefined();
    expect(
      penPressure({ pointerType: "mouse", pressure: 0.5 }),
    ).toBeUndefined();
    expect(penPressure({ pointerType: "touch", pressure: 1 })).toBeUndefined();
    expect(penPressure({ pressure: 0.5 })).toBeUndefined();
    expect(penWidthFactor(0.5)).toBeCloseTo(1);
    expect(penWidthFactor(0)).toBeLessThan(penWidthFactor(1));
  });

  it("cuts a stroke into smooth pieces that meet end to end", () => {
    const points = [
      { x: 0, y: 0 },
      { x: 10, y: 0 },
      { x: 20, y: 10 },
      { x: 30, y: 10 },
    ];
    const plain = penSegments({ width: 4, points });
    expect(plain).toHaveLength(4);
    expect(plain[0]!.from).toEqual(points[0]);
    expect(plain[plain.length - 1]!.to).toEqual(points[3]);
    for (let i = 1; i < plain.length; i++) {
      expect(plain[i]!.from).toEqual(plain[i - 1]!.to);
    }
    expect(plain.slice(1, -1).map((g) => g.ctrl)).toEqual([
      points[1],
      points[2],
    ]);
    expect(plain.every((g) => g.width === 4)).toBe(true);

    const pressed = penSegments({
      width: 4,
      points,
      pressures: [0.1, 0.5, 0.9, 0.9],
    });
    expect(pressed.map((g) => g.width)).toEqual([
      4 * penWidthFactor(0.1),
      4 * penWidthFactor(0.5),
      4 * penWidthFactor(0.9),
      4 * penWidthFactor(0.9),
    ]);
    expect(penSegments({ width: 4, points: points.slice(0, 2) })).toEqual([
      { from: points[0], ctrl: null, to: points[1], width: 4 },
    ]);
    expect(penSegments({ width: 4, points: points.slice(0, 1) })).toEqual([]);
  });
});

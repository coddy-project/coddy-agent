import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "vitest";
import {
  type BackdropColors,
  paintBackdrop,
  paintBackdropLayers,
  parseCssColor,
} from "./canvasBackdrop";

const css = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
  "utf8",
);

describe("parseCssColor", () => {
  test("reads hex and rgb()/rgba() spellings", () => {
    expect(parseCssColor("#0f0f10")).toEqual([15, 15, 16, 1]);
    expect(parseCssColor(" #FFF ")).toEqual([255, 255, 255, 1]);
    expect(parseCssColor("rgba(147, 51, 234, 0.12)")).toEqual([
      147, 51, 234, 0.12,
    ]);
    expect(parseCssColor("rgb(1 2 3 / 50%)")).toEqual([1, 2, 3, 0.5]);
  });

  test("refuses what it cannot read", () => {
    expect(parseCssColor("")).toBeNull();
    expect(parseCssColor("color-mix(in srgb, red 50%, blue)")).toBeNull();
    expect(parseCssColor("rgba(1, 2)")).toBeNull();
  });
});

test("every theme spells the four backdrop tokens in a form the painter reads", () => {
  const tokens = [
    "--coddy-canvas-gradient-top",
    "--coddy-canvas-gradient-bottom",
    "--coddy-canvas-glow-purple",
    "--coddy-canvas-glow-indigo",
  ];
  for (const token of tokens) {
    const values = [...css.matchAll(new RegExp(`${token}:\\s*([^;]+);`, "g"))];
    // the default block plus one per theme
    expect(values.length).toBeGreaterThanOrEqual(7);
    for (const [, value = ""] of values) {
      expect(parseCssColor(value), `${token}: ${value}`).not.toBeNull();
    }
  }
});

test("the painter's glow geometry matches the html background rule", () => {
  const rule = css.match(/\nhtml\s*\{[^}]*\}/s)?.[0].replace(/\s+/g, " ");
  expect(rule).toContain(
    "radial-gradient( 1200px 600px at 30% 10%, var(--coddy-canvas-glow-purple), transparent 55% )",
  );
  expect(rule).toContain(
    "radial-gradient( 900px 500px at 80% 70%, var(--coddy-canvas-glow-indigo), transparent 50% )",
  );
});

describe("paintBackdrop", () => {
  const colors: BackdropColors = {
    top: [20, 20, 20, 1],
    bottom: [10, 10, 10, 1],
    purple: [200, 0, 200, 0.5],
    indigo: [0, 0, 200, 0.5],
  };
  // A random() that always returns 0.5 makes the dither exactly zero.
  const still = () => 0.5;

  function paint(width: number, height: number, scale = 1) {
    const data = new Uint8ClampedArray(width * height * 4);
    paintBackdrop(data, width, height, scale, colors, still);
    return (x: number, y: number) => {
      const i = (y * width + x) * 4;
      return [data[i]!, data[i + 1]!, data[i + 2]!, data[i + 3]!];
    };
  }

  test("draws the vertical gradient where no glow reaches", () => {
    // 4000 px wide: the far left of the bottom row is outside both glows.
    const px = paint(4000, 101);
    expect(px(0, 100)).toEqual([10, 10, 10, 255]);
    expect(px(3999, 0)).toEqual([20, 20, 20, 255]);
  });

  test("puts each glow's full alpha at its centre and none at its end stop", () => {
    const width = 2000;
    const height = 1000;
    const px = paint(width, height);
    // purple centre at 30% 10%: half purple over the gradient
    const c = px(600, 100);
    expect(c[0]).toBeGreaterThan(100);
    expect(c[1]).toBeLessThan(15);
    // just past the purple end stop (0.55 * 1200 px) on the centre row
    const past = px(600 + 661, 100);
    expect(past[0]).toBe(past[1]);
    // indigo centre at 80% 70%
    const i = px(1600, 700);
    expect(i[2]).toBeGreaterThan(100);
    expect(i[0]).toBeLessThan(15);
  });

  test("scales the glow radii with the device pixel ratio", () => {
    const px1 = paint(2000, 1000, 1);
    const px2 = paint(4000, 2000, 2);
    // 500 CSS px right of the purple centre: inside the glow at both scales,
    // at the same strength
    const a = px1(600 + 500, 100);
    const b = px2(1200 + 1000, 200);
    expect(a[0]).toBeGreaterThan(20);
    for (let k = 0; k < 3; k++) {
      expect(Math.abs(a[k]! - b[k]!)).toBeLessThanOrEqual(1);
    }
  });

  test("a region is the same part of the backdrop as the whole viewport paints", () => {
    const W = 1600;
    const H = 900;
    const whole = new Uint8ClampedArray(W * H * 4);
    paintBackdrop(whole, W, H, 1, colors, still);
    const box = { x: 300, y: 40, width: 500, height: 120 };
    const part = new Uint8ClampedArray(box.width * box.height * 4);
    paintBackdropLayers([{ data: part }], box, W, H, 1, colors, still);
    for (const [x, y] of [
      [0, 0],
      [250, 60],
      [499, 119],
    ] as const) {
      const p = (y * box.width + x) * 4;
      const q = ((box.y + y) * W + box.x + x) * 4;
      expect([...part.slice(p, p + 4)]).toEqual([...whole.slice(q, q + 4)]);
    }
  });

  test("the default generator dithers by about one level and keeps the tone", () => {
    const flat: BackdropColors = {
      top: [20.3, 40.7, 60.5, 1],
      bottom: [20.3, 40.7, 60.5, 1],
      purple: [0, 0, 0, 0],
      indigo: [0, 0, 0, 0],
    };
    const W = 400;
    const H = 100;
    const data = new Uint8ClampedArray(W * H * 4);
    paintBackdrop(data, W, H, 1, flat);
    const sums = [0, 0, 0];
    let worst = 0;
    let opaque = true;
    for (let p = 0; p < data.length; p += 4) {
      for (let k = 0; k < 3; k++) {
        worst = Math.max(worst, Math.abs(data[p + k]! - flat.top[k]!));
        sums[k]! += data[p + k]!;
      }
      opaque &&= data[p + 3] === 255;
    }
    expect(worst).toBeLessThanOrEqual(1.5);
    expect(opaque).toBe(true);
    for (let k = 0; k < 3; k++) {
      expect(sums[k]! / (W * H)).toBeCloseTo(flat.top[k]!, 1);
    }
  });

  test("adds a dither of at most about one level", () => {
    const flat: BackdropColors = {
      ...colors,
      bottom: colors.top,
      purple: [0, 0, 0, 0],
      indigo: [0, 0, 0, 0],
    };
    const data = new Uint8ClampedArray(64 * 1 * 4);
    let k = 0;
    const seq = [0, 1, 1, 0];
    paintBackdrop(data, 64, 1, 1, flat, () => seq[k++ % 4]!);
    const values = new Set<number>();
    for (let x = 0; x < 64; x++) values.add(data[x * 4]!);
    // base 20 with noise -1..+1 (rounded by the clamped array)
    for (const v of values) expect(Math.abs(v - 20)).toBeLessThanOrEqual(2);
    expect(values.size).toBeGreaterThan(1);
  });
});

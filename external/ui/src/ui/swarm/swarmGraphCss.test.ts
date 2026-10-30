import { describe, expect, it } from "vitest";
import { stylesPartial, stylesSource } from "../../stylesSource";

// The SVG fills the viewport by absolute placement, never by height:100%:
// a percentage on a replaced element resolves against the parent's specified
// height, and .swarm-graph-viewport is sized by flex-grow - the SVG would
// collapse to its intrinsic 150px and the map would render as an empty box.
const css = stylesSource();

const rule = (selector: string): string => {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return (
    new RegExp(`(^|\\n)${escaped}\\s*\\{[^}]*\\}`, "s").exec(css)?.[0] ?? ""
  );
};

describe("swarm graph canvas sizing", () => {
  it("layout buttons keep the zoom button height at every viewport", () => {
    const zoomHeight = /\bheight:\s*([^;]+);/.exec(rule(".zoom-control"))?.[1];
    expect(zoomHeight).toBe("28px");
    const heights = [
      ...stylesPartial("screens/swarm").matchAll(
        /(?:^|\n)\s*\.swarm-canvas-control\s*\{([^}]+)\}/g,
      ),
    ]
      .map((match) => /\bheight:\s*([^;]+);/.exec(match[1]!)?.[1])
      .filter((height) => height !== undefined);
    expect(heights.length).toBeGreaterThan(0);
    for (const height of heights) expect(height).toBe(zoomHeight);
  });

  it("the svg is absolutely placed inside the viewport", () => {
    const body = rule(".swarm-graph");
    expect(body).toContain("position: absolute");
    expect(body).toContain("inset: 0");
  });

  it("the viewport grows with the panel and clips what overflows", () => {
    const body = rule(".swarm-graph-viewport");
    expect(body).toContain("position: relative");
    expect(body).toContain("flex: 1");
    expect(body).toContain("overflow: hidden");
  });

  it("the panel grows to the dock's height", () => {
    const body = rule(".swarm-graph-panel");
    expect(body).toContain("flex: 1");
    expect(body).toContain("min-height: 0");
  });
});

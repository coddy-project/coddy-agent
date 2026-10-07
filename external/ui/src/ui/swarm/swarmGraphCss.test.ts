import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// The SVG fills the viewport by absolute placement, never by height:100%:
// a percentage on a replaced element resolves against the parent's specified
// height, and .swarm-graph-viewport is sized by flex-grow - the SVG would
// collapse to its intrinsic 150px and the map would render as an empty box.
const dir = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(join(dir, "../../styles.css"), "utf8");

const rule = (selector: string): string => {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return (
    new RegExp(`(^|\\n)${escaped}\\s*\\{[^}]*\\}`, "s").exec(css)?.[0] ?? ""
  );
};

describe("swarm graph canvas sizing", () => {
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

import { expect, test } from "vitest";
import { stylesPartial } from "../../stylesSource";

test("the animated hero accent follows the selected theme", () => {
  const css = stylesPartial("chat/screen");
  const animation = css.match(
    /@keyframes hero-accent-cycle\s*\{([\s\S]*?)\n\}/,
  )?.[1];
  expect(animation).toBeDefined();
  const colors = [...animation!.matchAll(/\bcolor:\s*([^;]+);/g)].map(
    (match) => match[1],
  );
  expect(colors).toHaveLength(4);
  for (const color of colors) {
    expect(color).toContain("var(--coddy-hero-accent)");
  }
  expect(animation).not.toMatch(/rgba?\(/);
  expect(animation).toContain("var(--coddy-hero-accent-glow)");
});

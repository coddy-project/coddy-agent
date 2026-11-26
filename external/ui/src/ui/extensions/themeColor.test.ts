import { expect, test } from "vitest";
import { formatThemeColor, parseThemeColor } from "./themeColor";

test("the host parses every color form used by themes and extension styles", () => {
  expect(parseThemeColor("#38bdf8")).toMatchObject({ h: 198, s: 93, l: 60 });
  expect(parseThemeColor("rgb(56, 189, 248)")).toMatchObject({
    h: 198,
    s: 93,
    l: 60,
  });
  expect(parseThemeColor("hsl(120 50% 40%)")).toEqual({ h: 120, s: 50, l: 40 });
  expect(parseThemeColor("color(srgb 0.2 0.4 0.6)")).toMatchObject({
    h: 210,
    s: 50,
    l: 40,
  });
  expect(parseThemeColor("oklch(0.7 0.1 30)")).not.toBeNull();
});

test("formatting returns a CSS color accepted by theme overrides", () => {
  expect(formatThemeColor({ h: 120, s: 50, l: 40 })).toBe("hsl(120, 50%, 40%)");
  expect(formatThemeColor({ h: -120, s: 150, l: -10 })).toBe(
    "hsl(240, 100%, 0%)",
  );
});

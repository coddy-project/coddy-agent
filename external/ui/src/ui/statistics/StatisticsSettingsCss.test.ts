import { expect, test } from "vitest";
import { stylesPartial } from "../../stylesSource";

test("custom tariff inputs have room for all three rates", () => {
  const css = stylesPartial("screens/statistics").replace(/\s+/g, " ");

  expect(css).toMatch(
    /\.statistics-settings \.settings-array-entry\s*\{[^}]*grid-template-columns:\s*repeat\(3,\s*minmax\(0,\s*1fr\)\)\s*40px;/,
  );
  expect(css).toMatch(/\.statistics-rate-remove\s*\{[^}]*grid-column:\s*4;/);
});

test("custom tariff currency has a full row on phones", () => {
  const css = stylesPartial("screens/statistics").replace(/\s+/g, " ");

  expect(css).toMatch(
    /\.statistics-rate-currency\s*\{[^}]*grid-column:\s*1\s*\/\s*3;/,
  );
  expect(css).toMatch(
    /@media\s*\(max-width:\s*599px\)\s*\{[\s\S]*?\.statistics-rate-currency\s*\{[^}]*grid-column:\s*1\s*\/\s*-1;/,
  );
});

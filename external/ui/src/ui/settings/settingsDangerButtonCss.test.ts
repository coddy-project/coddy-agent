/**
 * Contract: a destructive settings button reads as one on every canvas. The
 * dark themes draw it in their dark pink, which is nearly invisible on the
 * light one; that canvas takes the danger red of its theme, the same pair the
 * confirmation dialog's button draws (`confirm-dialog-btn--danger`).
 */
import { expect, test } from "vitest";
import { stylesSource } from "../../stylesSource";

const css = stylesSource();

function rule(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return (
    new RegExp(`(^|\\n)${escaped}\\s*\\{[^}]*\\}`, "s").exec(css)?.[0] ?? ""
  );
}

test("the light canvas darkens a destructive settings button", () => {
  const light = rule('[data-theme="light"] .settings-btn-danger');
  expect(light).not.toBe("");
  expect(light).toMatch(/color:\s*#b91c1c/);
  expect(light).toMatch(/border-color:\s*rgba\(220,\s*38,\s*38/);
});

test("the light hover darkens with it", () => {
  const hover = rule(
    '[data-theme="light"] .settings-btn-danger:hover:not(:disabled)',
  );
  expect(hover).not.toBe("");
  expect(hover).toMatch(/border-color:\s*rgba\(220,\s*38,\s*38/);
  expect(hover).toMatch(/box-shadow:\s*0 0 0 1px rgba\(220,\s*38,\s*38/);
});

test("the dark themes keep their palette", () => {
  expect(rule(".settings-btn-danger")).toMatch(/color:\s*#fecaca/);
  expect(rule(".settings-btn-danger:hover:not(:disabled)")).toMatch(
    /border-color:\s*rgba\(248,\s*113,\s*113/,
  );
});

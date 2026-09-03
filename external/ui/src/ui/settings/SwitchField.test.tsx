import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SwitchField } from "./SwitchField";

afterEach(cleanup);

const cssPath = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../styles.css",
);

function cssText(): string {
  return readFileSync(cssPath, "utf8");
}

// The label names the switch: a settings form never needs to duplicate it in
// an aria-label, and the accessible name stays in sync with what is drawn.
test("switch is named by its visible label and toggles through onChange", () => {
  let last: boolean | null = null;
  render(
    <SwitchField
      checked={false}
      onChange={(next) => {
        last = next;
      }}
      label="Stream responses"
    />,
  );
  const sw = screen.getByRole("switch", { name: "Stream responses" });
  expect(sw.getAttribute("aria-checked")).toBe("false");
  fireEvent.click(sw);
  expect(last).toBe(true);
});

// The visible text is a real <label for>, so clicking it flips the switch the
// way an iOS-style row is expected to behave.
test("clicking the label toggles the switch", () => {
  let last: boolean | null = null;
  const { container } = render(
    <SwitchField
      checked={false}
      onChange={(next) => {
        last = next;
      }}
      label="Multimodal"
    />,
  );
  const sw = screen.getByRole("switch", { name: "Multimodal" });
  const label = container.querySelector<HTMLLabelElement>(
    ".settings-switch-field-label",
  );
  expect(label?.tagName).toBe("LABEL");
  expect(label?.htmlFor).toBe(sw.id);
  fireEvent.click(label!);
  expect(last).toBe(true);
});

test("an explicit aria label wins over the visible label", () => {
  render(
    <SwitchField
      checked={true}
      onChange={() => {}}
      label="Enabled"
      ariaLabel="Skill auto-discovery"
      dataTestId="sf-toggle"
    />,
  );
  const sw = screen.getByRole("switch", { name: "Skill auto-discovery" });
  expect(sw).toBe(screen.getByTestId("sf-toggle"));
});

// Layout contract (DESIGN.md, "Boolean switch fields"): switch, label, and
// description are direct children of one grid container. The description is
// not a sibling paragraph indented by a hard-coded padding - it lives in the
// label column, so its left edge follows the label whatever the switch width.
test("label and description share one grid container with the switch", () => {
  const { container } = render(
    <SwitchField
      checked={false}
      onChange={() => {}}
      label="Multimodal"
      description="When true, the model accepts image or file inputs."
    />,
  );
  const field = container.querySelector(".settings-switch-field");
  expect(field).not.toBeNull();
  const sw = screen.getByRole("switch", { name: "Multimodal" });
  const label = field!.querySelector(".settings-switch-field-label");
  const desc = field!.querySelector(".settings-switch-field-desc");
  expect(label?.textContent).toBe("Multimodal");
  expect(desc?.textContent).toBe(
    "When true, the model accepts image or file inputs.",
  );
  expect(sw.parentElement).toBe(field);
  expect(label?.parentElement).toBe(field);
  expect(desc?.parentElement).toBe(field);
  // Description keeps the shared muted typography of every field description.
  expect(desc?.classList.contains("settings-field-desc")).toBe(true);
  // No legacy checkbox-era indent anywhere in the field.
  expect(
    container.querySelector(".settings-field-desc-below-checkbox"),
  ).toBeNull();
});

test("no description renders no empty paragraph", () => {
  const { container } = render(
    <SwitchField checked={false} onChange={() => {}} label="Enabled" />,
  );
  expect(container.querySelector(".settings-switch-field-desc")).toBeNull();
});

// CSS contract. jsdom does no layout, so the geometry that makes the row read
// right is pinned as source rules: a grid whose first column is the control's
// own width, an 8px gap, both first-row cells vertically centred, and the
// description pushed into the label column with the shared paragraph margin
// cancelled by a child selector (specificity, not source order). The
// hard-coded 28px indent that put descriptions under the switch is gone.
// Anchored at line start so an indented @media override can never be pinned
// in place of the base rule.
test("switch field grid centres the label on the switch and indents the description by column", () => {
  const css = cssText();
  const field = css.match(/^\.settings-switch-field\s*\{([^}]*)\}/m);
  expect(field).not.toBeNull();
  expect(field![1]).toMatch(/display:\s*grid/);
  expect(field![1]).toMatch(
    /grid-template-columns:\s*auto minmax\(0,\s*1fr\)\s*;/,
  );
  expect(field![1]).toMatch(/column-gap:\s*8px/);
  expect(field![1]).toMatch(/row-gap:\s*4px/);
  expect(field![1]).toMatch(/align-items:\s*center/);
  const desc = css.match(
    /^\.settings-switch-field\s*>\s*\.settings-switch-field-desc\s*\{([^}]*)\}/m,
  );
  expect(desc).not.toBeNull();
  expect(desc![1]).toMatch(/grid-column:\s*2/);
  expect(desc![1]).toMatch(/margin:\s*0\s*;/);
  expect(css).not.toMatch(/\.settings-field-desc-below-checkbox/);
  // The inline-flow helper that caused the bug when used alone has no
  // consumers left and is gone with them.
  expect(css).not.toMatch(/\.settings-row-inline/);
});

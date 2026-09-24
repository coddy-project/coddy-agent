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

// Layout contract (DESIGN.md, "Boolean switch fields"): the switch and one
// label cell are the direct children of the grid. The description is not a
// paragraph under the label any more: like every settings field's, it is the
// (i) beside the name, and its text shows in the tip while that is hovered.
test("the label cell carries the label and the (i) with the description", () => {
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
  const cell = field!.querySelector(".settings-switch-field-label-cell");
  expect(sw.parentElement).toBe(field);
  expect(cell?.parentElement).toBe(field);
  expect(cell?.querySelector(".settings-switch-field-label")?.textContent).toBe(
    "Multimodal",
  );
  const hint = cell!.querySelector<HTMLButtonElement>(".field-hint");
  expect(hint?.getAttribute("aria-label")).toBe("About Multimodal");
  fireEvent.mouseEnter(hint!);
  expect(screen.getByRole("tooltip").textContent).toBe(
    "When true, the model accepts image or file inputs.",
  );
  // No paragraph under the label, no checkbox-era indent.
  expect(field!.querySelector(".settings-field-desc")).toBeNull();
  expect(
    container.querySelector(".settings-field-desc-below-checkbox"),
  ).toBeNull();
});

test("no description renders no (i)", () => {
  const { container } = render(
    <SwitchField checked={false} onChange={() => {}} label="Enabled" />,
  );
  expect(container.querySelector(".field-hint")).toBeNull();
});

// The (i) sits in the label cell but outside the <label>: opening the tip
// must never flip the switch.
test("clicking the (i) does not toggle the switch", () => {
  let last: boolean | null = null;
  const { container } = render(
    <SwitchField
      checked={false}
      onChange={(next) => {
        last = next;
      }}
      label="Multimodal"
      description="Accepts images."
    />,
  );
  fireEvent.click(container.querySelector(".field-hint")!);
  expect(last).toBeNull();
  expect(screen.getByRole("tooltip").textContent).toBe("Accepts images.");
});

// CSS contract. jsdom does no layout, so the geometry that makes the row read
// right is pinned as source rules: a grid whose first column is the control's
// own width, an 8px gap, both cells vertically centred, and the label cell a
// flex row centring the label and its (i). The description paragraph and the
// hard-coded 28px indent that once put it under the switch are both gone.
// Anchored at line start so an indented @media override can never be pinned
// in place of the base rule.
test("switch field grid centres the label cell on the switch", () => {
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
  const cell = css.match(/^\.settings-switch-field-label-cell\s*\{([^}]*)\}/m);
  expect(cell).not.toBeNull();
  expect(cell![1]).toMatch(/display:\s*flex/);
  expect(cell![1]).toMatch(/align-items:\s*center/);
  expect(cell![1]).toMatch(/min-width:\s*0/);
  expect(css).not.toMatch(/\.settings-switch-field-desc/);
  expect(css).not.toMatch(/\.settings-field-desc-below-checkbox/);
  // The inline-flow helper that caused the bug when used alone has no
  // consumers left and is gone with them.
  expect(css).not.toMatch(/\.settings-row-inline/);
});

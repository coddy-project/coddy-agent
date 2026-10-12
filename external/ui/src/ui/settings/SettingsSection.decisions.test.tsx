import React from "react";
import { afterEach, expect, test } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { SettingsSection } from "./SettingsSection";

afterEach(cleanup);

function Harness(props: { initial?: Record<string, unknown> }) {
  const [doc, setDoc] = React.useState<Record<string, unknown>>(
    props.initial ?? {
      decisions: { enable: false, model: "frida-decisions", threshold: 0.5 },
    },
  );
  return (
    <>
      <SettingsSection
        section={{
          id: "decisions",
          label: "Command safety (decisions)",
          kind: "object",
          schemaKey: "decisions",
        }}
        schema={{
          type: "object",
          properties: {
            decisions: {
              type: "object",
              properties: {
                enable: { type: "boolean", default: false },
                model: {
                  type: "string",
                  enum: ["frida-decisions", "clef-flash"],
                  default: "frida-decisions",
                },
                threshold: {
                  type: "number",
                  minimum: 0,
                  maximum: 1,
                  default: 0.5,
                },
              },
            },
          },
        }}
        doc={doc}
        setDoc={setDoc}
      />
      <output data-testid="doc">{JSON.stringify(doc)}</output>
    </>
  );
}

test("decisions renders the enable switch and offers both decisions models", () => {
  render(<Harness />);
  expect(screen.getByRole("switch", { name: /enabled/i })).toHaveAttribute(
    "aria-checked",
    "false",
  );
  fireEvent.focus(screen.getByRole("combobox"));
  expect(screen.getByText("frida-decisions")).toBeTruthy();
  expect(screen.getByText("clef-flash")).toBeTruthy();
});

test("toggling the switch flips decisions.enable and keeps the model", () => {
  render(<Harness />);
  fireEvent.click(screen.getByRole("switch", { name: /enabled/i }));
  const doc = JSON.parse(screen.getByTestId("doc").textContent!);
  expect(doc.decisions).toEqual({
    enable: true,
    model: "frida-decisions",
    threshold: 0.5,
  });
});

test("picking another decisions model writes it to the document", () => {
  render(<Harness />);
  fireEvent.focus(screen.getByRole("combobox"));
  fireEvent.mouseDown(screen.getByText("clef-flash"));
  const doc = JSON.parse(screen.getByTestId("doc").textContent!);
  expect(doc.decisions).toEqual({
    enable: false,
    model: "clef-flash",
    threshold: 0.5,
  });
});

test("raising the unsafe threshold writes it to the document", () => {
  render(<Harness />);
  const input = screen.getByRole("spinbutton", { name: /unsafe threshold/i });
  expect(input).toHaveAttribute("min", "0");
  expect(input).toHaveAttribute("max", "1");
  fireEvent.change(input, { target: { value: "0.9", valueAsNumber: 0.9 } });
  const doc = JSON.parse(screen.getByTestId("doc").textContent!);
  expect(doc.decisions.threshold).toBe(0.9);
});

// DESIGN.md "Section form layout": the section's switch opens the tab on its
// own, the fields sit in a fieldset by meaning, its description behind the
// (i) beside the legend.
test("the switch stands alone above one fieldset of the model and the threshold", () => {
  const { container } = render(<Harness />);
  const root = container.querySelector(".settings-schema-root")!;
  const [first, second] = [...root.children] as HTMLElement[];
  if (!first || !second) throw new Error("the form has fewer than two blocks");
  expect(first.tagName).not.toBe("FIELDSET");
  expect(first.querySelector("[role='switch']")).not.toBeNull();
  expect(second.tagName).toBe("FIELDSET");
  const legend = second.querySelector(":scope > legend")!;
  expect(legend.textContent).toContain("Model and threshold");
  expect(legend.querySelector("button")).not.toBeNull();
  expect(within(second).getByRole("combobox")).toBeTruthy();
  expect(
    within(second).getByRole("spinbutton", { name: /unsafe threshold/i }),
  ).toBeTruthy();
  expect(within(second).queryByRole("switch")).toBeNull();
});

test("a configuration without the section shows the check switched off", () => {
  render(<Harness initial={{}} />);
  expect(screen.getByRole("switch", { name: /enabled/i })).toHaveAttribute(
    "aria-checked",
    "false",
  );
});

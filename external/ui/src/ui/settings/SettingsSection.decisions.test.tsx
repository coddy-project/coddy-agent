import React from "react";
import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SettingsSection } from "./SettingsSection";

afterEach(cleanup);

function Harness() {
  const [doc, setDoc] = React.useState<Record<string, unknown>>({
    decisions: { enable: false, model: "frida-decisions", threshold: 0.5 },
  });
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
                threshold: { type: "number", minimum: 0, maximum: 1, default: 0.5 },
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

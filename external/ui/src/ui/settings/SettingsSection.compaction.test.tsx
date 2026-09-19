import React from "react";
import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SettingsSection } from "./SettingsSection";

afterEach(cleanup);

function Harness() {
  const [doc, setDoc] = React.useState<Record<string, unknown>>({
    models: [{ model: "openai/gpt-4o" }, { model: "anthropic/claude" }],
    compaction: { model: "", threshold: 0.8 },
  });
  return (
    <>
      <SettingsSection
        section={{
          id: "compaction",
          label: "Context compaction",
          kind: "object",
          schemaKey: "compaction",
        }}
        schema={{
          type: "object",
          properties: {
            compaction: {
              type: "object",
              properties: {
                model: { type: "string" },
                threshold: { type: "number" },
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

test("compaction offers configured models and preserves other settings when selecting", () => {
  render(<Harness />);
  fireEvent.focus(screen.getByRole("combobox"));
  fireEvent.mouseDown(screen.getByText("anthropic/claude"));
  expect(JSON.parse(screen.getByTestId("doc").textContent!).compaction).toEqual(
    { model: "anthropic/claude", threshold: 0.8 },
  );
});

test("compaction allows a custom model and clearing the override", () => {
  render(<Harness />);
  const input = screen.getByRole("combobox");
  for (const model of ["custom/model", ""]) {
    fireEvent.change(input, { target: { value: model } });
    expect(
      JSON.parse(screen.getByTestId("doc").textContent!).compaction.model,
    ).toBe(model);
  }
});

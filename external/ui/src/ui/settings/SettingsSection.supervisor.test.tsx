import React from "react";
import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SettingsSection } from "./SettingsSection";

afterEach(cleanup);

function Harness() {
  const [doc, setDoc] = React.useState<Record<string, unknown>>({
    models: [{ model: "openai/gpt-4o" }, { model: "anthropic/claude" }],
    supervisor: { model: "", max_continuations: 10, stall_seconds: 300 },
  });
  return (
    <>
      <SettingsSection
        section={{
          id: "supervisor",
          label: "Session goal",
          kind: "object",
          schemaKey: "supervisor",
        }}
        schema={{
          type: "object",
          properties: {
            supervisor: {
              type: "object",
              properties: {
                model: { type: "string" },
                max_continuations: { type: "integer" },
                stall_seconds: { type: "integer" },
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

test("the supervisor model is picked from the configured models", () => {
  render(<Harness />);
  fireEvent.focus(screen.getByRole("combobox"));
  fireEvent.mouseDown(screen.getByText("anthropic/claude"));
  expect(JSON.parse(screen.getByTestId("doc").textContent!).supervisor).toEqual(
    { model: "anthropic/claude", max_continuations: 10, stall_seconds: 300 },
  );
});

test("the goal checks and the watchdog are fieldsets of their own", () => {
  render(<Harness />);
  const legends = screen
    .getAllByRole("group")
    .map((g) => g.querySelector("legend")?.textContent);
  expect(legends).toEqual(expect.arrayContaining(["Goal checks", "Watchdog"]));
});

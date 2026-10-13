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
      {/* An edit of the Logical Models tab that is not saved yet. */}
      <button
        type="button"
        onClick={() =>
          setDoc((d) => ({
            ...d,
            models: [...(d.models as unknown[]), { model: "hub/qwen3-coder" }],
          }))
        }
      >
        add unsaved model
      </button>
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

test("compaction offers a model added to the settings document before it is saved", () => {
  render(<Harness />);
  fireEvent.click(screen.getByText("add unsaved model"));
  fireEvent.focus(screen.getByRole("combobox"));
  fireEvent.mouseDown(screen.getByText("hub/qwen3-coder"));
  expect(
    JSON.parse(screen.getByTestId("doc").textContent!).compaction.model,
  ).toBe("hub/qwen3-coder");
});

// The section's own switch turns compaction off altogether, the automatic one
// only the trigger: it stands with its threshold in a group of its own, never
// beside the master switch, so the form shows which one depends on which.
test("compaction groups the automatic switch with its threshold under the master switch", () => {
  const { container } = render(
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
              enable: { type: "boolean" },
              auto_enable: { type: "boolean" },
              threshold_percent: { type: "integer" },
              keep_recent_turns: { type: "integer" },
              model: { type: "string" },
            },
          },
        },
      }}
      doc={{ compaction: { enable: true, auto_enable: true } }}
      setDoc={() => {}}
    />,
  );
  const fieldsets = Array.from(container.querySelectorAll("fieldset"));
  const byLegend = (name: string) =>
    fieldsets.find((f) =>
      f.querySelector("legend")?.textContent?.includes(name),
    );
  const auto = byLegend("Automatic compaction");
  expect(auto).toBeDefined();
  expect(auto!.textContent).toContain("Enabled");
  expect(auto!.textContent).toContain("Threshold (%)");
  expect(auto!.textContent).not.toContain("Keep recent turns");
  const summary = byLegend("Summarization");
  expect(summary!.textContent).toContain("Keep recent turns");
  expect(summary!.textContent).not.toContain("Threshold (%)");
  // The master switch opens the form outside every fieldset.
  const switches = Array.from(
    container.querySelectorAll('input[type="checkbox"], [role="switch"]'),
  );
  expect(switches.filter((s) => !s.closest("fieldset"))).toHaveLength(1);
  expect(switches.filter((s) => auto!.contains(s))).toHaveLength(1);
});

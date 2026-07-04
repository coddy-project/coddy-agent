import React from "react";
import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SettingsArraySection } from "./SettingsArraySection";
import type { JsonSchema } from "./SchemaForm";

afterEach(cleanup);

const arraySchema: JsonSchema = {
  type: "array",
  title: "LLM providers",
  items: {
    type: "object",
    properties: {
      name: { type: "string", title: "Provider name" },
      type: {
        type: "string",
        title: "Provider type",
        enum: ["openai", "anthropic"],
      },
    },
    "x-coddy-property-order": ["name", "type"],
  },
} as unknown as JsonSchema;

function Harness({ initial = [] }: { initial?: unknown[] }) {
  const [val, setVal] = React.useState<unknown[]>(initial);
  return (
    <SettingsArraySection
      schema={arraySchema}
      value={val}
      onChange={setVal}
      labelField="name"
    />
  );
}

test("Add seeds a new item and opens its form", () => {
  render(<Harness />);
  expect(screen.getByText(/Nothing here yet/)).toBeTruthy();
  fireEvent.click(screen.getByTestId("settings-master-add"));
  expect(screen.getByLabelText("Provider name")).toBeTruthy();
  expect(screen.getByTestId("settings-detail-back")).toBeTruthy();
});

test("editing the label field updates the list", () => {
  render(<Harness />);
  fireEvent.click(screen.getByTestId("settings-master-add"));
  fireEvent.change(screen.getByLabelText("Provider name"), {
    target: { value: "openai" },
  });
  fireEvent.click(screen.getByTestId("settings-detail-back"));
  expect(screen.getByTestId("settings-master-item-0").textContent).toBe("openai");
});

test("unnamed items get a fallback label", () => {
  render(<Harness />);
  fireEvent.click(screen.getByTestId("settings-master-add"));
  fireEvent.click(screen.getByTestId("settings-detail-back"));
  expect(screen.getByTestId("settings-master-item-0").textContent).toContain(
    "(unnamed",
  );
});

test("Remove deletes an item from the list", () => {
  render(<Harness initial={[{ name: "p1", type: "openai" }]} />);
  expect(screen.getByTestId("settings-master-item-0")).toBeTruthy();
  fireEvent.click(screen.getByLabelText(/Remove p1/));
  expect(screen.queryByTestId("settings-master-item-0")).toBeNull();
});

import React from "react";
import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import {
  SchemaForm,
  type JsonSchema,
  type SchemaFormGroup,
} from "./SchemaForm";

afterEach(cleanup);

// The layout rules of a settings form (DESIGN.md "Section form layout"): a
// section's own switch opens the form outside every fieldset, the fields sit
// in fieldsets by meaning, each fieldset where its first field stands, and
// lists and nested objects are blocks of their own beside the groups.

const sectionSchema = {
  type: "object",
  "x-coddy-property-order": [
    "dirs",
    "trust",
    "enable",
    "max_runs",
    "extra",
    "timeout",
  ],
  properties: {
    dirs: {
      type: "array",
      title: "Definition directories",
      items: { type: "string" },
    },
    trust: { type: "string", title: "Project definitions" },
    enable: { type: "boolean", title: "Enabled" },
    max_runs: { type: "integer", title: "Max runs" },
    extra: { type: "string", title: "Added later" },
    timeout: { type: "integer", title: "Timeout" },
  },
} as unknown as JsonSchema;

function Harness(props: {
  schema?: JsonSchema;
  groups?: SchemaFormGroup[];
  initial?: Record<string, unknown>;
}) {
  const [doc, setDoc] = React.useState<Record<string, unknown>>(
    props.initial ?? { dirs: ["a", "b"] },
  );
  return (
    <SchemaForm
      schema={props.schema ?? sectionSchema}
      value={doc}
      onChange={setDoc}
      groups={props.groups}
    />
  );
}

/** The form's top-level blocks: "FS:<legend>" for a fieldset, "F:<label>" for a bare field. */
function blocks(container: HTMLElement): string[] {
  const root = container.querySelector(".settings-schema-root")!;
  return [...root.children].map((el) => {
    if (el.tagName === "FIELDSET") {
      return `FS:${el.querySelector(":scope > legend")?.textContent?.trim()}`;
    }
    const label = el.querySelector(
      ".settings-switch-field-label, .settings-label-text, .settings-label",
    );
    return `F:${label?.textContent?.trim()}`;
  });
}

test("the section switch opens the form, outside every fieldset, wherever the schema puts it", () => {
  const { container } = render(
    <Harness groups={[{ id: "main", legend: "Settings" }]} />,
  );
  expect(blocks(container)[0]).toBe("F:Enabled");
  expect(container.querySelector("fieldset [role='switch']")).toBeNull();
});

test("without groups the switch still comes first", () => {
  const { container } = render(<Harness />);
  expect(blocks(container)[0]).toBe("F:Enabled");
});

test("the catch-all takes the loose fields; a list stands beside it, in schema order", () => {
  const { container } = render(
    <Harness groups={[{ id: "main", legend: "Settings" }]} />,
  );
  expect(blocks(container)).toEqual([
    "F:Enabled",
    "FS:Definition directories",
    "FS:Settings",
  ]);
  const main = screen.getByTestId("settings-group-main");
  expect(main.textContent).toContain("Project definitions");
  expect(main.textContent).toContain("Timeout");
  expect(main.querySelector("fieldset")).toBeNull();
});

test("each group stands where its first field stands; a key no group names stays in place", () => {
  const { container } = render(
    <Harness
      groups={[
        { id: "limits", legend: "Limits", paths: ["max_runs", "timeout"] },
        { id: "trust", legend: "Trust", paths: ["trust"] },
      ]}
    />,
  );
  expect(blocks(container)).toEqual([
    "F:Enabled",
    "FS:Definition directories",
    "FS:Trust",
    "FS:Limits",
    "F:Added later",
  ]);
});

test("only the first group without paths is the catch-all", () => {
  render(
    <Harness
      groups={[
        { id: "first", legend: "First" },
        { id: "second", legend: "Second" },
      ]}
    />,
  );
  expect(screen.getByTestId("settings-group-first")).toBeTruthy();
  expect(screen.queryByTestId("settings-group-second")).toBeNull();
});

test("a group's description is the (i) beside its legend", () => {
  render(
    <Harness
      groups={[{ id: "main", legend: "Settings", description: "What it is" }]}
    />,
  );
  const hint = screen
    .getByTestId("settings-group-main")
    .querySelector("legend .field-hint");
  expect(hint).toHaveAttribute("aria-label", "About Settings");
});

test("a collapsible group starts folded and opens from its legend", () => {
  render(
    <Harness
      groups={[
        { id: "main", legend: "Settings" },
        {
          id: "more",
          legend: "Advanced",
          paths: ["timeout"],
          collapsible: true,
        },
      ]}
    />,
  );
  const toggle = screen.getByTestId("settings-group-more-toggle");
  expect(toggle).toHaveAttribute("aria-expanded", "false");
  expect(screen.getByLabelText("Timeout")).not.toBeVisible();
  fireEvent.click(toggle);
  expect(screen.getByLabelText("Timeout")).toBeVisible();
});

// A list of plain values is its inputs and Add: no "dirs[0]" label per row.
test("the entries of a list of values are bare inputs named by the list", () => {
  const { container } = render(<Harness />);
  const list = container.querySelector("fieldset")!;
  expect(list.textContent).not.toMatch(/\[\d\]/);
  expect(screen.getByLabelText("Definition directories 1")).toHaveValue("a");
  expect(screen.getByLabelText("Definition directories 2")).toHaveValue("b");
});

const nestedSchema = {
  type: "object",
  properties: {
    websearch: {
      type: "object",
      title: "Web search",
      properties: {
        engines: {
          type: "array",
          title: "Engines",
          items: { type: "string" },
        },
      },
    },
    levels: {
      type: "array",
      title: "Component levels",
      items: {
        type: "object",
        properties: {
          component: { type: "string", title: "Component" },
          level: { type: "string", title: "Level" },
        },
      },
    },
  },
} as unknown as JsonSchema;

// Inside a nested object a list keeps its frame (the Telegram admins, the web
// search engines), and an entry of a list of objects is a frame of its own
// with no "levels[0]" legend.
test("a list of objects frames each entry without an index legend", () => {
  const { container } = render(
    <Harness
      schema={nestedSchema}
      initial={{
        websearch: { engines: ["brave"] },
        levels: [{ component: "gateway", level: "debug" }],
      }}
    />,
  );
  expect(screen.getByLabelText("Engines 1")).toHaveValue("brave");
  const entry = container.querySelector(".settings-array-entry")!;
  expect(entry.tagName).toBe("FIELDSET");
  expect(entry.querySelector("legend")).toBeNull();
  expect(entry).toHaveAttribute("aria-label", "Component levels 1");
  expect(container.textContent).not.toMatch(/levels\[0\]/);
});

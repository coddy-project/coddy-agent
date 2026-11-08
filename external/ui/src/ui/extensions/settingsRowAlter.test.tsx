import React from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import {
  alterSettingsRows,
  SettingsRowAlterContext,
  type SettingsRowAlter,
  type SettingsRowDescriptor,
} from "./settingsRowAlter";
import {
  SchemaForm,
  type FieldOverride,
  type JsonSchema,
} from "../settings/SchemaForm";
import { FieldLabel } from "../settings/FieldHint";
import {
  buildSettingsSearchIndex,
  searchSettings,
} from "../settings/settingsSearch";
import type { SectionDescriptor } from "../settings/settingsSections";

afterEach(cleanup);

const schema: JsonSchema = {
  type: "object",
  "x-coddy-property-order": ["alpha", "nested", "omega"],
  properties: {
    alpha: {
      type: "string",
      title: "Original alpha",
      description: "Alpha help",
    },
    nested: {
      type: "object",
      title: "Nested box",
      "x-coddy-property-order": ["first", "second", "entries"],
      properties: {
        first: { type: "string", title: "First nested" },
        second: { type: "integer", title: "Second nested" },
        entries: {
          type: "array",
          title: "Entry list",
          items: {
            type: "object",
            "x-coddy-property-order": ["name", "enabled"],
            properties: {
              name: { type: "string", title: "Entry name" },
              enabled: { type: "boolean", title: "Entry enabled" },
            },
          },
        },
      },
    },
    omega: { type: "string", title: "Original omega" },
  },
};

const value = {
  alpha: "keep alpha",
  nested: {
    first: "keep first",
    second: 2,
    entries: [{ name: "keep entry", enabled: true }],
  },
  omega: "keep omega",
};

const section: SectionDescriptor = {
  id: "demo",
  label: "Demo",
  kind: "object",
  schemaKey: "demo",
};
const rootSchema: JsonSchema = {
  type: "object",
  properties: { demo: schema },
};

function alteredForm(
  alters: readonly SettingsRowAlter[],
  onChange: (next: Record<string, unknown>) => void = () => {},
  fieldOverride?: FieldOverride,
) {
  return (
    <SettingsRowAlterContext.Provider value={{ alters }}>
      <SchemaForm
        schema={schema}
        value={value}
        onChange={onChange}
        i18nDomain="demo"
        fieldOverride={fieldOverride}
      />
    </SettingsRowAlterContext.Provider>
  );
}

function alteredIndex(alters: readonly SettingsRowAlter[]) {
  return buildSettingsSearchIndex({
    sections: [section],
    schema: rootSchema,
    doc: { demo: value },
    rowAlters: alters,
  });
}

function titles(index: ReturnType<typeof alteredIndex>, query: string) {
  return searchSettings(index, query).hits.map((hit) => hit.entry.names[0]);
}

function alter(run: SettingsRowAlter["run"], id = "test"): SettingsRowAlter {
  return { id, run };
}

describe("Settings row alter passes", () => {
  test("passes receive frozen descriptors with section, path, label, kind, type and description", () => {
    let seen: readonly SettingsRowDescriptor[] = [];
    const pass = alter((rows, context) => {
      seen = rows;
      expect(Object.isFrozen(rows)).toBe(true);
      expect(Object.isFrozen(context)).toBe(true);
      expect(rows.every((row) => Object.isFrozen(row))).toBe(true);
      expect(context).toEqual({ section: "demo" });
      return rows;
    });

    render(alteredForm([pass]));

    expect(seen.map((row) => row.id)).toEqual([
      "alpha",
      "nested",
      "nested.first",
      "nested.second",
      "nested.entries",
      "nested.entries.name",
      "nested.entries.enabled",
      "omega",
    ]);
    expect(seen[0]).toMatchObject({
      section: "demo",
      id: "alpha",
      path: "alpha",
      label: "Original alpha",
      kind: "field",
      type: "string",
      description: "Alpha help",
    });
    expect(seen[1]).toMatchObject({ kind: "block", type: "object" });
  });

  test("a throwing pass loses only its say: earlier and later passes still apply", () => {
    const calls: string[] = [];
    const result = alterSettingsRows(
      [
        {
          section: "demo",
          id: "alpha",
          path: "alpha",
          label: "Original alpha",
          kind: "field",
          type: "string",
        },
        {
          section: "demo",
          id: "omega",
          path: "omega",
          label: "Original omega",
          kind: "field",
          type: "string",
        },
      ],
      [
        alter((rows) => {
          calls.push("first");
          return rows.map((row) =>
            row.id === "alpha" ? { ...row, label: "Earlier title" } : row,
          );
        }, "first"),
        alter(() => {
          calls.push("broken");
          throw new Error("no say");
        }, "broken"),
        alter((rows) => {
          calls.push("last");
          expect(rows[0]!.label).toBe("Earlier title");
          expect(Object.isFrozen(rows[0])).toBe(true);
          return rows.map((row) =>
            row.id === "omega" ? { ...row, label: "Later title" } : row,
          );
        }, "last"),
      ],
      "demo",
    );

    expect(calls).toEqual(["first", "broken", "last"]);
    expect(result.map((row) => row.label)).toEqual([
      "Earlier title",
      "Later title",
    ]);
  });

  test("hide removes a nested row and its search entry without deleting its value", () => {
    const onChange = vi.fn();
    const hide = alter((rows) =>
      rows.map((row) =>
        row.id === "nested.first" ? { ...row, visible: false } : row,
      ),
    );
    const { container } = render(alteredForm([hide], onChange));

    expect(screen.queryByLabelText("First nested")).toBeNull();
    expect(
      container.querySelector('[data-settings-anchor="demo:nested.first"]'),
    ).toBeNull();
    expect(screen.getByLabelText("Second nested")).toHaveValue(2);
    fireEvent.change(screen.getByLabelText("Original omega"), {
      target: { value: "edited" },
    });
    expect(onChange).toHaveBeenCalledWith({
      ...value,
      omega: "edited",
    });

    const index = alteredIndex([hide]);
    expect(index.some((entry) => entry.anchor === "demo:nested.first")).toBe(
      false,
    );
    expect(titles(index, "First nested")).toEqual([]);
    expect(screen.getByLabelText("Entry name")).toHaveValue("keep entry");
  });

  test("hiding a block hides its descendants from drawing and search", () => {
    const hide = alter((rows) => rows.filter((row) => row.id !== "nested"));
    const { container } = render(alteredForm([hide]));

    expect(container.textContent).not.toContain("First nested");
    expect(container.textContent).not.toContain("Entry name");
    const index = alteredIndex([hide]);
    expect(
      index.some(
        (entry) =>
          entry.anchor === "demo:nested" ||
          entry.anchor?.startsWith("demo:nested."),
      ),
    ).toBe(false);
    expect(index.some((entry) => entry.anchor === "demo:omega")).toBe(true);
  });

  test("retitle and reorder apply to nested objects and list item fields", () => {
    const change = alter((rows) => {
      const labels: Record<string, string> = {
        "nested.first": "Renamed first",
        "nested.second": "Renamed second",
        "nested.entries.name": "Renamed entry",
      };
      const rank = new Map(
        [
          "nested.second",
          "nested.entries",
          "nested.entries.enabled",
          "nested.entries.name",
          "nested.first",
        ].map((id, index) => [id, index]),
      );
      return rows
        .map((row) => ({ ...row, label: labels[row.id] ?? row.label }))
        .sort((a, b) => (rank.get(a.id) ?? 100) - (rank.get(b.id) ?? 100));
    });
    const { container } = render(alteredForm([change]));

    expect(screen.getByLabelText("Renamed first")).toHaveValue("keep first");
    expect(screen.queryByLabelText("First nested")).toBeNull();
    expect(screen.getByLabelText("Renamed entry")).toHaveValue("keep entry");
    const nested = container.querySelector(
      '[data-settings-anchor="demo:nested"] > .settings-fieldset > .settings-nested',
    )!;
    expect(
      [...nested.children].map((el) => el.getAttribute("data-settings-anchor")),
    ).toEqual([
      "demo:nested.second",
      "demo:nested.entries",
      "demo:nested.first",
    ]);
    const entry = container.querySelector(".settings-array-entry")!;
    expect(
      [...entry.querySelectorAll("[data-settings-anchor]")].map((el) =>
        el.getAttribute("data-settings-anchor"),
      ),
    ).toEqual(["demo:nested.entries.enabled", "demo:nested.entries.name"]);

    const index = alteredIndex([change]);
    expect(titles(index, "Renamed first")).toEqual(["Renamed first"]);
    expect(titles(index, "First nested")).not.toContain("First nested");
    expect(titles(index, "Renamed entry")).toEqual(["Renamed entry"]);
    const nestedFields = index
      .filter(
        (entry) =>
          entry.keyPath?.startsWith("demo.nested.") &&
          !entry.keyPath.includes("entries"),
      )
      .map((entry) => entry.anchor);
    expect(nestedFields).toEqual(["demo:nested.second", "demo:nested.first"]);
  });

  test("reorder keeps group membership and its legend intact", () => {
    const reorder = alter((rows) => {
      const rank = new Map([
        ["nested", 0],
        ["omega", 1],
        ["alpha", 2],
      ]);
      return [...rows].sort(
        (a, b) => (rank.get(a.id) ?? 100) - (rank.get(b.id) ?? 100),
      );
    });
    const { container } = render(
      <SettingsRowAlterContext.Provider value={{ alters: [reorder] }}>
        <SchemaForm
          schema={schema}
          value={value}
          onChange={() => {}}
          i18nDomain="demo"
          groups={[{ id: "main", legend: "Main", paths: ["alpha", "omega"] }]}
        />
      </SettingsRowAlterContext.Provider>,
    );

    const root = container.querySelector(".settings-schema-root")!;
    expect(root.children[0]).toHaveAttribute(
      "data-settings-anchor",
      "demo:nested",
    );
    const group = screen.getByTestId("settings-group-main");
    expect(group.querySelector("legend")!.textContent).toBe("Main");
    expect(
      [...group.querySelector(".settings-form-group-body")!.children].map(
        (el) => el.getAttribute("data-settings-anchor"),
      ),
    ).toEqual(["demo:omega", "demo:alpha"]);
  });

  test("retitle reaches a field override and keeps its control and save path", () => {
    const onChange = vi.fn();
    const override: FieldOverride = ({
      path,
      schema: field,
      value: current,
      onChange: set,
    }) =>
      path === "alpha" ? (
        <div className="settings-row">
          <FieldLabel label={field.title!} description={field.description} />
          <input
            aria-label={field.title!}
            value={String(current ?? "")}
            onChange={(event) => set(event.target.value)}
          />
        </div>
      ) : null;
    const retitle = alter((rows) =>
      rows.map((row) =>
        row.id === "alpha" ? { ...row, label: "Override title" } : row,
      ),
    );
    render(alteredForm([retitle], onChange, override));

    const input = screen.getByLabelText("Override title");
    expect(input).toHaveValue("keep alpha");
    expect(screen.queryByLabelText("Original alpha")).toBeNull();
    fireEvent.change(input, { target: { value: "through override" } });
    expect(onChange).toHaveBeenCalledWith({
      ...value,
      alpha: "through override",
    });

    const index = alteredIndex([retitle]);
    expect(titles(index, "Override title")).toEqual(["Override title"]);
  });
});

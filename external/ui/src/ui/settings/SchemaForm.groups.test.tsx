import React from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, test } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import {
  SchemaForm,
  type FieldOverride,
  type JsonSchema,
  type SchemaFormGroup,
} from "./SchemaForm";
import { SETTINGS_REVEAL_EVENT } from "./settingsReveal";

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
  override?: FieldOverride;
  domain?: string;
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
      fieldOverride={props.override}
      i18nDomain={props.domain}
    />
  );
}

/**
 * The form's top-level blocks: "FS:<legend>" for a fieldset, "F:<label>" for a
 * bare field. A field stands in the box that carries its anchor; the block is
 * what the box holds.
 */
function blocks(container: HTMLElement): string[] {
  const root = container.querySelector(".settings-schema-root")!;
  return [...root.children].map((child) => {
    const el = child.classList.contains("settings-field")
      ? child.firstElementChild!
      : child;
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

// The boxes a settings search takes the operator to: every field the form
// draws stands in a box of its own that carries its anchor, "<domain>:<path>".

/** Every anchor in the form, in document order. */
function anchors(container: HTMLElement): string[] {
  return [...container.querySelectorAll("[data-settings-anchor]")].map(
    (el) => el.getAttribute("data-settings-anchor")!,
  );
}

function boxOf(container: HTMLElement, anchor: string): HTMLElement {
  const boxes = container.querySelectorAll<HTMLElement>(
    `[data-settings-anchor="${anchor}"]`,
  );
  expect(boxes, `the box ${anchor}`).toHaveLength(1);
  return boxes[0]!;
}

test("every drawn field is a box of its own that carries its anchor, an override's fragment included", () => {
  // A fragment: the two nodes are one field and stand in one box.
  const override: FieldOverride = ({ path }) =>
    path === "timeout" ? (
      <>
        <label>
          First half
          <input aria-label="First half" />
        </label>
        <p>Second half</p>
      </>
    ) : null;
  const { container } = render(
    <Harness
      domain="demo"
      override={override}
      groups={[{ id: "main", legend: "Settings" }]}
    />,
  );
  expect(anchors(container)).toEqual([
    "demo:enable",
    "demo:dirs",
    "group:demo:main",
    "demo:trust",
    "demo:max_runs",
    "demo:extra",
    "demo:timeout",
  ]);
  const enable = boxOf(container, "demo:enable");
  expect(enable.tagName).toBe("DIV");
  expect(enable).toHaveClass("settings-field");
  expect(within(enable).getByRole("switch")).toBeTruthy();
  expect(
    within(boxOf(container, "demo:dirs")).getByLabelText(
      "Definition directories 1",
    ),
  ).toHaveValue("a");
  expect(
    within(boxOf(container, "demo:max_runs")).getByLabelText("Max runs"),
  ).toBeTruthy();
  const timeout = boxOf(container, "demo:timeout");
  expect(within(timeout).getByLabelText("First half")).toBeTruthy();
  expect(within(timeout).getByText("Second half")).toBeTruthy();
  // The group is the fieldset, and holds the boxes of its fields.
  const group = boxOf(container, "group:demo:main");
  expect(group.tagName).toBe("FIELDSET");
  expect(group).not.toHaveClass("settings-field");
  expect(group).toContainElement(timeout);
});

test("a field an override hides draws no box", () => {
  const override: FieldOverride = ({ path }) =>
    path === "trust" ? false : null;
  const { container } = render(
    <Harness
      domain="demo"
      override={override}
      groups={[{ id: "main", legend: "Settings" }]}
    />,
  );
  expect(anchors(container)).not.toContain("demo:trust");
  expect(container.textContent).not.toContain("Project definitions");
  expect(anchors(container)).toContain("demo:max_runs");
});

test("an entry of a list of objects is no box of its own; its fields are, one per entry", () => {
  const { container } = render(
    <Harness
      domain="demo"
      schema={nestedSchema}
      initial={{
        websearch: { engines: ["brave"] },
        levels: [
          { component: "gateway", level: "debug" },
          { component: "agent", level: "info" },
        ],
      }}
    />,
  );
  // The list is one box. Its two entry frames are not boxes, so the list's
  // anchor is not drawn a second and a third time.
  expect(anchors(container)).toEqual([
    "demo:levels",
    "demo:levels.component",
    "demo:levels.level",
    "demo:levels.component",
    "demo:levels.level",
    "demo:websearch",
    "demo:websearch.engines",
  ]);
  const list = boxOf(container, "demo:levels");
  const frames = list.querySelectorAll(".settings-array-entry");
  expect(frames).toHaveLength(2);
  for (const frame of frames) {
    expect(frame.hasAttribute("data-settings-anchor")).toBe(false);
    expect(frame.closest(".settings-field")).toBe(list);
  }
});

test("a folded group opens when something inside it is revealed", () => {
  const { container } = render(
    <Harness
      domain="demo"
      groups={[
        { id: "main", legend: "Settings" },
        {
          id: "more",
          legend: "Advanced",
          paths: ["timeout"],
          collapsible: true,
        },
        {
          id: "other",
          legend: "Other",
          paths: ["max_runs"],
          collapsible: true,
        },
      ]}
    />,
  );
  const more = boxOf(container, "group:demo:more");
  const other = boxOf(container, "group:demo:other");
  const expanded = (fold: HTMLElement) =>
    fold.querySelector("legend button")!.getAttribute("aria-expanded");
  expect([expanded(more), expanded(other)]).toEqual(["false", "false"]);

  // From a field of the main group: neither fold is above it.
  act(() => {
    within(boxOf(container, "demo:trust"))
      .getByLabelText("Project definitions")
      .dispatchEvent(new CustomEvent(SETTINGS_REVEAL_EVENT, { bubbles: true }));
  });
  expect([expanded(more), expanded(other)]).toEqual(["false", "false"]);

  // From a field inside the fold: that fold opens, and only that one.
  const box = boxOf(container, "demo:timeout");
  expect(more).toContainElement(box);
  expect(screen.getByLabelText("Timeout")).not.toBeVisible();
  act(() => {
    box.dispatchEvent(
      new CustomEvent(SETTINGS_REVEAL_EVENT, { bubbles: true }),
    );
  });
  expect([expanded(more), expanded(other)]).toEqual(["true", "false"]);
  expect(screen.getByLabelText("Timeout")).toBeVisible();
});

// A reveal asks for the fold the moment the box is in the page, which a mutation
// observer sees in the microtask after the commit - before React has run the
// passive effects of that commit. The fold must already be listening then.
// Inside act every effect has run when render returns, so the page's own order
// is set up by hand: act off, one render scheduled the way a browser's is.
test("a fold hears a reveal asked the moment it is in the page, before its passive effects ran", async () => {
  const env = globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean };
  const actEnvironment = env.IS_REACT_ACT_ENVIRONMENT;
  env.IS_REACT_ACT_ENVIRONMENT = false;
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  try {
    const asked = new Promise<void>((resolve) => {
      const observer = new MutationObserver(() => {
        const box = container.querySelector(
          '[data-settings-anchor="demo:timeout"]',
        );
        if (box) {
          observer.disconnect();
          box.dispatchEvent(
            new CustomEvent(SETTINGS_REVEAL_EVENT, { bubbles: true }),
          );
          resolve();
        }
      });
      observer.observe(container, { childList: true, subtree: true });
    });
    root.render(
      <Harness
        domain="demo"
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
    await asked;
    await waitFor(() =>
      expect(
        container
          .querySelector(
            ".settings-fieldset--collapsible .settings-form-group-body",
          )!
          .hasAttribute("hidden"),
      ).toBe(false),
    );
  } finally {
    root.unmount();
    container.remove();
    if (actEnvironment === undefined) {
      delete env.IS_REACT_ACT_ENVIRONMENT;
    } else {
      env.IS_REACT_ACT_ENVIRONMENT = actEnvironment;
    }
  }
});

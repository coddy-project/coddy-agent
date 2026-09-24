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
  expect(screen.getByTestId("settings-master-item-0").textContent).toBe(
    "openai",
  );
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

// The open row lives in the address (`#/settings/providers?id=demo`): the
// list reports what it opens, renames and closes, and follows what the
// address names.
function RoutedHarness(props: {
  initial: unknown[];
  route: string | null;
  onRoute: (item: string | null) => void;
}) {
  const [val, setVal] = React.useState<unknown[]>(props.initial);
  return (
    <SettingsArraySection
      schema={arraySchema}
      value={val}
      onChange={setVal}
      labelField="name"
      backLabel="LLM providers"
      routeItem={props.route}
      onRouteItemChange={props.onRoute}
    />
  );
}

const ROWS = [
  { name: "demo", type: "openai" },
  { name: "hub", type: "anthropic" },
];

test("opening a row puts its name in the address and going back takes it out", () => {
  const routes: (string | null)[] = [];
  render(
    <RoutedHarness
      initial={ROWS}
      route={null}
      onRoute={(r) => routes.push(r)}
    />,
  );

  fireEvent.click(screen.getByTestId("settings-master-item-1"));
  expect(routes).toEqual(["hub"]);
  fireEvent.click(screen.getByTestId("settings-detail-back"));
  expect(routes).toEqual(["hub", null]);
});

test("the address opens the row it names", () => {
  render(<RoutedHarness initial={ROWS} route="hub" onRoute={() => {}} />);

  expect(
    (screen.getByLabelText("Provider name") as HTMLInputElement).value,
  ).toBe("hub");
});

test("an address naming no row goes back to the list and clears the name", () => {
  const routes: (string | null)[] = [];
  render(
    <RoutedHarness
      initial={ROWS}
      route="ghost"
      onRoute={(r) => routes.push(r)}
    />,
  );

  expect(screen.getByTestId("settings-master-item-0")).toBeTruthy();
  expect(screen.queryByTestId("settings-detail-back")).toBeNull();
  expect(routes).toContain(null);
});

test("changing the address switches the open row, and clearing it closes the form", () => {
  const { rerender } = render(
    <RoutedHarness initial={ROWS} route="demo" onRoute={() => {}} />,
  );
  expect(
    (screen.getByLabelText("Provider name") as HTMLInputElement).value,
  ).toBe("demo");

  rerender(<RoutedHarness initial={ROWS} route="hub" onRoute={() => {}} />);
  expect(
    (screen.getByLabelText("Provider name") as HTMLInputElement).value,
  ).toBe("hub");

  rerender(<RoutedHarness initial={ROWS} route={null} onRoute={() => {}} />);
  expect(screen.getByTestId("settings-master-item-0")).toBeTruthy();
});

test("renaming the open row renames it in the address", () => {
  const routes: (string | null)[] = [];
  render(
    <RoutedHarness
      initial={ROWS}
      route={null}
      onRoute={(r) => routes.push(r)}
    />,
  );

  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  fireEvent.change(screen.getByLabelText("Provider name"), {
    target: { value: "demo2" },
  });
  expect(routes).toEqual(["demo", "demo2"]);
  // The form stays on the renamed row.
  expect(
    (screen.getByLabelText("Provider name") as HTMLInputElement).value,
  ).toBe("demo2");
});

test("a new row without a name keeps the address on the list and its form open", () => {
  const routes: (string | null)[] = [];
  render(
    <RoutedHarness
      initial={ROWS}
      route={null}
      onRoute={(r) => routes.push(r)}
    />,
  );

  fireEvent.click(screen.getByTestId("settings-master-add"));
  expect(routes).toEqual([]);
  expect(screen.getByTestId("settings-detail-back")).toBeTruthy();
});

// The way back is a quiet link naming the list, the app's chevron pointing
// left, never the bordered button with a text arrow it replaced.
test("the back control names the list it returns to, behind a left chevron", () => {
  render(<RoutedHarness initial={ROWS} route="demo" onRoute={() => {}} />);

  const back = screen.getByTestId("settings-detail-back");
  expect(back.textContent).toBe("LLM providers");
  expect(back.classList.contains("settings-back-link")).toBe(true);
  expect(
    back.querySelector(".coddy-chevron.coddy-chevron--left"),
  ).not.toBeNull();
  expect(back.classList.contains("settings-btn")).toBe(false);
});

// On the narrow shell the drawer's head leads back to the list; the form's
// own link would repeat it under a second copy of the same name.
test("the back link stays out when the drawer's head already leads back", () => {
  const [, schema] = [null, arraySchema];
  render(
    <SettingsArraySection
      schema={schema}
      value={ROWS}
      onChange={() => {}}
      labelField="name"
      backLabel="LLM providers"
      routeItem="demo"
      hideBackLink
    />,
  );

  expect(screen.getByLabelText("Provider name")).toBeTruthy();
  expect(screen.queryByTestId("settings-detail-back")).toBeNull();
});

// The address can switch rows without passing through the list. The form
// must start afresh for the new row: what a field remembers (the proxy URL
// the switch brings back, a filter, a fold) belongs to the row it was typed in.
test("switching rows through the address mounts a fresh form", () => {
  const { rerender } = render(
    <RoutedHarness initial={ROWS} route="demo" onRoute={() => {}} />,
  );
  const first = screen.getByLabelText("Provider name");

  rerender(<RoutedHarness initial={ROWS} route="hub" onRoute={() => {}} />);

  const second = screen.getByLabelText("Provider name") as HTMLInputElement;
  expect(second.value).toBe("hub");
  expect(second).not.toBe(first);
});

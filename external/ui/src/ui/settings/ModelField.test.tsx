import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach } from "vitest";
import { useState } from "react";
import { expect, test } from "vitest";
import { ModelField } from "./ModelField";
import type { ProviderRow } from "./useProviderModels";

afterEach(cleanup);

const PROVIDERS: ProviderRow[] = [
  { name: "demo", type: "openai" },
  { name: "hub", type: "neuraldeep" },
];

function Harness(props: { initial?: string; providers?: ProviderRow[] }) {
  const [v, setV] = useState(props.initial ?? "");
  return (
    <>
      <output data-testid="model-field-value">{v}</output>
      <ModelField
        value={v}
        onChange={setV}
        providers={props.providers ?? PROVIDERS}
      />
    </>
  );
}

test("provider select lists the document's provider names", () => {
  render(<Harness />);

  const select = screen.getByTestId(
    "model-field-provider",
  ) as HTMLSelectElement;
  expect([...select.options].map((o) => o.value)).toEqual(["", "demo", "hub"]);
});

test("picking a provider then typing the id composes provider/id", () => {
  render(<Harness />);

  fireEvent.change(screen.getByTestId("model-field-provider"), {
    target: { value: "demo" },
  });
  fireEvent.change(screen.getByTestId("model-field-model"), {
    target: { value: "m1" },
  });

  expect(screen.getByTestId("model-field-value").textContent).toBe("demo/m1");
});

test("an existing value splits into provider and id on the first slash", () => {
  render(<Harness initial="openai/gpt-4o" />);

  expect(
    (screen.getByTestId("model-field-provider") as HTMLSelectElement).value,
  ).toBe("openai");
  expect(
    (screen.getByTestId("model-field-model") as HTMLInputElement).value,
  ).toBe("gpt-4o");
});

test("a model id may itself contain slashes", () => {
  render(<Harness initial="a/b/c" />);

  expect(
    (screen.getByTestId("model-field-provider") as HTMLSelectElement).value,
  ).toBe("a");
  expect(
    (screen.getByTestId("model-field-model") as HTMLInputElement).value,
  ).toBe("b/c");
});

test("a provider missing from the document stays selectable", () => {
  render(<Harness initial="gone/m1" />);

  const select = screen.getByTestId(
    "model-field-provider",
  ) as HTMLSelectElement;
  expect(select.value).toBe("gone");
  expect([...select.options].map((o) => o.value)).toContain("gone");
});

test("the field carries no fetch button - the list lives on the provider", () => {
  render(<Harness />);

  expect(screen.queryByTestId("model-field-fetch")).toBeNull();
});

test("clearing the provider keeps the typed id", () => {
  render(<Harness initial="demo/m1" />);

  fireEvent.change(screen.getByTestId("model-field-provider"), {
    target: { value: "" },
  });

  expect(screen.getByTestId("model-field-value").textContent).toBe("m1");
  expect(
    (screen.getByTestId("model-field-model") as HTMLInputElement).value,
  ).toBe("m1");
});

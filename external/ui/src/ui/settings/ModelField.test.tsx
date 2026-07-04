import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ModelField } from "./ModelField";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function Harness({ providers = ["openai"] }: { providers?: string[] }) {
  const [val, setVal] = React.useState("");
  return (
    <>
      <ModelField value={val} onChange={setVal} providers={providers} />
      <span data-testid="val">{val}</span>
    </>
  );
}

test("fetch populates the model combobox and picking writes provider/id", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue({
    ok: true,
    json: async () => ({ ok: true, models: [{ id: "gpt-4o" }, { id: "gpt-4o-mini" }] }),
  } as unknown as Response);

  render(<Harness />);
  fireEvent.click(screen.getByTestId("model-field-fetch"));
  await waitFor(() =>
    expect(screen.getByTestId("model-field-fetch").textContent).toBe("Fetch models"),
  );

  // Open the model combobox and pick the fetched option.
  fireEvent.focus(screen.getByTestId("model-field-model"));
  const opt = await screen.findByText("openai/gpt-4o-mini");
  fireEvent.mouseDown(opt);
  expect(screen.getByTestId("val").textContent).toBe("openai/gpt-4o-mini");
});

test("fetch failure falls back to manual typing in the combobox", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue({
    ok: true,
    json: async () => ({ ok: false, error: "bad key", models: [] }),
  } as unknown as Response);

  render(<Harness />);
  fireEvent.click(screen.getByTestId("model-field-fetch"));
  await screen.findByText(/Couldn't fetch models/);

  fireEvent.change(screen.getByTestId("model-field-model"), {
    target: { value: "openai/custom" },
  });
  expect(screen.getByTestId("val").textContent).toBe("openai/custom");
});

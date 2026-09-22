import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ModelField } from "./ModelField";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function Harness({
  providers = [{ name: "openai", type: "openai" }],
}: {
  providers?: { name?: string; type?: string; api_base?: string }[];
}) {
  const [val, setVal] = React.useState("");
  return (
    <>
      <ModelField value={val} onChange={setVal} providers={providers} />
      <span data-testid="val">{val}</span>
    </>
  );
}

test("fetch populates the model combobox and picking writes provider/id", async () => {
  const spy = vi.spyOn(globalThis, "fetch").mockResolvedValue({
    ok: true,
    json: async () => ({ ok: true, models: [{ id: "gpt-4o" }, { id: "gpt-4o-mini" }] }),
  } as unknown as Response);

  render(<Harness />);
  fireEvent.click(screen.getByTestId("model-field-fetch"));
  await waitFor(() =>
    expect(screen.getByTestId("model-field-fetch").textContent).toBe("Fetch models"),
  );

  // The provider row travels in the request body so unsaved providers fetch too.
  expect(spy).toHaveBeenCalledWith(
    "/coddy/providers/models",
    expect.objectContaining({
      method: "POST",
      body: expect.stringContaining('"name":"openai"'),
    }),
  );

  // Open the model combobox and pick the fetched option.
  fireEvent.focus(screen.getByTestId("model-field-model"));
  const opt = await screen.findByText("openai/gpt-4o-mini");
  fireEvent.mouseDown(opt);
  expect(screen.getByTestId("val").textContent).toBe("openai/gpt-4o-mini");
});

test("fetch merges the model lists of every provider in the form", async () => {
  const spy = vi.spyOn(globalThis, "fetch").mockImplementation(async (_url, init) => {
    const body = JSON.parse(String((init as RequestInit).body));
    const id = body.name === "alpha" ? "m1" : "m2";
    return {
      ok: true,
      json: async () => ({ ok: true, models: [{ id }] }),
    } as unknown as Response;
  });

  render(
    <Harness
      providers={[
        { name: "alpha", type: "openai" },
        { name: "beta", type: "openai" },
      ]}
    />,
  );
  fireEvent.click(screen.getByTestId("model-field-fetch"));
  await waitFor(() => expect(spy).toHaveBeenCalledTimes(2));

  fireEvent.focus(screen.getByTestId("model-field-model"));
  await screen.findByText("alpha/m1");
  await screen.findByText("beta/m2");
});

test("there is no separate provider selector - one field only", () => {
  render(<Harness />);
  expect(screen.queryByTestId("model-field-provider")).toBeNull();
  expect(screen.getByTestId("model-field-model")).toBeTruthy();
  expect(screen.getByTestId("model-field-fetch")).toBeTruthy();
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

test("one failing provider does not drop the lists that succeeded", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (_url, init) => {
    const body = JSON.parse(String((init as RequestInit).body));
    if (body.name === "broken") {
      return {
        ok: true,
        json: async () => ({ ok: false, error: "upstream down", models: [] }),
      } as unknown as Response;
    }
    return {
      ok: true,
      json: async () => ({ ok: true, models: [{ id: "m1" }] }),
    } as unknown as Response;
  });

  render(
    <Harness
      providers={[
        { name: "broken", type: "openai" },
        { name: "ok", type: "openai" },
      ]}
    />,
  );
  fireEvent.click(screen.getByTestId("model-field-fetch"));
  await screen.findByText(/Couldn't fetch models/);

  fireEvent.focus(screen.getByTestId("model-field-model"));
  await screen.findByText("ok/m1");
});

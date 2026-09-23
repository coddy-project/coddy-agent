import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ProviderModelsFetch } from "./ProviderModelsFetch";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function stubModels(
  models: { id: string; name?: string; context_window?: number }[],
  ok = true,
) {
  const fetchMock = vi.fn(
    async (_input: unknown, _init?: { body?: string }) => ({
      ok: true,
      json: async () => ({ ok, models, error: ok ? undefined : "boom" }),
    }),
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

test("the fetch button is disabled while the row lacks a name or a type", () => {
  render(
    <ProviderModelsFetch
      provider={{ type: "openai" }}
      existingModels={[]}
      onAddModel={() => {}}
    />,
  );

  expect(
    (screen.getByTestId("provider-fetch-models") as HTMLButtonElement).disabled,
  ).toBe(true);
});

test("fetch posts the row as edited and lists the advertised ids", async () => {
  const fetchMock = stubModels([{ id: "m1" }, { id: "m2", name: "Model Two" }]);
  const onAddModel = vi.fn();
  render(
    <ProviderModelsFetch
      provider={{ name: "demo", type: "openai", api_key: "sk-x" }}
      existingModels={[]}
      onAddModel={onAddModel}
    />,
  );

  fireEvent.click(screen.getByTestId("provider-fetch-models"));

  await waitFor(() =>
    expect(screen.getByText("m1")).toBeTruthy(),
  );
  expect(fetchMock).toHaveBeenCalledWith(
    "/coddy/providers/models",
    expect.objectContaining({ method: "POST" }),
  );
  const body = JSON.parse(
    (fetchMock.mock.calls[0]?.[1] as { body?: string } | undefined)?.body ?? "{}",
  ) as Record<string, unknown>;
  expect(body).toMatchObject({ name: "demo", type: "openai", api_key: "sk-x" });
  expect(screen.getByText("Model Two")).toBeTruthy();
});

test("a context window the provider reports shows next to the id", async () => {
  stubModels([
    { id: "m1", context_window: 131072 },
    { id: "m2" },
  ]);
  render(
    <ProviderModelsFetch
      provider={{ name: "demo", type: "openai" }}
      existingModels={[]}
      onAddModel={() => {}}
    />,
  );

  fireEvent.click(screen.getByTestId("provider-fetch-models"));

  await waitFor(() => expect(screen.getByText("131k")).toBeTruthy());
  const ctx = document.querySelector(".provider-model-ctx");
  expect(ctx?.getAttribute("title")).toContain("131,072");
});

test("a fetchable provider fetches its list when the form opens", async () => {
  const fetchMock = stubModels([{ id: "m1" }]);
  render(
    <ProviderModelsFetch
      provider={{ name: "demo", type: "openai" }}
      existingModels={[]}
      onAddModel={() => {}}
    />,
  );

  await waitFor(() => expect(screen.getByText("m1")).toBeTruthy());
  expect(fetchMock).toHaveBeenCalledWith(
    "/coddy/providers/models",
    expect.objectContaining({ method: "POST" }),
  );
});

test("an id the document lists but the provider does not advertise is a warn row", async () => {
  stubModels([{ id: "m1" }]);
  render(
    <ProviderModelsFetch
      provider={{ name: "demo", type: "openai" }}
      existingModels={["demo/m1", "demo/old-id", "other/x"]}
      onAddModel={() => {}}
    />,
  );

  fireEvent.click(screen.getByTestId("provider-fetch-models"));

  const stale = await screen.findByTestId("provider-model-stale-old-id");
  expect(stale.classList.contains("is-stale")).toBe(true);
  // Another provider's rows are not judged by this list.
  expect(screen.queryByTestId("provider-model-stale-x")).toBeNull();
});

test("the add control appends provider/id to the logical models", async () => {
  stubModels([{ id: "m1" }]);
  const onAddModel = vi.fn();
  render(
    <ProviderModelsFetch
      provider={{ name: "demo", type: "openai" }}
      existingModels={[]}
      onAddModel={onAddModel}
    />,
  );

  fireEvent.click(screen.getByTestId("provider-fetch-models"));
  fireEvent.click(await screen.findByTestId("provider-model-add-m1"));

  expect(onAddModel).toHaveBeenCalledWith("demo/m1");
});

test("a model the document already lists shows a disabled check", async () => {
  stubModels([{ id: "m1" }]);
  render(
    <ProviderModelsFetch
      provider={{ name: "demo", type: "openai" }}
      existingModels={["demo/m1"]}
      onAddModel={() => {}}
    />,
  );

  fireEvent.click(screen.getByTestId("provider-fetch-models"));

  const btn = (await screen.findByTestId(
    "provider-model-add-m1",
  )) as HTMLButtonElement;
  expect(btn.disabled).toBe(true);
});

test("a failed fetch shows the error and no list", async () => {
  stubModels([], false);
  render(
    <ProviderModelsFetch
      provider={{ name: "demo", type: "openai" }}
      existingModels={[]}
      onAddModel={() => {}}
    />,
  );

  fireEvent.click(screen.getByTestId("provider-fetch-models"));

  await waitFor(() =>
    expect(screen.getByText(/Couldn't fetch models: boom/)).toBeTruthy(),
  );
  expect(screen.queryByTestId("provider-model-add-m1")).toBeNull();
});

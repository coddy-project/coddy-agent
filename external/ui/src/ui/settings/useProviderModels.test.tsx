import { afterEach, expect, test, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import {
  useAutoProviderModels,
  type FetchedModel,
  type ProviderRow,
} from "./useProviderModels";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const TERRA: ProviderRow = {
  name: "terra",
  type: "coddy",
  api_base: "https://terra:12345",
};

// Shows the list the hook keeps for one provider row, as the settings form
// reads it, once the provider has answered.
function Probe(props: { row: ProviderRow }) {
  const { fetched, error, models } = useAutoProviderModels(props.row);
  return fetched ? (
    <output data-testid="listed">{JSON.stringify({ error, models })}</output>
  ) : null;
}

/** The list the hook keeps for a provider that answers with these rows. */
async function listed(rows: unknown[]): Promise<FetchedModel[]> {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({ ok: true, models: rows }),
    })),
  );
  render(<Probe row={TERRA} />);
  const out = await waitFor(() => screen.getByTestId("listed"));
  const { error, models } = JSON.parse(out.textContent!) as {
    error: string;
    models: FetchedModel[];
  };
  expect(error).toBe("");
  return models;
}

test("a listing keeps whether a model takes pictures, when it says so with a boolean", async () => {
  expect(
    await listed([
      { id: "vision", multimodal: true },
      { id: "text", multimodal: false },
      { id: "vague", multimodal: "yes" },
      { id: "silent" },
    ]),
  ).toEqual([
    { id: "vision", multimodal: true },
    { id: "text", multimodal: false },
    { id: "vague" },
    { id: "silent" },
  ]);
});

test("a listing keeps the reasoning levels of a model in order, without empty or non-text entries", async () => {
  expect(
    await listed([
      { id: "thinker", reasoning_levels: ["low", "", 3, null, "high", "max"] },
      { id: "none", reasoning_levels: [] },
      { id: "scalar", reasoning_levels: "low" },
      { id: "silent" },
    ]),
  ).toEqual([
    { id: "thinker", reasoning_levels: ["low", "high", "max"] },
    { id: "none", reasoning_levels: [] },
    { id: "scalar" },
    { id: "silent" },
  ]);
});

test("a listing keeps a model's default reasoning level only when it names one", async () => {
  expect(
    await listed([
      { id: "named", reasoning_default: "medium" },
      { id: "blank", reasoning_default: "" },
      { id: "number", reasoning_default: 2 },
      { id: "silent" },
    ]),
  ).toEqual([
    { id: "named", reasoning_default: "medium" },
    { id: "blank" },
    { id: "number" },
    { id: "silent" },
  ]);
});

test("a listing keeps whether reasoning may be turned off, when it says so with a boolean", async () => {
  expect(
    await listed([
      { id: "may", allow_reasoning_off: true },
      { id: "may-not", allow_reasoning_off: false },
      { id: "vague", allow_reasoning_off: "true" },
      { id: "silent" },
    ]),
  ).toEqual([
    { id: "may", allow_reasoning_off: true },
    { id: "may-not", allow_reasoning_off: false },
    { id: "vague" },
    { id: "silent" },
  ]);
});

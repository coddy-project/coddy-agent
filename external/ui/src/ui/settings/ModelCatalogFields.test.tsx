import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MaxTokensField, MultimodalField } from "./ModelCatalogFields";
import type { ProviderRow } from "./useProviderModels";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const CLAUDE: ProviderRow = { name: "claude", type: "anthropic", api_key: "k" };

type Suggestion = {
  context_window?: number;
  max_tokens?: number;
  multimodal: boolean;
};

/**
 * The provider's listing reports `reported` for claude-sonnet-4-5 (none when
 * undefined); the catalog answers `suggestion` for it. Returns the mocks of
 * the two routes.
 */
function stub(suggestion: Suggestion | undefined, reported?: number) {
  const listing = vi.fn(async (_input: unknown, _init?: RequestInit) => ({
    ok: true,
    json: async () => ({
      ok: true,
      models: [{ id: "claude-sonnet-4-5", context_window: reported }],
    }),
  }));
  const catalog = vi.fn(async (_input: unknown, _init?: RequestInit) => ({
    ok: true,
    json: async () => ({
      ok: true,
      enabled: true,
      models: suggestion ? { "claude-sonnet-4-5": suggestion } : {},
    }),
  }));
  vi.stubGlobal("fetch", (input: unknown, init?: RequestInit) =>
    String(input) === "/coddy/models/catalog"
      ? catalog(input, init)
      : listing(input, init),
  );
  return { listing, catalog };
}

function Row(props: { maxTokens?: unknown; multimodal?: unknown }) {
  const [maxTokens, setMaxTokens] = React.useState<unknown>(props.maxTokens);
  const [multimodal, setMultimodal] = React.useState<unknown>(props.multimodal);
  return (
    <>
      <MaxTokensField
        value={maxTokens}
        onChange={setMaxTokens}
        model="claude/claude-sonnet-4-5"
        providerRow={CLAUDE}
        label="Max tokens"
      />
      <MultimodalField
        value={multimodal}
        onChange={setMultimodal}
        model="claude/claude-sonnet-4-5"
        providerRow={CLAUDE}
        label="Multimodal"
      />
      <output data-testid="max">{JSON.stringify(maxTokens ?? null)}</output>
      <output data-testid="mm">{JSON.stringify(multimodal ?? null)}</output>
    </>
  );
}

const SONNET = { context_window: 200000, max_tokens: 40000, multimodal: true };

// A new row is seeded with max_tokens 4096, which nobody chose: the hint still
// shows, and only the click writes the catalog's number.
test("the catalog's max_tokens is offered over the seed and written on Apply", async () => {
  stub(SONNET);
  render(<Row maxTokens={4096} />);

  const hint = await screen.findByTestId("max-tokens-catalog");
  expect(hint.textContent).toContain("The models catalog suggests 40,000.");
  expect(screen.getByTestId("max").textContent).toBe("4096");

  fireEvent.click(screen.getByTestId("max-tokens-catalog-apply"));
  expect(screen.getByTestId("max").textContent).toBe("40000");
  expect(screen.queryByTestId("max-tokens-catalog")).toBeNull();
});

// A value the operator typed stays until they apply another.
test("a hand-set max_tokens is never overwritten without the click", async () => {
  stub(SONNET);
  render(<Row maxTokens={8000} />);

  await screen.findByTestId("max-tokens-catalog");
  await new Promise((r) => setTimeout(r, 20));
  expect(screen.getByTestId("max").textContent).toBe("8000");
});

test("no hint while the field already holds the catalog's number", async () => {
  const { catalog } = stub(SONNET);
  render(<Row maxTokens={40000} multimodal={true} />);

  await waitFor(() => expect(catalog).toHaveBeenCalled());
  await new Promise((r) => setTimeout(r, 20));
  expect(screen.queryByTestId("max-tokens-catalog")).toBeNull();
  expect(screen.queryByTestId("multimodal-catalog")).toBeNull();
});

test("the catalog's image support is offered on a switch that is off", async () => {
  stub(SONNET);
  render(<Row multimodal={false} />);

  const hint = await screen.findByTestId("multimodal-catalog");
  expect(hint.textContent).toContain(
    "The models catalog says this model reads images.",
  );
  fireEvent.click(screen.getByTestId("multimodal-catalog-apply"));
  expect(screen.getByTestId("mm").textContent).toBe("true");
  expect(screen.queryByTestId("multimodal-catalog")).toBeNull();
});

test("a model the catalog does not know gets no hint", async () => {
  const { catalog } = stub(undefined);
  render(<Row maxTokens={4096} multimodal={false} />);

  await waitFor(() => expect(catalog).toHaveBeenCalled());
  await new Promise((r) => setTimeout(r, 20));
  expect(screen.queryByTestId("max-tokens-catalog")).toBeNull();
  expect(screen.queryByTestId("multimodal-catalog")).toBeNull();
});

// The two fields of a row share one read of the provider's listing and one
// lookup, and the lookup carries the window the provider reports, which the
// suggested max_tokens is measured against.
test("the fields of a row share one lookup carrying the provider's window", async () => {
  const { listing, catalog } = stub(SONNET, 100000);
  render(<Row maxTokens={4096} multimodal={false} />);

  await screen.findByTestId("max-tokens-catalog");
  await screen.findByTestId("multimodal-catalog");
  expect(listing).toHaveBeenCalledTimes(1);
  expect(catalog).toHaveBeenCalledTimes(1);
  const body = JSON.parse(
    String((catalog.mock.calls[0]?.[1] as RequestInit).body),
  ) as Record<string, unknown>;
  expect(body).toEqual({
    name: "claude",
    type: "anthropic",
    api_base: "",
    models: [{ id: "claude-sonnet-4-5", context_window: 100000 }],
  });
});

test("without a provider row nothing is asked", async () => {
  const { listing, catalog } = stub(SONNET);
  render(
    <MaxTokensField
      value={4096}
      onChange={() => {}}
      model="nowhere/m1"
      providerRow={undefined}
      label="Max tokens"
    />,
  );
  await new Promise((r) => setTimeout(r, 20));
  expect(listing).not.toHaveBeenCalled();
  expect(catalog).not.toHaveBeenCalled();
});

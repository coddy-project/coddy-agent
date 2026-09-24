import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ProviderModelList } from "./ProviderModelList";
import { AUTO_FETCH_DEBOUNCE_MS } from "./useProviderModels";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

type StubModel = { id: string; name?: string; context_window?: number };

/** Answers every POST /coddy/providers/models with the given list (or an error). */
function stubModels(models: StubModel[], ok = true) {
  const fetchMock = vi.fn(async (_input: unknown, _init?: RequestInit) => ({
    ok: true,
    json: async () => ({ ok, models, error: ok ? undefined : "boom" }),
  }));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function postedBody(fetchMock: ReturnType<typeof stubModels>, call = 0) {
  const init = fetchMock.mock.calls[call]?.[1] as RequestInit | undefined;
  return JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
}

function toggle(id: string): HTMLButtonElement {
  return screen.getByTestId(`provider-model-toggle-${id}`) as HTMLButtonElement;
}

const noop = () => {};

test("opening a provider row fetches the list with the row as the form holds it", async () => {
  const fetchMock = stubModels([{ id: "m1" }, { id: "m2", name: "Model Two" }]);
  render(
    <ProviderModelList
      provider={{
        name: "demo",
        type: "openai",
        api_base: "http://stub/v1",
        api_key: "sk-unsaved",
        timeout_ms: 0,
      }}
      existingModels={[]}
      onAddModel={noop}
      onRemoveModel={noop}
    />,
  );

  await screen.findByTestId("provider-model-m1");
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(fetchMock).toHaveBeenCalledWith(
    "/coddy/providers/models",
    expect.objectContaining({ method: "POST" }),
  );
  // The unsaved key travels; keys the route does not read stay home.
  expect(postedBody(fetchMock)).toEqual({
    name: "demo",
    type: "openai",
    api_base: "http://stub/v1",
    api_key: "sk-unsaved",
    api_key_command: "",
    proxy: "",
  });
  // A listed answer needs no words: no status line under the legend.
  expect(screen.queryByTestId("provider-models-status")).toBeNull();
  // A display name that says more than the id is the row's tooltip.
  expect(screen.getByTestId("provider-model-m2").getAttribute("title")).toBe(
    "Model Two",
  );
});

test("the refresh icon sits in the legend beside Models", async () => {
  stubModels([{ id: "m1" }]);
  render(
    <ProviderModelList
      provider={{ name: "demo", type: "openai" }}
      existingModels={[]}
      onAddModel={noop}
      onRemoveModel={noop}
    />,
  );

  await screen.findByTestId("provider-model-m1");
  const legend = screen.getByTestId("provider-models").querySelector("legend");
  expect(legend?.textContent).toBe("Models");
  expect(legend?.contains(screen.getByTestId("provider-models-refresh"))).toBe(
    true,
  );
  expect(legend?.querySelector(".field-hint")).toBeNull();
});

test("a row without a name or a type fetches nothing and says why", () => {
  const fetchMock = stubModels([{ id: "m1" }]);
  render(
    <ProviderModelList
      provider={{ type: "openai" }}
      existingModels={[]}
      onAddModel={noop}
      onRemoveModel={noop}
    />,
  );

  expect(fetchMock).not.toHaveBeenCalled();
  expect(
    (screen.getByTestId("provider-models-refresh") as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  expect(screen.getByTestId("provider-models-status").textContent).toBe(
    "Give the provider an id and pick its type to list its models.",
  );
});

test("a model is added to Logical models from its row", async () => {
  stubModels([{ id: "m1", context_window: 131072 }, { id: "m2" }]);
  const onAddModel = vi.fn();
  render(
    <ProviderModelList
      provider={{ name: "demo", type: "openai" }}
      existingModels={[]}
      onAddModel={onAddModel}
      onRemoveModel={noop}
    />,
  );

  await screen.findByTestId("provider-model-m1");
  expect(toggle("m1").getAttribute("aria-pressed")).toBe("false");
  expect(toggle("m1").getAttribute("title")).toBe(
    "Add demo/m1 to Logical models",
  );
  fireEvent.click(toggle("m1"));
  fireEvent.click(toggle("m2"));

  // The context window the provider reports travels with the id.
  expect(onAddModel).toHaveBeenNthCalledWith(1, "demo/m1", 131072);
  expect(onAddModel).toHaveBeenNthCalledWith(2, "demo/m2", undefined);
});

test("a model already in Logical models is checked and unchecking removes it", async () => {
  stubModels([{ id: "m1" }, { id: "m2" }]);
  const onAddModel = vi.fn();
  const onRemoveModel = vi.fn();
  render(
    <ProviderModelList
      provider={{ name: "demo", type: "openai" }}
      existingModels={["demo/m1", "other/m2"]}
      onAddModel={onAddModel}
      onRemoveModel={onRemoveModel}
    />,
  );

  await screen.findByTestId("provider-model-m1");
  expect(toggle("m1").getAttribute("aria-pressed")).toBe("true");
  expect(toggle("m1").classList.contains("is-listed")).toBe(true);
  expect(toggle("m1").getAttribute("title")).toBe(
    "Remove demo/m1 from Logical models",
  );
  // Another provider's m2 does not count for this one.
  expect(toggle("m2").getAttribute("aria-pressed")).toBe("false");

  fireEvent.click(toggle("m1"));
  expect(onRemoveModel).toHaveBeenCalledWith("demo/m1");
  expect(onAddModel).not.toHaveBeenCalled();
});

test("a listed model the provider does not advertise is flagged and can be removed", async () => {
  stubModels([{ id: "m1" }]);
  const removed: string[] = [];
  render(
    <ProviderModelList
      provider={{ name: "demo", type: "openai" }}
      existingModels={["demo/m1", "demo/gone", "other/gone"]}
      onAddModel={noop}
      onRemoveModel={(id) => removed.push(id)}
    />,
  );

  const stale = await screen.findByTestId("provider-model-stale-gone");
  expect(stale.classList.contains("is-stale")).toBe(true);
  // The row stays an id; the explanation is its tooltip only.
  expect(stale.textContent).toBe("gone");
  expect(stale.getAttribute("title")).toBe(
    "demo/gone is not advertised by the provider anymore",
  );
  // Another provider's rows are not judged by this provider's list.
  expect(screen.getAllByTestId(/provider-model-stale-/)).toHaveLength(1);

  expect(toggle("gone").getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(toggle("gone"));
  expect(removed).toEqual(["demo/gone"]);
});

test("a failed fetch reports the error and lists nothing", async () => {
  stubModels([], false);
  render(
    <ProviderModelList
      provider={{ name: "demo", type: "openai" }}
      existingModels={["demo/m1"]}
      onAddModel={noop}
      onRemoveModel={noop}
    />,
  );

  await waitFor(() =>
    expect(screen.getByTestId("provider-models-status").textContent).toBe(
      "Couldn't fetch models: boom",
    ),
  );
  expect(
    screen
      .getByTestId("provider-models-status")
      .classList.contains("settings-error"),
  ).toBe(true);
  expect(screen.queryByTestId("provider-models-list")).toBeNull();
  // Without a list nothing can be called stale.
  expect(screen.queryByTestId("provider-model-stale-m1")).toBeNull();
});

test("an empty answer says the provider returned nothing", async () => {
  stubModels([]);
  render(
    <ProviderModelList
      provider={{ name: "demo", type: "openai" }}
      existingModels={[]}
      onAddModel={noop}
      onRemoveModel={noop}
    />,
  );

  await waitFor(() =>
    expect(screen.getByTestId("provider-models-status").textContent).toBe(
      "The provider returned no models.",
    ),
  );
});

test("the context window the provider reports shows right of the id", async () => {
  stubModels([{ id: "m1", context_window: 131072 }, { id: "m2" }]);
  render(
    <ProviderModelList
      provider={{ name: "demo", type: "openai" }}
      existingModels={[]}
      onAddModel={noop}
      onRemoveModel={noop}
    />,
  );

  const ctx = await screen.findByText("131k");
  expect(ctx.getAttribute("title")).toBe("Context window of 131,072 tokens");
  const row = screen.getByTestId("provider-model-m1");
  // id, then the window, then the toggle.
  expect([...row.children].map((el) => el.className)).toEqual([
    "provider-models-item-id",
    "provider-models-item-ctx",
    "provider-models-toggle",
  ]);
  expect(
    screen
      .getByTestId("provider-model-m2")
      .querySelector(".provider-models-item-ctx"),
  ).toBeNull();
});

test("a long list gets a filter that narrows the rows", async () => {
  stubModels(Array.from({ length: 10 }, (_, i) => ({ id: `m${i + 1}` })));
  render(
    <ProviderModelList
      provider={{ name: "demo", type: "openai" }}
      existingModels={["demo/m10"]}
      onAddModel={noop}
      onRemoveModel={noop}
    />,
  );

  await screen.findByTestId("provider-model-m1");
  fireEvent.change(screen.getByTestId("provider-models-filter"), {
    target: { value: "M1" },
  });

  expect(
    screen
      .getAllByTestId(/^provider-model-m\d+$/)
      .map((el) => el.dataset.testid),
  ).toEqual(["provider-model-m1", "provider-model-m10"]);

  fireEvent.change(screen.getByTestId("provider-models-filter"), {
    target: { value: "nothing" },
  });
  expect(screen.getByText("No model matches the filter.")).toBeTruthy();
});

test("a short list has no filter", async () => {
  stubModels([{ id: "m1" }, { id: "m2" }]);
  render(
    <ProviderModelList
      provider={{ name: "demo", type: "openai" }}
      existingModels={[]}
      onAddModel={noop}
      onRemoveModel={noop}
    />,
  );

  await screen.findByTestId("provider-model-m1");
  expect(screen.queryByTestId("provider-models-filter")).toBeNull();
});

test("a display name that only restyles the id is not repeated", async () => {
  stubModels([
    { id: "gpt-5.6-sol", name: "GPT-5.6-Sol" },
    { id: "m2", name: "Model Two" },
  ]);
  render(
    <ProviderModelList
      provider={{ name: "demo", type: "openai" }}
      existingModels={[]}
      onAddModel={noop}
      onRemoveModel={noop}
    />,
  );

  const row = await screen.findByTestId("provider-model-gpt-5.6-sol");
  expect(row.getAttribute("title")).toBeNull();
  expect(screen.getByTestId("provider-model-m2").getAttribute("title")).toBe(
    "Model Two",
  );
});

// The server runs a posted api_key_command. An automatic fetch must never
// carry it - it would run whatever is half-typed in the field - so only the
// refresh icon, pressed on purpose, sends it.
test("an automatic fetch leaves the credential command out; the refresh icon sends it", async () => {
  const fetchMock = stubModels([{ id: "m1" }]);
  render(
    <ProviderModelList
      provider={{
        name: "demo",
        type: "openai",
        api_key_command: "pass show demo",
      }}
      existingModels={[]}
      onAddModel={noop}
      onRemoveModel={noop}
    />,
  );

  await screen.findByTestId("provider-model-m1");
  expect(postedBody(fetchMock, 0).api_key_command).toBe("");

  fireEvent.click(screen.getByTestId("provider-models-refresh"));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  expect(postedBody(fetchMock, 1).api_key_command).toBe("pass show demo");
});

// A key typed next to a half-typed endpoint must not travel to it: edits to
// the endpoint, the key or the proxy fetch nothing by themselves, the refresh
// icon sends the row as it now stands. A new id or type is another provider,
// fetched once it stays still.
test("connection edits wait for the refresh icon; a new id fetches once it settles", async () => {
  const fetchMock = stubModels([{ id: "m1" }]);
  const props = {
    existingModels: [],
    onAddModel: noop,
    onRemoveModel: noop,
  };
  const { rerender } = render(
    <ProviderModelList
      provider={{ name: "demo", type: "openai" }}
      {...props}
    />,
  );
  await screen.findByTestId("provider-model-m1");
  expect(fetchMock).toHaveBeenCalledTimes(1);

  for (const base of ["http://a", "http://ap", "http://api.example/v1"]) {
    rerender(
      <ProviderModelList
        provider={{
          name: "demo",
          type: "openai",
          api_base: base,
          api_key: "sk-1",
        }}
        {...props}
      />,
    );
  }
  await new Promise((r) => setTimeout(r, AUTO_FETCH_DEBOUNCE_MS + 150));
  expect(fetchMock).toHaveBeenCalledTimes(1);

  fireEvent.click(screen.getByTestId("provider-models-refresh"));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  expect(postedBody(fetchMock, 1)).toMatchObject({
    api_base: "http://api.example/v1",
    api_key: "sk-1",
  });

  for (const name of ["d", "de", "dem", "demo2"]) {
    rerender(
      <ProviderModelList
        provider={{ name, type: "openai", api_base: "http://api.example/v1" }}
        {...props}
      />,
    );
  }
  expect(fetchMock).toHaveBeenCalledTimes(2);
  await new Promise((r) => setTimeout(r, AUTO_FETCH_DEBOUNCE_MS + 150));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
  expect(postedBody(fetchMock, 2).name).toBe("demo2");
});

// Containment contract (DESIGN.md, "Provider models list"). jsdom does no
// layout, so the rules that keep a long id from widening the form past a
// phone's panel are pinned as source: the fieldset gives up the min-content
// width every <fieldset> defaults to, the id may shrink and ellipsizes, and
// the toggle never squeezes.
test("the list is contained: the box may shrink, the id ellipsizes, the toggle never squeezes", () => {
  const css = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
    "utf8",
  );
  const rule = (selector: string) => {
    const m = css.match(
      new RegExp(
        `^${selector.replace(/[.>*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{([^}]*)\\}`,
        "m",
      ),
    );
    expect(m, `missing rule ${selector}`).not.toBeNull();
    return m![1]!;
  };
  expect(rule(".provider-models-box")).toMatch(/min-inline-size:\s*0/);
  const id = rule(".provider-models-item-id");
  expect(id).toMatch(/min-width:\s*0/);
  expect(id).toMatch(/text-overflow:\s*ellipsis/);
  expect(rule(".provider-models-toggle")).toMatch(/flex:\s*0 0 auto/);
  // No separators between the rows, and a row only as wide as its content,
  // so the hover lights the id and its toggle rather than the box's width.
  const item = rule(".provider-models-item");
  expect(item).not.toMatch(/border-bottom/);
  expect(item).toMatch(/align-self:\s*flex-start/);
  expect(item).toMatch(/max-width:\s*calc\(100% \+ 6px\)/);
  expect(item).toMatch(/margin-left:\s*-6px/);
});

import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { ContextWindowField } from "./ContextWindowField";
import type { ProviderRow } from "./useProviderModels";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const DEMO: ProviderRow = {
  name: "demo",
  type: "openai",
  api_base: "http://stub/v1",
  api_key: "sk-unsaved",
};

type StubModel = { id: string; context_window?: number };

function stubModels(models: StubModel[] | { error: string }) {
  const fetchMock = vi.fn(async (_input: unknown, _init?: RequestInit) => ({
    ok: true,
    json: async () =>
      Array.isArray(models)
        ? { ok: true, models }
        : { ok: false, error: models.error, models: [] },
  }));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function Harness(props: {
  initial?: unknown;
  model?: string;
  providerRow?: ProviderRow | undefined;
}) {
  const [val, setVal] = React.useState<unknown>(props.initial);
  return (
    <>
      <ContextWindowField
        value={val}
        onChange={setVal}
        model={props.model ?? "demo/m1"}
        providerRow={"providerRow" in props ? props.providerRow : DEMO}
        label="Context window (tokens)"
      />
      <output data-testid="val">{JSON.stringify(val ?? null)}</output>
    </>
  );
}

function input(): HTMLInputElement {
  return screen.getByTestId("context-window-input") as HTMLInputElement;
}

test("an unset window shows the one the provider reports for the model", async () => {
  const fetchMock = stubModels([
    { id: "m1", context_window: 131072 },
    { id: "m2", context_window: 8192 },
  ]);
  render(<Harness initial={0} />);

  await waitFor(() =>
    expect(input().placeholder).toBe("131072, reported by demo"),
  );
  expect(input().value).toBe("");
  // Asked with the provider row as the document holds it.
  expect(fetchMock).toHaveBeenCalledWith(
    "/coddy/providers/models",
    expect.objectContaining({ method: "POST" }),
  );
  const body = JSON.parse(
    String((fetchMock.mock.calls[0]?.[1] as RequestInit).body),
  ) as Record<string, unknown>;
  expect(body).toMatchObject({ name: "demo", api_key: "sk-unsaved" });
  // Showing the provider's number writes nothing.
  expect(screen.getByTestId("val").textContent).toBe("0");
});

test("a window the provider does not report falls back to the default in the placeholder", async () => {
  stubModels([{ id: "m1" }]);
  render(<Harness />);

  await waitFor(() => expect(input().placeholder).toBe("128000, the default"));
});

test("fetching the context window writes the provider's number into the model", async () => {
  stubModels([{ id: "m1", context_window: 131072 }]);
  render(<Harness initial={0} />);

  await waitFor(() =>
    expect(input().placeholder).toBe("131072, reported by demo"),
  );
  fireEvent.click(screen.getByTestId("context-window-fetch"));

  await waitFor(() =>
    expect(screen.getByTestId("val").textContent).toBe("131072"),
  );
  expect(input().value).toBe("131072");
});

test("fetching for a model the provider does not report says so and writes nothing", async () => {
  stubModels([{ id: "other", context_window: 4096 }]);
  render(<Harness initial={0} />);

  await waitFor(() =>
    expect(
      (screen.getByTestId("context-window-fetch") as HTMLButtonElement)
        .disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByTestId("context-window-fetch"));

  expect((await screen.findByTestId("context-window-note")).textContent).toBe(
    "demo does not report a context window for m1.",
  );
  expect(screen.getByTestId("val").textContent).toBe("0");
});

test("a failed fetch reports the provider's error", async () => {
  stubModels({ error: "bad key" });
  render(<Harness initial={0} />);

  await waitFor(() =>
    expect(
      (screen.getByTestId("context-window-fetch") as HTMLButtonElement)
        .disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByTestId("context-window-fetch"));

  const note = await screen.findByTestId("context-window-note");
  expect(note.textContent).toBe("Couldn't fetch the context window: bad key");
  expect(note.classList.contains("settings-error")).toBe(true);
});

test("a typed window is the model's; clearing it goes back to the provider's", async () => {
  stubModels([{ id: "m1", context_window: 131072 }]);
  render(<Harness initial={32768} />);

  expect(input().value).toBe("32768");
  fireEvent.change(input(), { target: { value: "65536" } });
  expect(screen.getByTestId("val").textContent).toBe("65536");

  fireEvent.change(input(), { target: { value: "" } });
  expect(screen.getByTestId("val").textContent).toBe("0");
  await waitFor(() =>
    expect(input().placeholder).toBe("131072, reported by demo"),
  );
});

test("without a provider row there is nothing to ask", () => {
  const fetchMock = stubModels([]);
  render(<Harness model="unknown/m1" providerRow={undefined} />);

  expect(fetchMock).not.toHaveBeenCalled();
  expect(
    (screen.getByTestId("context-window-fetch") as HTMLButtonElement).disabled,
  ).toBe(true);
  expect(input().placeholder).toBe("128000, the default");
});

// An answer to an explicit ask is about the model it was asked for: if the id
// on screen changed while it was in flight, it must not land on the new one.
test("a context window that arrives after the model id changed is not written", async () => {
  let release: (() => void) | undefined;
  let calls = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => {
      calls++;
      if (calls === 2) {
        await new Promise<void>((resolve) => {
          release = resolve;
        });
      }
      return {
        ok: true,
        json: async () => ({
          ok: true,
          models: [
            { id: "m1", context_window: 131072 },
            { id: "m2", context_window: 8192 },
          ],
        }),
      };
    }),
  );
  function Switching() {
    const [model, setModel] = React.useState("demo/m1");
    const [val, setVal] = React.useState<unknown>(0);
    return (
      <>
        <ContextWindowField
          value={val}
          onChange={setVal}
          model={model}
          providerRow={DEMO}
          label="Context window (tokens)"
        />
        <button data-testid="switch" onClick={() => setModel("demo/m2")} />
        <output data-testid="val">{JSON.stringify(val)}</output>
      </>
    );
  }
  render(<Switching />);
  await waitFor(() =>
    expect(input().placeholder).toBe("131072, reported by demo"),
  );

  fireEvent.click(screen.getByTestId("context-window-fetch"));
  await waitFor(() => expect(release).toBeDefined());
  fireEvent.click(screen.getByTestId("switch"));
  release!();
  await new Promise((r) => setTimeout(r, 20));

  expect(screen.getByTestId("val").textContent).toBe("0");
});

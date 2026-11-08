import { afterEach, expect, test, vi } from "vitest";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { ExtensionsProvider, useExtensions } from "./ExtensionsProvider";
import { ExtensionRuntime } from "./runtime";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const installed = {
  api: 1,
  id: "demo",
  name: "Demo",
  version: "1.0.0",
  sha256: "a".repeat(64),
  entry_sha256: "b".repeat(64),
  enabled: false,
  ui: { entry: "main.js" },
};

test("a poll that finds the same packages does not re-render the context", async () => {
  const fetchSpy = vi.spyOn(globalThis, "fetch").mockImplementation(
    async () =>
      new Response(JSON.stringify({ items: [installed] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
  );
  let renders = 0;
  function Consumer() {
    useExtensions();
    renders += 1;
    return null;
  }
  render(
    <ExtensionsProvider>
      <Consumer />
    </ExtensionsProvider>,
  );
  // The first answer is the one list change this test allows.
  await waitFor(() => expect(fetchSpy).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(renders).toBeGreaterThanOrEqual(2));
  await act(async () => {});
  const settled = renders;

  // Focus polls the list again; the packages are unchanged.
  for (let i = 0; i < 2; i++) {
    await act(async () => {
      window.dispatchEvent(new Event("focus"));
    });
    await waitFor(() => expect(fetchSpy).toHaveBeenCalledTimes(2 + i));
    await act(async () => {});
  }
  expect(renders).toBe(settled);
});

test("an explicit refresh is the one that asks for the failed again", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(
    async () =>
      new Response(JSON.stringify({ items: [installed] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
  );
  const reconcile = vi
    .spyOn(ExtensionRuntime.prototype, "reconcile")
    .mockResolvedValue(undefined);
  let refresh!: (retryFailed?: boolean) => Promise<void>;
  function Consumer() {
    refresh = useExtensions()!.refresh;
    return null;
  }
  render(
    <ExtensionsProvider>
      <Consumer />
    </ExtensionsProvider>,
  );
  await waitFor(() => expect(reconcile).toHaveBeenCalled());
  expect(reconcile).toHaveBeenLastCalledWith([installed], false);
  await act(async () => {
    await refresh(true);
  });
  expect(reconcile).toHaveBeenLastCalledWith([installed], true);
});

test("surfaces coming and going arrive as events", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(
    async () =>
      new Response(JSON.stringify({ items: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
  );
  let runtime!: ExtensionRuntime;
  function Consumer() {
    runtime = useExtensions()!.runtime;
    return null;
  }
  render(
    <ExtensionsProvider>
      <Consumer />
    </ExtensionsProvider>,
  );
  await waitFor(() => expect(runtime).toBeDefined());

  const surfaces: unknown[] = [];
  const overlaySets: unknown[] = [];
  const signal = new AbortController().signal;
  runtime.events.on("watcher", signal, "surfaceChanged", (event) =>
    surfaces.push(event),
  );
  runtime.events.on("watcher", signal, "overlaysChanged", (event) =>
    overlaySets.push(event),
  );
  // Mount alone is not a change: the diff starts from what is already open.
  await act(async () => {});
  expect(surfaces).toEqual([]);
  expect(overlaySets).toEqual([]);

  const publish = (patch: Record<string, unknown>) =>
    (
      runtime as unknown as {
        publish: (p: Record<string, unknown>) => void;
      }
    ).publish(patch);
  const shellRow = {
    key: "demo/big",
    owner: "demo",
    id: "big",
    mount: () => {},
    api: {},
  };
  await act(async () => {
    publish({ shell: shellRow });
  });
  expect(surfaces).toEqual([{ surface: "shell", id: "demo/big", open: true }]);

  await act(async () => {
    publish({
      shell: null,
      overlays: [{ ...shellRow, key: "demo/float", id: "float" }],
    });
  });
  expect(surfaces[1]).toEqual({ surface: "shell", id: null, open: false });
  expect(overlaySets).toEqual([{ ids: ["demo/float"] }]);

  await act(async () => {
    publish({ overlays: [] });
  });
  expect(overlaySets[1]).toEqual({ ids: [] });

  const panelRow = {
    ...shellRow,
    key: "demo/lab",
    id: "lab",
    title: "Lab",
    placement: "slide-over",
  };
  await act(async () => {
    publish({ panels: [panelRow] });
    (runtime as unknown as { openPanel: (panel: unknown) => void }).openPanel(
      panelRow,
    );
  });
  expect(surfaces[2]).toEqual({
    surface: "panel",
    id: "demo/lab",
    open: true,
  });
  // Losing the registration closes the visible panel even while the host's
  // open-panel state still points at its key.
  await act(async () => {
    publish({ panels: [] });
  });
  expect(surfaces[3]).toEqual({ surface: "panel", id: null, open: false });
});

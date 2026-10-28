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

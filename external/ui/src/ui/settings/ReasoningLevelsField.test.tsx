import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { ReasoningLevelsField } from "./ReasoningLevelsField";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

// The field's three states are "key absent" (auto-detect), [] (selector hidden),
// and a non-empty list. JSON.stringify is how the settings document reaches the
// server, so the harness reports the value through it: an absent key must stay
// absent, which `undefined` alone would not show.
function Harness({
  initial,
  providerType,
}: {
  initial?: unknown;
  providerType?: string;
}) {
  const [model, setModel] = React.useState<Record<string, unknown>>(
    initial === undefined
      ? { model: "valera/qwen3.8-27b" }
      : { model: "valera/qwen3.8-27b", reasoning_levels: initial },
  );
  return (
    <>
      <output data-testid="model-json">{JSON.stringify(model)}</output>
      {/* Stands in for the operator retyping the model id above the field. */}
      <button
        type="button"
        data-testid="retype-model"
        onClick={() => setModel((m) => ({ ...m, model: "valera/gpt-5.5" }))}
      >
        retype
      </button>
      <ReasoningLevelsField
        value={model["reasoning_levels"]}
        onChange={(v) => setModel((m) => ({ ...m, reasoning_levels: v }))}
        model={String(model["model"])}
        providerType={providerType}
        label="Reasoning levels"
      />
    </>
  );
}

// deferredFetch hands the test the resolve handle of a fetch that has not
// answered yet, so it can change the form in between and then let the stale
// answer land.
function deferredFetch(payload: unknown) {
  let resolve: (r: Response) => void = () => {};
  const pending = new Promise<Response>((r) => {
    resolve = r;
  });
  vi.spyOn(globalThis, "fetch").mockReturnValue(pending);
  return () =>
    resolve({
      ok: true,
      status: 200,
      json: async () => payload,
    } as unknown as Response);
}

function savedModel(): Record<string, unknown> {
  return JSON.parse(screen.getByTestId("model-json").textContent || "{}");
}

function mockFetch(payload: unknown, ok = true) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValue({
    ok,
    status: ok ? 200 : 500,
    json: async () => payload,
  } as unknown as Response);
}

test("fetching fills the list with the levels detected for the model id", async () => {
  const f = mockFetch({
    ok: true,
    levels: ["low", "medium", "high"],
    detected: true,
  });

  render(<Harness />);
  expect(savedModel()).toEqual({ model: "valera/qwen3.8-27b" });

  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));
  await waitFor(() =>
    expect(savedModel()).toEqual({
      model: "valera/qwen3.8-27b",
      reasoning_levels: ["low", "medium", "high"],
    }),
  );

  expect(f).toHaveBeenCalledWith(
    "/coddy/config/reasoning-levels?model=valera%2Fqwen3.8-27b",
  );
  expect(screen.getByTestId("reasoning-levels-item-0")).toBeTruthy();
  expect(screen.getByTestId("reasoning-levels-item-2")).toBeTruthy();
});

test("a model with no detected levels is not turned into an empty override", async () => {
  mockFetch({ ok: true, levels: [], detected: false });

  render(<Harness />);
  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));

  await waitFor(() =>
    expect(screen.getByTestId("reasoning-levels-status").textContent).toContain(
      "no auto-detected reasoning levels",
    ),
  );
  // An empty list would read as the explicit opt-out and hide the selector.
  expect(savedModel()).toEqual({ model: "valera/qwen3.8-27b" });
});

test("a failed fetch is reported and leaves the field untouched", async () => {
  mockFetch({ ok: false, error: "boom" });

  render(<Harness />);
  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));

  await waitFor(() =>
    expect(screen.getByTestId("reasoning-levels-status").textContent).toContain(
      "boom",
    ),
  );
  expect(savedModel()).toEqual({ model: "valera/qwen3.8-27b" });
});

test("use auto-detected drops the key so the backend detects again", () => {
  render(<Harness initial={["low", "high"]} />);
  expect(savedModel()["reasoning_levels"]).toEqual(["low", "high"]);

  fireEvent.click(screen.getByTestId("reasoning-levels-auto"));

  // JSON.stringify omits an undefined value, which is what makes the saved
  // config omit the key and fall back to auto-detection.
  expect(savedModel()).toEqual({ model: "valera/qwen3.8-27b" });
  expect(screen.getByTestId("reasoning-levels-status").textContent).toContain(
    "Auto-detected",
  );
});

test("removing the last level reaches the explicit opt-out and says so", () => {
  render(<Harness initial={["low"]} />);

  fireEvent.click(screen.getByTestId("reasoning-levels-remove-0"));

  expect(savedModel()).toEqual({
    model: "valera/qwen3.8-27b",
    reasoning_levels: [],
  });
  expect(screen.getByTestId("reasoning-levels-status").textContent).toContain(
    "selector is hidden",
  );
});

test("the fetch button is disabled until a model id is typed", () => {
  function Empty() {
    return (
      <ReasoningLevelsField
        value={undefined}
        onChange={() => {}}
        model="  "
        label="Reasoning levels"
      />
    );
  }
  render(<Empty />);
  expect(
    (screen.getByTestId("reasoning-levels-fetch") as HTMLButtonElement)
      .disabled,
  ).toBe(true);
});

test("adding a level by hand after an empty detection reports the override, not the old fetch", async () => {
  mockFetch({ ok: true, levels: [], detected: false });

  render(<Harness />);
  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));
  await waitFor(() =>
    expect(screen.getByTestId("reasoning-levels-status").textContent).toContain(
      "no auto-detected reasoning levels",
    ),
  );

  fireEvent.click(screen.getByTestId("reasoning-levels-add"));

  // The list now has a row, so the status must describe the list the operator
  // is building rather than keep repeating the fetch that came back empty.
  expect(savedModel()["reasoning_levels"]).toEqual([""]);
  expect(screen.getByTestId("reasoning-levels-status").textContent).toContain(
    "These exact levels",
  );
});

test("retyping the model id clears the feedback of the previous fetch", async () => {
  mockFetch({ ok: false, error: "boom" });

  render(<Harness />);
  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));
  await waitFor(() =>
    expect(screen.getByTestId("reasoning-levels-status").textContent).toContain(
      "boom",
    ),
  );

  fireEvent.click(screen.getByTestId("retype-model"));

  // "boom" was about the old id; the new id starts from the auto-detect state.
  expect(screen.getByTestId("reasoning-levels-status").textContent).toContain(
    "Auto-detected",
  );
  expect(savedModel()).toEqual({ model: "valera/gpt-5.5" });
});

test("a fetch that answers after the model id changed is dropped", async () => {
  const settle = deferredFetch({
    ok: true,
    levels: ["low", "medium", "high"],
    detected: true,
  });

  render(<Harness />);
  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));
  fireEvent.click(screen.getByTestId("retype-model"));
  settle();

  await waitFor(() =>
    expect(screen.getByTestId("reasoning-levels-fetch").textContent).toBe(
      "Fetch reasoning levels",
    ),
  );
  // qwen3 levels must not be written onto the gpt-5.5 entry.
  expect(savedModel()).toEqual({ model: "valera/gpt-5.5" });
});

test("a fetch that answers after the field unmounted does not write back", async () => {
  const settle = deferredFetch({
    ok: true,
    levels: ["low", "medium", "high"],
    detected: true,
  });
  const onChange = vi.fn();
  const view = render(
    <ReasoningLevelsField
      value={undefined}
      onChange={onChange}
      model="valera/qwen3.8-27b"
      label="Reasoning levels"
    />,
  );
  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));
  view.unmount();
  settle();
  await new Promise((r) => setTimeout(r, 0));
  expect(onChange).not.toHaveBeenCalled();
});

test("the provider type chosen in the form travels with the request", async () => {
  const f = mockFetch({
    ok: true,
    levels: ["none", "low", "medium", "high"],
    detected: true,
  });

  render(<Harness providerType="codex" />);
  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));
  await waitFor(() =>
    expect(savedModel()["reasoning_levels"]).toEqual([
      "none",
      "low",
      "medium",
      "high",
    ]),
  );
  expect(f).toHaveBeenCalledWith(
    "/coddy/config/reasoning-levels?model=valera%2Fqwen3.8-27b&provider_type=codex",
  );
});

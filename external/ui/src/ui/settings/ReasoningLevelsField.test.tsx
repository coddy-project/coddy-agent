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

test("a level edited by hand while a fetch is pending is not overwritten by the answer", async () => {
  const settle = deferredFetch({
    ok: true,
    levels: ["low", "medium", "high"],
    detected: true,
  });

  render(<Harness />);
  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));
  // The operator does not wait for the answer and starts a list by hand.
  fireEvent.click(screen.getByTestId("reasoning-levels-add"));
  expect(savedModel()["reasoning_levels"]).toEqual([""]);

  settle();
  await new Promise((r) => setTimeout(r, 0));

  // The manual edit is the newer intent; the late answer must not replace it.
  expect(savedModel()["reasoning_levels"]).toEqual([""]);
  expect(screen.getByTestId("reasoning-levels-status").textContent).toContain(
    "These exact levels",
  );
});

test("removing the last level while a fetch is pending keeps the opt-out", async () => {
  const settle = deferredFetch({
    ok: true,
    levels: ["low", "medium", "high"],
    detected: true,
  });

  render(<Harness initial={["low"]} />);
  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));
  fireEvent.click(screen.getByTestId("reasoning-levels-remove-0"));
  expect(savedModel()["reasoning_levels"]).toEqual([]);

  settle();
  await new Promise((r) => setTimeout(r, 0));

  expect(savedModel()["reasoning_levels"]).toEqual([]);
  expect(screen.getByTestId("reasoning-levels-status").textContent).toContain(
    "selector is hidden",
  );
});

// Add stands alone in the field's column and keeps its own width (the
// .settings-row-action rule), instead of stretching across the field.
test("the Add button is a lone row action, not a full-width bar", () => {
  render(<Harness initial={["low"]} />);

  const add = screen.getByTestId("reasoning-levels-add");
  expect(add.classList.contains("settings-row-action")).toBe(true);
  expect(add.parentElement?.classList.contains("settings-row")).toBe(true);
});

// --- a model of a remote Coddy ---------------------------------------------
//
// The levels of such a model come from the remote's listing while the key is
// absent: there is nothing to detect from an alias and nothing to fetch, so the
// Fetch control goes, the status says who decides, and the levels the listing
// offers show read-only as the inherited value.

const LAB = {
  name: "lab",
  type: "coddy",
  api_base: "https://lab.example:12345",
};

function CoddyHarness({
  initial,
  providerRow = LAB,
}: {
  initial?: unknown;
  /** null stands for "the form has no row for this model's provider". */
  providerRow?: Record<string, unknown> | null;
}) {
  const [model, setModel] = React.useState<Record<string, unknown>>(
    initial === undefined
      ? { model: "lab/terra" }
      : { model: "lab/terra", reasoning_levels: initial },
  );
  return (
    <>
      <output data-testid="model-json">{JSON.stringify(model)}</output>
      <ReasoningLevelsField
        value={model["reasoning_levels"]}
        onChange={(v) => setModel((m) => ({ ...m, reasoning_levels: v }))}
        model={String(model["model"])}
        providerType="coddy"
        providerRow={providerRow ?? undefined}
        label="Reasoning levels"
      />
    </>
  );
}

/** Answers the listing POST with the given models, and fails on any other call. */
function listing(models: unknown[]) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    if (String(input) !== "/coddy/providers/models") {
      throw new Error(`unexpected request ${String(input)}`);
    }
    return {
      ok: true,
      status: 200,
      json: async () => ({ ok: true, models }),
    } as unknown as Response;
  });
}

test("a coddy row has no Fetch: the remote's listing decides, and the levels it offers show", async () => {
  const f = listing([
    {
      id: "terra",
      reasoning_levels: ["low", "high"],
      allow_reasoning_off: true,
    },
  ]);
  render(<CoddyHarness />);
  expect(screen.queryByTestId("reasoning-levels-fetch")).toBeNull();
  expect(screen.getByTestId("reasoning-levels-status").textContent).toBe(
    "The remote's listing decides which levels this model offers.",
  );
  await waitFor(() =>
    expect(screen.getByTestId("reasoning-levels-remote").textContent).toBe(
      "Offered by the remote now: low, high",
    ),
  );
  // The key stays absent: showing the listing writes nothing.
  expect(savedModel()).toEqual({ model: "lab/terra" });
  expect(f).toHaveBeenCalledTimes(1);
  const [, init] = f.mock.calls[0] as [unknown, RequestInit];
  expect(JSON.parse(String(init.body))).toMatchObject({
    name: "lab",
    type: "coddy",
  });
});

test("a model the remote lists with no levels says so", async () => {
  listing([{ id: "terra" }, { id: "coder", reasoning_levels: ["low"] }]);
  render(<CoddyHarness />);
  await waitFor(() =>
    expect(screen.getByTestId("reasoning-levels-remote").textContent).toBe(
      "The remote lists no reasoning levels for this model.",
    ),
  );
});

test("a model the remote does not list, or a listing that failed, adds no line", async () => {
  const f = listing([{ id: "other", reasoning_levels: ["low"] }]);
  const { unmount } = render(<CoddyHarness />);
  await waitFor(() => expect(f).toHaveBeenCalledTimes(1));
  await new Promise((r) => setTimeout(r, 0));
  expect(screen.queryByTestId("reasoning-levels-remote")).toBeNull();
  unmount();
  vi.spyOn(globalThis, "fetch").mockRejectedValue(new Error("down"));
  render(<CoddyHarness />);
  await new Promise((r) => setTimeout(r, 10));
  expect(screen.queryByTestId("reasoning-levels-remote")).toBeNull();
  expect(screen.getByTestId("reasoning-levels-status").textContent).toContain(
    "listing decides",
  );
});

test("without a provider row nothing is fetched and the status still says who decides", async () => {
  const f = listing([]);
  render(<CoddyHarness providerRow={null} />);
  await new Promise((r) => setTimeout(r, 10));
  expect(f).not.toHaveBeenCalled();
  expect(screen.getByTestId("reasoning-levels-status").textContent).toContain(
    "listing decides",
  );
});

test("a list written for a coddy row overrides the remote's, and Follow the remote drops the key", async () => {
  listing([{ id: "terra", reasoning_levels: ["low", "high"] }]);
  render(<CoddyHarness initial={["low"]} />);
  expect(screen.getByTestId("reasoning-levels-status").textContent).toBe(
    "These exact levels are offered for this model, instead of the remote's.",
  );
  expect(screen.queryByTestId("reasoning-levels-fetch")).toBeNull();
  expect(screen.queryByTestId("reasoning-levels-remote")).toBeNull();
  const back = screen.getByTestId("reasoning-levels-auto");
  expect(back.textContent).toBe("Follow the remote");
  fireEvent.click(back);
  expect(savedModel()).toEqual({ model: "lab/terra" });
  await waitFor(() =>
    expect(screen.getByTestId("reasoning-levels-remote").textContent).toContain(
      "low, high",
    ),
  );
});

test("an empty list on a coddy row is the opt-out, and the way back names the remote", () => {
  listing([]);
  render(<CoddyHarness initial={[]} />);
  expect(screen.getByTestId("reasoning-levels-status").textContent).toBe(
    "Empty list: the reasoning selector is hidden for this model. Use 'Follow the remote' to go back.",
  );
});

test("a level can still be added by hand to a coddy row", () => {
  listing([]);
  render(<CoddyHarness />);
  fireEvent.click(screen.getByTestId("reasoning-levels-add"));
  expect(savedModel()["reasoning_levels"]).toEqual([""]);
});

test("another provider type keeps Fetch and asks no listing", () => {
  const f = mockFetch({ ok: true, levels: [], detected: false });
  render(<Harness providerType="openai" />);
  expect(screen.getByTestId("reasoning-levels-fetch")).toBeTruthy();
  expect(screen.queryByTestId("reasoning-levels-remote")).toBeNull();
  expect(f).not.toHaveBeenCalled();
});

import React, { useState } from "react";
import { afterEach, describe, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { Composer } from "./Composer";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const MANY_MODELS = [
  "opencode-go/deepseek-v4-pro",
  "opencode-go/deepseek-v4-flash",
  "opencode-go/kimi-k2.7-code",
  "opencode-go/kimi-k2.6",
  "opencode-go/glm-5.1",
  "opencode-go/glm-5",
  "opencode-go/mimo-v2.5-pro",
  "opencode-go/mimo-v2.5",
  "opencode-go/minimax-m3",
  "opencode-go/minimax-m2.7",
  "cliproxyapi/gpt-5.5",
  "cliproxyapi/gpt-5.4",
  "cliproxyapi/gpt-5.4-mini",
  "cliproxyapi/gemini-3.1-pro-preview",
  "cliproxyapi/gemini-2.5-pro",
];

function renderModelMenu(opts: {
  models?: string[];
  model?: string;
  onChange?: (id: string) => void;
}) {
  return render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModels={opts.models ?? MANY_MODELS}
      llmModel={opts.model ?? MANY_MODELS[0] ?? ""}
      onLlmModelChange={opts.onChange ?? (() => {})}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
}

function openModelMenu() {
  fireEvent.click(screen.getByRole("button", { name: /\bModel$/i }));
}

test("model menu shows a filter input when there are more than 5 models", () => {
  renderModelMenu({});
  openModelMenu();
  expect(screen.getByTestId("model-menu-filter")).toBeTruthy();
});

test("model menu omits the filter input for a short list", () => {
  renderModelMenu({
    models: ["opencode-go/glm-5", "cliproxyapi/gpt-5.5"],
    model: "opencode-go/glm-5",
  });
  openModelMenu();
  expect(screen.queryByTestId("model-menu-filter")).toBeNull();
});

test("exactly 5 models still omits the filter but keeps grouping", () => {
  renderModelMenu({
    models: [
      "opencode-go/glm-5",
      "opencode-go/glm-5.1",
      "opencode-go/kimi-k2.6",
      "cliproxyapi/gpt-5.5",
      "cliproxyapi/gpt-5.4",
    ],
    model: "opencode-go/glm-5",
  });
  openModelMenu();
  expect(screen.queryByTestId("model-menu-filter")).toBeNull();
  expect(screen.getByText("opencode-go")).toBeTruthy();
  expect(screen.getByText("cliproxyapi")).toBeTruthy();
});

test("6 models cross the threshold and reveal the filter", () => {
  renderModelMenu({
    models: [
      "opencode-go/glm-5",
      "opencode-go/glm-5.1",
      "opencode-go/kimi-k2.6",
      "cliproxyapi/gpt-5.5",
      "cliproxyapi/gpt-5.4",
      "cliproxyapi/gpt-5.4-mini",
    ],
    model: "opencode-go/glm-5",
  });
  openModelMenu();
  expect(screen.getByTestId("model-menu-filter")).toBeTruthy();
});

test("desktop shell renders the menu as an anchored dropdown (not a sheet)", () => {
  renderModelMenu({});
  openModelMenu();
  const menu = screen.getByRole("dialog", { name: /model/i });
  expect(menu.className).toContain("mode-menu--portal");
  expect(menu.className).not.toContain("mode-menu--sheet");
});

test("narrow shell renders the model menu as a full-width sheet", () => {
  const original = window.matchMedia;
  // Force the mobile/narrow shell breakpoint (max-width: 1199px) to match.
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: true,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia;
  try {
    renderModelMenu({});
    openModelMenu();
    const menu = screen.getByRole("dialog", { name: /model/i });
    expect(menu.className).toContain("mode-menu--sheet");
    expect(menu.className).not.toContain("mode-menu--portal");
    // Filter still applies on mobile (15 > 5).
    expect(screen.getByTestId("model-menu-filter")).toBeTruthy();
  } finally {
    window.matchMedia = original;
  }
});

test("model menu groups rows under vendor headers when several vendors exist", () => {
  renderModelMenu({});
  openModelMenu();
  expect(screen.getByText("opencode-go")).toBeTruthy();
  expect(screen.getByText("cliproxyapi")).toBeTruthy();
});

test("typing in the filter narrows the listed models", () => {
  renderModelMenu({});
  openModelMenu();
  fireEvent.change(screen.getByTestId("model-menu-filter"), {
    target: { value: "gemini" },
  });
  expect(screen.getByRole("option", { name: "gemini-2.5-pro" })).toBeTruthy();
  expect(screen.queryByRole("option", { name: "deepseek-v4-pro" })).toBeNull();
  // The non-matching vendor header is gone too.
  expect(screen.queryByText("opencode-go")).toBeNull();
});

test("filtering by vendor keeps that vendor's rows", () => {
  renderModelMenu({});
  openModelMenu();
  fireEvent.change(screen.getByTestId("model-menu-filter"), {
    target: { value: "cliproxyapi" },
  });
  const menu = screen.getByRole("dialog", { name: /model/i });
  expect(within(menu).getByRole("option", { name: "gpt-5.5" })).toBeTruthy();
  expect(within(menu).queryByRole("option", { name: "glm-5" })).toBeNull();
});

test("selecting a filtered model calls onLlmModelChange with the full id", () => {
  const onChange = vi.fn();
  renderModelMenu({ onChange });
  openModelMenu();
  fireEvent.change(screen.getByTestId("model-menu-filter"), {
    target: { value: "5.4-mini" },
  });
  fireEvent.click(screen.getByRole("option", { name: "gpt-5.4-mini" }));
  expect(onChange).toHaveBeenCalledWith("cliproxyapi/gpt-5.4-mini");
});

test("empty filter result shows a no-models notice", () => {
  renderModelMenu({});
  openModelMenu();
  fireEvent.change(screen.getByTestId("model-menu-filter"), {
    target: { value: "no-such-model" },
  });
  expect(screen.getByTestId("model-menu-empty")).toBeTruthy();
});

// A menu of the composer answers Escape itself: a short list has no filter to
// take the key, and a long one closes the same way. The key is claimed, so a
// screen of the rail under the chat would stay.
test("Escape closes the model menu, with a filter or without one", () => {
  renderModelMenu({
    models: ["opencode-go/glm-5", "cliproxyapi/gpt-5.5"],
    model: "opencode-go/glm-5",
  });
  openModelMenu();
  expect(screen.getByRole("menu")).toBeTruthy();
  expect(fireEvent.keyDown(document.body, { key: "Escape" })).toBe(false);
  expect(screen.queryByRole("menu")).toBeNull();
  cleanup();

  renderModelMenu({});
  openModelMenu();
  fireEvent.keyDown(screen.getByTestId("model-menu-filter"), { key: "Escape" });
  expect(screen.queryByRole("dialog", { name: /model/i })).toBeNull();
});

// Models added to a provider later are appended to models[] and used to land at
// the bottom of their group, below names that sort after them. Within a vendor
// the menu reads alphabetically, versions compared as numbers; the vendors keep
// the order of the configuration.
test("model menu lists the models of each vendor alphabetically, versions as numbers", () => {
  renderModelMenu({
    models: [
      "codex/gpt-6-astra",
      "codex/gpt-5.6-sol",
      "codex/gpt-5.6-terra",
      "codex/gpt-5.6-luna",
      "codex/gpt-6-luna",
      "codex/gpt-6-sol",
      "codex/gpt-5.10-mini",
      "neuraldeep/qwen3.8-27b",
      "neuraldeep/gpt-oss-120b",
    ],
    model: "codex/gpt-6-astra",
  });
  openModelMenu();
  const menu = screen.getByRole("dialog", { name: /model/i });
  const rows = within(menu)
    .getAllByRole("option")
    .map((el) => el.textContent);
  expect(rows).toEqual([
    "gpt-5.6-luna",
    "gpt-5.6-sol",
    "gpt-5.6-terra",
    "gpt-5.10-mini",
    "gpt-6-astra",
    "gpt-6-luna",
    "gpt-6-sol",
    "gpt-oss-120b",
    "qwen3.8-27b",
  ]);
  const labels = within(menu)
    .getAllByText(/^(codex|neuraldeep)$/)
    .map((el) => el.textContent);
  expect(labels).toEqual(["codex", "neuraldeep"]);
});

// Enter takes the first row the reader sees, so the order is the menu's order.
test("Enter in the filter picks the first model as the menu orders it", () => {
  const onChange = vi.fn();
  renderModelMenu({
    models: [
      "codex/gpt-6-sol",
      "codex/gpt-6-astra",
      "codex/gpt-5.6-luna",
      "stub/a",
      "stub/b",
      "stub/c",
    ],
    model: "stub/a",
    onChange,
  });
  openModelMenu();
  const filter = screen.getByTestId("model-menu-filter");
  fireEvent.change(filter, { target: { value: "gpt-6" } });
  fireEvent.keyDown(filter, { key: "Enter" });
  expect(onChange).toHaveBeenCalledWith("codex/gpt-6-astra");
});

function renderPicker(models: string[] = [], model = models[0] ?? "") {
  const onModelChange = vi.fn();
  const onModeChange = vi.fn();
  function Harness() {
    const [value, setValue] = useState("");
    const [mode, setMode] = useState("plan");
    const [llmModel, setModel] = useState(model);
    return (
      <Composer
        value={value}
        isEmpty={false}
        mode={mode}
        modes={["agent", "plan", "ask"]}
        llmModels={models}
        llmModel={llmModel}
        onLlmModelChange={(id) => {
          setModel(id);
          onModelChange(id);
        }}
        onModeChange={(id) => {
          setMode(id);
          onModeChange(id);
        }}
        onChange={setValue}
        onSend={() => {}}
      />
    );
  }
  render(<Harness />);
  return { onModeChange, onModelChange };
}

function pressKey(key: string) {
  const target = document.activeElement!;
  const uncancelled = fireEvent.keyDown(target, { key });
  // jsdom does not perform the native Enter activation of a focused button.
  if (uncancelled && key === "Enter" && target instanceof HTMLButtonElement) {
    fireEvent.click(target);
  }
}

function modeChip(value: string) {
  return screen.getByRole("button", {
    name: new RegExp(`^${value}\\b.*\\bMode\\b`, "i"),
  });
}

function stubModelCommand() {
  // Use the slash picker sheet, which does not depend on jsdom layout.
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: true,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }));
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => ({
      ok: true,
      json: async () =>
        url.includes("/coddy/commands")
          ? {
              object: "coddy.commands",
              items: [
                {
                  name: "model",
                  description: "Choose a model",
                  kind: "setting",
                },
              ],
            }
          : { items: [], has_more: false, page: 1 },
    })),
  );
}

async function openModelFromSlash() {
  const message = screen.getByRole<HTMLTextAreaElement>("textbox", {
    name: "Message",
  });
  act(() => message.focus());
  fireEvent.change(message, {
    target: { value: "/mo", selectionStart: 3, selectionEnd: 3 },
  });
  await screen.findByTestId("command-row-model");
  pressKey("Enter");
  expect(message.value).toBe("");
  expect(screen.queryByTestId("command-row-model")).toBeNull();
  return message;
}

describe("composer picker accessibility", () => {
  test("Mode is a radio menu with keyboard navigation", () => {
    renderPicker();
    fireEvent.click(modeChip("Plan"));
    const menu = within(screen.getByRole("menu"));
    const first = menu.getByRole("menuitemradio", {
      name: "Agent",
      checked: false,
    });
    const current = menu.getByRole("menuitemradio", {
      name: "Plan",
      checked: true,
    });
    const last = menu.getByRole("menuitemradio", {
      name: "Ask",
      checked: false,
    });
    expect(document.activeElement).toBe(first);
    pressKey("ArrowDown");
    expect(document.activeElement).toBe(current);
    pressKey("ArrowUp");
    expect(document.activeElement).toBe(first);
    pressKey("End");
    expect(document.activeElement).toBe(last);
    pressKey("Home");
    expect(document.activeElement).toBe(first);
  });

  test("Mode selection and Escape return focus to the chip", () => {
    const { onModeChange } = renderPicker();
    fireEvent.click(modeChip("Plan"));
    pressKey("End");
    pressKey("Enter");
    expect(onModeChange.mock.calls).toEqual([["ask"]]);
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(modeChip("Ask"));

    fireEvent.click(modeChip("Ask"));
    fireEvent.click(
      within(screen.getByRole("menu")).getByRole("menuitemradio", {
        name: "Agent",
      }),
    );
    expect(onModeChange.mock.calls).toEqual([["ask"], ["agent"]]);
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(modeChip("Agent"));

    fireEvent.click(modeChip("Agent"));
    pressKey("ArrowDown");
    pressKey("Escape");
    expect(onModeChange).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(modeChip("Agent"));
  });

  test("/model opens a radio menu for a short list and returns to the message field", async () => {
    stubModelCommand();
    const { onModelChange } = renderPicker(
      ["stub/alpha", "stub/beta"],
      "stub/beta",
    );
    const message = await openModelFromSlash();
    const menu = within(screen.getByRole("menu"));
    expect(
      menu.getByRole("menuitemradio", { name: "beta", checked: true }),
    ).toBeTruthy();
    expect(document.activeElement).toBe(
      menu.getByRole("menuitemradio", { name: "alpha", checked: false }),
    );
    pressKey("Escape");
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(message);
    expect(onModelChange).not.toHaveBeenCalled();

    await openModelFromSlash();
    fireEvent.click(
      within(screen.getByRole("menu")).getByRole("menuitemradio", {
        name: "alpha",
      }),
    );
    expect(onModelChange.mock.calls).toEqual([["stub/alpha"]]);
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(message);
  });

  test("/model opens a filter dialog for a long list and returns to the message field", async () => {
    stubModelCommand();
    const { onModelChange } = renderPicker(MANY_MODELS);
    const message = await openModelFromSlash();
    const dialog = () => within(screen.getByRole("dialog", { name: /model/i }));
    const filter = () => dialog().getByLabelText("Filter models");
    await waitFor(() => expect(document.activeElement).toBe(filter()));
    pressKey("Escape");
    expect(screen.queryByRole("dialog", { name: /model/i })).toBeNull();
    expect(document.activeElement).toBe(message);
    expect(onModelChange).not.toHaveBeenCalled();

    await openModelFromSlash();
    fireEvent.change(filter(), { target: { value: "5.4" } });
    const results = within(dialog().getByRole("listbox"));
    const first = results.getByRole("option", { name: "gpt-5.4" });
    expect(results.getAllByRole("option")).toHaveLength(2);
    pressKey("ArrowDown");
    expect(document.activeElement).toBe(first);
    pressKey("Enter");
    expect(onModelChange.mock.calls).toEqual([["cliproxyapi/gpt-5.4"]]);
    expect(screen.queryByRole("dialog", { name: /model/i })).toBeNull();
    expect(document.activeElement).toBe(message);
  });
});

test("identically named backend choices retain their provider in the accessible name", () => {
  renderModelMenu({
    models: ["acme/alpha", "another/alpha"],
    model: "another/alpha",
  });
  const chip = screen.getByRole("button", {
    name: "alpha (another/alpha), Model",
  });
  expect(chip).toHaveTextContent(/^alpha$/);
  fireEvent.click(chip);
  expect(
    screen.getByRole("menuitemradio", {
      name: "alpha (another/alpha)",
      checked: true,
    }),
  ).toBeTruthy();
  expect(
    screen.getByRole("menuitemradio", {
      name: "alpha (acme/alpha)",
      checked: false,
    }),
  ).toBeTruthy();
});

test("removing a live setting dismisses its open menu and returns to the composer", () => {
  const composer = (hasTier: boolean) => (
    <Composer
      value="draft"
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
      serviceTierControl={
        hasTier
          ? {
              value: "priority",
              choices: ["default", "priority"],
              onChange: () => {},
            }
          : undefined
      }
    />
  );
  const { rerender } = render(composer(true));
  fireEvent.click(screen.getByRole("button", { name: "Fast, Service tier" }));
  expect(
    screen.getByRole("menuitemradio", { name: "Fast", checked: true }),
  ).toBeInTheDocument();
  rerender(composer(false));
  expect(screen.queryByRole("menu")).toBeNull();
  expect(screen.getByRole("textbox", { name: "Message" })).toHaveFocus();
});

test("the model filter leaves composing keys to the IME and outside dismissal restores focus", () => {
  const onChange = vi.fn();
  renderModelMenu({ onChange });
  const chip = screen.getByRole("button", { name: /, Model$/ });
  fireEvent.click(chip);
  const filter = screen.getByRole("textbox", { name: "Filter models" });
  for (const key of ["ArrowDown", "Enter", "Escape"]) {
    fireEvent.keyDown(filter, { key, isComposing: true });
    expect(filter).toHaveFocus();
  }
  fireEvent.mouseDown(document.querySelector(".mode-menu-backdrop")!);
  expect(screen.queryByRole("dialog", { name: "Model" })).toBeNull();
  expect(chip).toHaveFocus();
  expect(onChange).not.toHaveBeenCalled();
});

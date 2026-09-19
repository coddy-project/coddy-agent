import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { Composer } from "./Composer";

afterEach(() => cleanup());

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
  fireEvent.click(screen.getByRole("button", { name: "Model" }));
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
  const menu = screen.getByRole("menu");
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
    const menu = screen.getByRole("menu");
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
  expect(screen.getByRole("menuitem", { name: "gemini-2.5-pro" })).toBeTruthy();
  expect(
    screen.queryByRole("menuitem", { name: "deepseek-v4-pro" }),
  ).toBeNull();
  // The non-matching vendor header is gone too.
  expect(screen.queryByText("opencode-go")).toBeNull();
});

test("filtering by vendor keeps that vendor's rows", () => {
  renderModelMenu({});
  openModelMenu();
  fireEvent.change(screen.getByTestId("model-menu-filter"), {
    target: { value: "cliproxyapi" },
  });
  const menu = screen.getByRole("menu");
  expect(within(menu).getByRole("menuitem", { name: "gpt-5.5" })).toBeTruthy();
  expect(within(menu).queryByRole("menuitem", { name: "glm-5" })).toBeNull();
});

test("selecting a filtered model calls onLlmModelChange with the full id", () => {
  const onChange = vi.fn();
  renderModelMenu({ onChange });
  openModelMenu();
  fireEvent.change(screen.getByTestId("model-menu-filter"), {
    target: { value: "5.4-mini" },
  });
  fireEvent.click(screen.getByRole("menuitem", { name: "gpt-5.4-mini" }));
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

import React, { useState } from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { Composer } from "./Composer";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const MODELS = ["openai/gpt-4o", "hub/qwen3-coder", "hub/qwen3-mini"];

// The floating variant is placed from getBoundingClientRect, which jsdom does
// not lay out, so the picker is tested as the bottom sheet of the stacked shell.
function stubStackedShell() {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: true,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
    onchange: null,
  }));
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve({ ok: false, status: 404, json: async () => ({}) }),
    ),
  );
}

function Harness(props: {
  onChange: (v: string) => void;
  onSend?: () => void;
  models?: string[];
}) {
  const [value, setValue] = useState("");
  return (
    <Composer
      value={value}
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModels={props.models ?? MODELS}
      llmModel="openai/gpt-4o"
      onLlmModelChange={() => {}}
      onModeChange={() => {}}
      onChange={(v) => {
        setValue(v);
        props.onChange(v);
      }}
      onSend={props.onSend ?? (() => {})}
    />
  );
}

function typeDraft(ta: HTMLElement, value: string) {
  fireEvent.change(ta, {
    target: { value, selectionStart: value.length, selectionEnd: value.length },
  });
}

test("/compact --model lists the configured models and narrows as the id is typed", async () => {
  stubStackedShell();
  render(<Harness onChange={() => {}} />);
  const ta = screen.getByRole("textbox", { name: "Message" });

  typeDraft(ta, "/compact --model ");
  await waitFor(() => {
    expect(screen.getByTestId("command-arg-menu")).toBeTruthy();
  });
  for (const id of ["openai_gpt-4o", "hub_qwen3-coder", "hub_qwen3-mini"]) {
    expect(screen.getByTestId(`command-arg-row-${id}`)).toBeTruthy();
  }

  typeDraft(ta, "/compact --model QW");
  await waitFor(() => {
    expect(screen.queryByTestId("command-arg-row-openai_gpt-4o")).toBeNull();
  });
  expect(screen.getByTestId("command-arg-row-hub_qwen3-coder")).toBeTruthy();
  expect(screen.getByTestId("command-arg-row-hub_qwen3-mini")).toBeTruthy();
});

test("arrows move the highlight and Enter puts the model into the draft instead of sending", async () => {
  stubStackedShell();
  const onChange = vi.fn();
  const onSend = vi.fn();
  render(<Harness onChange={onChange} onSend={onSend} />);
  const ta = screen.getByRole("textbox", { name: "Message" });

  typeDraft(ta, "/compact --model qw");
  await waitFor(() => {
    expect(screen.getByTestId("command-arg-row-hub_qwen3-coder")).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });
  fireEvent.keyDown(ta, { key: "ArrowDown" });
  await waitFor(() => {
    expect(screen.getByTestId("command-arg-row-hub_qwen3-mini")).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });
  fireEvent.keyDown(ta, { key: "Enter" });

  expect(onChange).toHaveBeenLastCalledWith("/compact --model hub/qwen3-mini ");
  expect(onSend).not.toHaveBeenCalled();
  await waitFor(() => {
    expect(screen.queryByTestId("command-arg-menu")).toBeNull();
  });
});

test("a dash offers --model, and picking it opens the models", async () => {
  stubStackedShell();
  const onChange = vi.fn();
  render(<Harness onChange={onChange} />);
  const ta = screen.getByRole("textbox", { name: "Message" });

  typeDraft(ta, "/compact --");
  await waitFor(() => {
    expect(screen.getByTestId("command-arg-row---model")).toBeTruthy();
  });
  fireEvent.keyDown(ta, { key: "Tab" });
  expect(onChange).toHaveBeenLastCalledWith("/compact --model ");
});

test("Escape closes the list and leaves the draft alone", async () => {
  stubStackedShell();
  const onChange = vi.fn();
  render(<Harness onChange={onChange} />);
  const ta = screen.getByRole("textbox", { name: "Message" });

  typeDraft(ta, "/compact --model qw");
  await waitFor(() => {
    expect(screen.getByTestId("command-arg-menu")).toBeTruthy();
  });
  fireEvent.keyDown(ta, { key: "Escape" });
  await waitFor(() => {
    expect(screen.queryByTestId("command-arg-menu")).toBeNull();
  });
  expect(onChange).toHaveBeenLastCalledWith("/compact --model qw");
});

test("a bare /compact and its instructions open nothing", async () => {
  stubStackedShell();
  render(<Harness onChange={() => {}} />);
  const ta = screen.getByRole("textbox", { name: "Message" });

  for (const draft of ["/compact ", "/compact keep the --model "]) {
    typeDraft(ta, draft);
    // Let any picker effect settle before asserting that none opened.
    await Promise.resolve();
    expect(screen.queryByTestId("command-arg-menu")).toBeNull();
  }
});

test("says so when no configured model matches", async () => {
  stubStackedShell();
  render(<Harness onChange={() => {}} />);
  const ta = screen.getByRole("textbox", { name: "Message" });

  typeDraft(ta, "/compact --model claude");
  await waitFor(() => {
    expect(screen.getByTestId("command-arg-menu")).toHaveTextContent(
      "No models match",
    );
  });
});

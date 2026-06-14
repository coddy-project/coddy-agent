import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Composer } from "./Composer";

afterEach(() => cleanup());

function renderComposerWithReasoning(opts: {
  levels?: string[];
  reasoning?: string;
  onChange?: (level: string) => void;
}) {
  return render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModels={["openai/gpt-5"]}
      llmModel="openai/gpt-5"
      onLlmModelChange={() => {}}
      llmReasoningLevels={opts.levels ?? ["minimal", "low", "medium", "high"]}
      llmReasoning={opts.reasoning ?? "medium"}
      onLlmReasoningChange={opts.onChange ?? (() => {})}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
}

test("reasoning selector shows the current level and opens its menu", () => {
  renderComposerWithReasoning({ reasoning: "high" });
  const btn = screen.getByRole("button", { name: "Reasoning level" });
  expect(btn).toHaveTextContent("High");
  fireEvent.click(btn);
  // All offered levels are listed.
  expect(screen.getByRole("menuitem", { name: "Minimal" })).toBeTruthy();
  expect(screen.getByRole("menuitem", { name: "High" })).toBeTruthy();
});

test("choosing a level calls onLlmReasoningChange", () => {
  const onChange = vi.fn();
  renderComposerWithReasoning({ reasoning: "medium", onChange });
  fireEvent.click(screen.getByRole("button", { name: "Reasoning level" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "Low" }));
  expect(onChange).toHaveBeenCalledWith("low");
});

test("reasoning selector is hidden when no levels offered", () => {
  render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModels={["openai/gpt-4o"]}
      llmModel="openai/gpt-4o"
      onLlmModelChange={() => {}}
      llmReasoningLevels={[]}
      onLlmReasoningChange={() => {}}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  expect(screen.queryByRole("button", { name: "Reasoning level" })).toBeNull();
});

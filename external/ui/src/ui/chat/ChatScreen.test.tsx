import React from "react";
import { afterEach, expect, test } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { ChatScreen } from "./ChatScreen";

afterEach(() => cleanup());

test("empty hero shows headline with accent span", () => {
  const { getByTestId, getByRole } = render(
    <ChatScreen
      title=""
      sessionId=""
      heroAccentVerb="know"
      heroComposerFocusEpoch={0}
      onTitleSave={() => {}}
      items={[]}
      draft=""
      tokenUsage={null}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onDraftChange={() => {}}
      onSend={() => {}}
    />,
  );

  expect(getByRole("heading", { level: 1 })).toHaveTextContent(
    "What do you want to know?",
  );
  expect(getByTestId("hero-title-accent")).toHaveTextContent("know");
  expect(getByRole("textbox")).toHaveFocus();
});

test("active chat wraps title in chat-title-column aligned with composer column", () => {
  const { container } = render(
    <ChatScreen
      title="Hi"
      sessionId="s1"
      heroAccentVerb="know"
      heroComposerFocusEpoch={0}
      onTitleSave={() => {}}
      items={[{ type: "user_message", id: "1", content: "x" }]}
      draft=""
      tokenUsage={null}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onDraftChange={() => {}}
      onSend={() => {}}
    />,
  );

  const col = container.querySelector(".chat-title-column");
  expect(col).toBeTruthy();
  expect(col?.querySelector(".chat-header")).toBeTruthy();
});

test("disabled attachments survive the empty-to-active composer transition", async () => {
  const common = {
    title: "",
    sessionId: "",
    heroAccentVerb: "know" as const,
    heroComposerFocusEpoch: 0,
    onTitleSave: () => {},
    draft: "",
    tokenUsage: null,
    mode: "agent",
    modes: ["agent", "plan"],
    llmModels: ["openai/vision", "openai/text"],
    llmModel: "openai/vision",
    onLlmModelChange: () => {},
    onModeChange: () => {},
    onDraftChange: () => {},
    onSend: () => {},
  };
  const { rerender } = render(
    <ChatScreen {...common} items={[]} llmModelMultimodal={true} />,
  );
  fireEvent.change(screen.getByTestId("composer-file-input"), {
    target: {
      files: [new File(["img"], "photo.png", { type: "image/png" })],
    },
  });
  await waitFor(() => screen.getByText("photo.png"));

  rerender(
    <ChatScreen
      {...common}
      sessionId="s1"
      items={[{ type: "user_message", id: "1", content: "hello" }]}
      llmModel="openai/text"
      llmModelMultimodal={false}
    />,
  );

  expect(
    screen.getByText("photo.png").closest(".composer-attachment-chip"),
  ).toHaveClass("composer-attachment-chip--disabled");
});

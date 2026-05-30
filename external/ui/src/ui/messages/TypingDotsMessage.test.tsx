import React from "react";
import { afterEach, expect, test } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { TypingDotsMessage } from "./TypingDotsMessage";
import { MessageList } from "./MessageList";
import type { TranscriptItem } from "../chat/types";

afterEach(() => cleanup());

test("renders three dots when generating", () => {
  render(<TypingDotsMessage />);
  const dots = document.querySelectorAll(".typing-dots-dot");
  expect(dots.length).toBe(3);
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
});

test("MessageList shows typing dots when generating and no streaming assistant", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hello" },
  ];
  render(<MessageList items={items} generating={true} />);
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
});

test("MessageList hides typing dots when not generating", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hello" },
  ];
  render(<MessageList items={items} generating={false} />);
  expect(screen.queryByTestId("typing-dots")).toBeNull();
});

test("MessageList hides typing dots when streaming assistant message is present", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Hello" },
    { id: "a1", type: "assistant_message", content: "Hi th", streaming: true },
  ];
  render(<MessageList items={items} generating={true} />);
  expect(screen.queryByTestId("typing-dots")).toBeNull();
});

test("MessageList shows typing dots when generating with tool call in progress", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Do something" },
    {
      id: "t1",
      type: "tool_call",
      toolCallId: "call_1",
      title: "read_file",
      kind: "read",
      status: "in_progress",
    },
  ];
  render(<MessageList items={items} generating={true} />);
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
});

test("MessageList shows typing dots when generating between tool calls (no streaming text)", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "Go" },
    {
      id: "t1",
      type: "tool_call",
      toolCallId: "call_1",
      title: "read_file",
      kind: "read",
      status: "completed",
      resultText: "content",
    },
  ];
  render(<MessageList items={items} generating={true} />);
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
});

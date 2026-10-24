import React from "react";
import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { MessageList } from "./MessageList";
import type { TranscriptItem } from "../chat/types";

afterEach(cleanup);

const items: TranscriptItem[] = [
  { type: "user_message", id: "u1", content: "First prompt" },
  { type: "assistant_message", id: "a1", content: "An answer" },
  { type: "user_message", id: "u2", content: "Follow-up prompt" },
];

test("read-only follow-ups keep their spacing without enabling editing", () => {
  const { container } = render(<MessageList items={items} />);
  const prompts = container.querySelectorAll(".msg-user-stack");
  expect(prompts[0]).not.toHaveClass("msg-user-stack--followup");
  expect(prompts[1]).toHaveClass("msg-user-stack--followup");
  expect(
    container.querySelector('[data-testid="user-message-edit"]'),
  ).toBeNull();
});

test("a read-only window keeps the global follow-up spacing without edit controls", () => {
  const { container } = render(
    <MessageList items={items.slice(2)} userMsgIndexBase={37} />,
  );
  expect(container.querySelector(".msg-user-stack")).toHaveClass(
    "msg-user-stack--followup",
  );
  expect(
    container.querySelector('[data-testid="user-message-edit"]'),
  ).toBeNull();
});

test("a render slice does not turn a read-only follow-up into the first prompt", () => {
  const { container } = render(<MessageList items={items} renderStart={2} />);
  expect(container.querySelectorAll(".msg-user-stack")).toHaveLength(1);
  expect(container.querySelector(".msg-user-stack")).toHaveClass(
    "msg-user-stack--followup",
  );
});

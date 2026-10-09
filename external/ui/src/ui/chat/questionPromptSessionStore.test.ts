import { expect, test } from "vitest";

import {
  hasUnresolvedQuestionPrompt,
  mergeStoredQuestionPromptsIntoTranscript,
  pickRicherQuestionToolArgs,
} from "./questionPromptSessionStore";
import type { CoddyQuestionPayload } from "./questionTypes";
import type { TranscriptItem } from "./types";

test("pickRicherQuestionToolArgs prefers the JSON with more parsed questions", () => {
  const full = JSON.stringify({
    questions: [
      { question: "A?", options: [{ label: "1" }] },
      { question: "B?", options: [{ label: "2" }] },
    ],
  });
  const truncated = '{"questions":[]}';
  expect(pickRicherQuestionToolArgs(full, truncated)).toBe(full);
  expect(pickRicherQuestionToolArgs(truncated, full)).toBe(full);
});

test("mergeStoredQuestionPromptsIntoTranscript inserts after matching tool_call", () => {
  const payload: CoddyQuestionPayload = {
    sessionId: "sess_x",
    requestId: "q_123",
    toolCallId: "call_abc",
    questions: [{ question: "Hi?", options: [{ label: "Yes" }] }],
  };

  try {
    window.localStorage.setItem(
      "coddy_qp_v1:sess_x",
      JSON.stringify([
        {
          requestId: "q_123",
          payload,
          resolved: {
            skipped: false,
            answers: [["Yes"]],
            summaryLine: "Hi? Yes",
          },
        },
      ]),
    );
  } catch {
    return;
  }

  const merged: TranscriptItem[] = [
    {
      id: "t1",
      type: "tool_call",
      toolCallId: "call_abc",
      title: "question",
      status: "completed",
      argsText: "{}",
      resultText: "{}",
    },
  ];

  const out = mergeStoredQuestionPromptsIntoTranscript(merged, "sess_x");
  expect(out.findIndex((x) => x.type === "question_prompt")).toBe(1);
  const qp = out[1];
  expect(qp?.type === "question_prompt" ? qp.resolved?.summaryLine : "").toBe(
    "Hi? Yes",
  );
  try {
    window.localStorage.removeItem("coddy_qp_v1:sess_x");
  } catch {
    //
  }
});

test("reconstructed pending question clears its marker after settlement", () => {
  const payload: CoddyQuestionPayload = {
    sessionId: "sess_x",
    requestId: "q_pending",
    toolCallId: "call_pending",
    questions: [{ question: "Continue?", options: [{ label: "Yes" }] }],
  };
  const merged: TranscriptItem[] = [
    {
      id: "tc_pending",
      type: "tool_call",
      toolCallId: "call_pending",
      title: "question",
      status: "completed",
    },
  ];

  try {
    window.localStorage.setItem(
      "coddy_qp_v1:sess_x",
      JSON.stringify([{ requestId: "q_pending", payload }]),
    );
    const pending = mergeStoredQuestionPromptsIntoTranscript(merged, "sess_x");
    expect(hasUnresolvedQuestionPrompt(pending)).toBe(true);

    window.localStorage.setItem(
      "coddy_qp_v1:sess_x",
      JSON.stringify([
        {
          requestId: "q_pending",
          payload,
          resolved: {
            skipped: false,
            answers: [["Yes"]],
            summaryLine: "Continue? Yes",
          },
        },
      ]),
    );
    const settled = mergeStoredQuestionPromptsIntoTranscript(merged, "sess_x");
    expect(hasUnresolvedQuestionPrompt(settled)).toBe(false);
  } finally {
    try {
      window.localStorage.removeItem("coddy_qp_v1:sess_x");
    } catch {
      // Storage can be unavailable in privacy mode.
    }
  }
});

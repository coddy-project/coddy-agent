import { describe, expect, it } from "vitest";
import type { CoddyQuestionPayload } from "./questionTypes";
import type { TranscriptItem } from "./types";
import { reattachLocalQuestionPrompts } from "./transcriptQuestionReattach";

function toolRow(
  toolCallId: string,
): Extract<TranscriptItem, { type: "tool_call" }> {
  return {
    id: `t_${toolCallId}`,
    type: "tool_call",
    toolCallId,
    status: "completed",
  };
}

function qp(
  rid: string,
  toolCallId?: string,
): Extract<TranscriptItem, { type: "question_prompt" }> {
  const payload: CoddyQuestionPayload = {
    sessionId: "s",
    requestId: rid,
    ...(toolCallId !== undefined ? { toolCallId } : {}),
    questions: [{ question: "Sample?", options: [{ label: "ok" }] }],
  };
  return { id: `qp_${rid}`, type: "question_prompt", payload };
}

describe("reattachLocalQuestionPrompts", () => {
  it("inserts local question_prompt after its tool_call when merge dropped it", () => {
    const toolId = "call_q1";
    const merged: TranscriptItem[] = [
      toolRow(toolId),
      {
        id: "a",
        type: "assistant_message",
        content: "Hello",
      },
    ];
    const local: TranscriptItem[] = [merged[0]!, qp("r42", toolId)];
    const out = reattachLocalQuestionPrompts(merged, local);
    expect(out.length).toBe(merged.length + 1);
    expect(out.findIndex((x) => x.type === "question_prompt")).toBe(1);
    const qRow = out[1];
    expect(qRow?.type === "question_prompt" ? qRow.payload.requestId : "").toBe(
      "r42",
    );
  });

  it("does not duplicate when merged already carries the prompt", () => {
    const toolId = "call_dup";
    const prompt = qp("r9", toolId);
    const merged: TranscriptItem[] = [
      toolRow(toolId),
      prompt,
      { id: "a", type: "assistant_message", content: "Done" },
    ];
    const out = reattachLocalQuestionPrompts(merged, [...merged]);
    expect(out.filter((x) => x.type === "question_prompt").length).toBe(1);
  });
});

import type { TranscriptItem } from "./types";

/** Local-only rows for interactive prompts (SSE + POST /question). Persisted transcripts omit them. */
function isQuestionPrompt(
  x: TranscriptItem,
): x is Extract<TranscriptItem, { type: "question_prompt" }> {
  return x.type === "question_prompt";
}

/**
 * Re-insert question_prompt rows from the client shadow after merging server messages.
 * The HTTP messages list contains no question_prompt rows, so naive prefix merge strips them
 * and breaks the transcript when a tool row aligns but the following local row differs.
 */
export function reattachLocalQuestionPrompts(
  merged: TranscriptItem[],
  local: TranscriptItem[] | undefined,
): TranscriptItem[] {
  if (!local?.length) {
    return merged;
  }
  const extras = local.filter(isQuestionPrompt);
  if (extras.length === 0) {
    return merged;
  }
  const have = new Set(
    merged.filter(isQuestionPrompt).map((x) => x.payload.requestId.trim()),
  );
  let out = [...merged];
  for (const q of extras) {
    const rid = q.payload.requestId.trim();
    if (!rid || have.has(rid)) {
      continue;
    }
    have.add(rid);
    const tcid = q.payload.toolCallId?.trim();
    let insertAt = out.length;
    if (tcid) {
      const idx = out.findIndex(
        (x): x is Extract<TranscriptItem, { type: "tool_call" }> =>
          x.type === "tool_call" && x.toolCallId === tcid,
      );
      if (idx >= 0) {
        insertAt = idx + 1;
      }
    }
    out.splice(insertAt, 0, q);
  }
  return out;
}


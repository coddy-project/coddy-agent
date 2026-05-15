/**
 * Payload for interactive question tool (matches server SSE and POST /question).
 */
export type CoddyQuestionOption = { label: string; description?: string };

export type CoddyQuestionItem = {
  header?: string;
  question: string;
  options: CoddyQuestionOption[];
  multiple?: boolean;
  custom?: boolean;
};

export type CoddyQuestionPayload = {
  sessionId: string;
  requestId: string;
  toolCallId?: string;
  questions: CoddyQuestionItem[];
};

/** Stored on the transcript row after POST /question succeeds. */
export type QuestionResolvedState = {
  skipped: boolean;
  /** Answers per question (same shape as POST body). */
  answers: string[][];
  /** Single-line label for the collapsed header. */
  summaryLine: string;
};

export function letterForOptionIndex(i: number): string {
  if (i >= 0 && i < 26) {
    return String.fromCharCode(65 + i);
  }
  return String(i + 1);
}

function normQuestions(raw: unknown): CoddyQuestionItem[] {
  if (!Array.isArray(raw)) return [];
  const out: CoddyQuestionItem[] = [];
  for (const q of raw) {
    if (!q || typeof q !== "object") continue;
    const o = q as Record<string, unknown>;
    const question = typeof o.question === "string" ? o.question.trim() : "";
    const optsRaw = o.options;
    const options: CoddyQuestionOption[] = [];
    if (Array.isArray(optsRaw)) {
      for (const op of optsRaw) {
        if (!op || typeof op !== "object") continue;
        const oo = op as Record<string, unknown>;
        const label = typeof oo.label === "string" ? oo.label.trim() : "";
        if (!label) continue;
        options.push({
          label,
          description:
            typeof oo.description === "string" ? oo.description : undefined,
        });
      }
    }
    if (!question || options.length === 0) continue;
    out.push({
      header: typeof o.header === "string" ? o.header : undefined,
      question,
      options,
      multiple: o.multiple === true,
      custom: o.custom === true,
    });
  }
  return out;
}

export function parseCoddyQuestionPayload(
  raw: Record<string, unknown>,
): CoddyQuestionPayload | null {
  const sessionId = String(raw.sessionId || "").trim();
  const requestId = String(raw.requestId || "").trim();
  const questions = normQuestions(raw.questions);
  if (!sessionId || !requestId || questions.length === 0) {
    return null;
  }
  return {
    sessionId,
    requestId,
    toolCallId:
      typeof raw.toolCallId === "string" && raw.toolCallId.trim() !== ""
        ? raw.toolCallId.trim()
        : undefined,
    questions,
  };
}

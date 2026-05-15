/**
 * Parses tool args / results for the `question` tool so the SPA can render
 * human-readable copy instead of raw JSON in ToolCallMessage.
 */

export type QuestionToolArgItem = {
  question: string;
};

function trimQ(s: string): string {
  return s.replace(/\s+/g, " ").trim();
}

export function parseQuestionToolQuestionsFromArgs(
  argsText: string | undefined,
): QuestionToolArgItem[] {
  if (!argsText || !argsText.trim()) return [];
  try {
    const v = JSON.parse(argsText) as { questions?: unknown };
    const raw = v.questions;
    if (!Array.isArray(raw)) return [];
    const out: QuestionToolArgItem[] = [];
    for (const item of raw) {
      if (!item || typeof item !== "object") continue;
      const q = trimQ(String((item as { question?: unknown }).question ?? ""));
      if (q) out.push({ question: q });
    }
    return out;
  } catch {
    return [];
  }
}

export function parseQuestionToolAnswersFromResult(
  resultText: string | undefined,
): string[][] {
  if (!resultText || !resultText.trim()) return [];
  try {
    const v = JSON.parse(resultText) as { answers?: unknown };
    const raw = v.answers;
    if (!Array.isArray(raw)) return [];
    const out: string[][] = [];
    for (const row of raw) {
      if (!Array.isArray(row)) {
        out.push([]);
        continue;
      }
      const cells = row
        .map((x) => trimQ(String(x ?? "")))
        .filter((s) => s.length > 0);
      out.push(cells);
    }
    return out;
  } catch {
    return [];
  }
}

/** One-line summary for the tool_call summary row (replaces literal "question"). */
export function questionToolSummaryLabel(args: {
  argsText?: string | undefined;
  resultText?: string | undefined;
  pendingLike: boolean;
  terminal: boolean;
}): string {
  const qs = parseQuestionToolQuestionsFromArgs(args.argsText);
  const answers = parseQuestionToolAnswersFromResult(args.resultText);

  if (qs.length === 0) {
    return args.pendingLike ? "Questions..." : "Questions";
  }

  function shorten(text: string, max: number): string {
    const t = trimQ(text);
    if (t.length <= max) return t;
    return `${t.slice(0, Math.max(0, max - 3))}...`;
  }

  if (args.terminal && answers.length > 0 && qs.length > 0) {
    const parts: string[] = [];
    const n = Math.max(qs.length, answers.length);
    for (let i = 0; i < n; i++) {
      const q = qs[i]?.question ?? "";
      const a = answers[i]?.join(", ") ?? "";
      if (!q && !a) continue;
      if (!q) {
        parts.push(shorten(a, 72));
      } else if (!a) {
        parts.push(shorten(`${q}?`, 88));
      } else {
        const qDisp = shorten(q.replace(/\?$/, "").trim(), 72);
        parts.push(`${qDisp}? ${shorten(a, 40)}`);
      }
    }
    if (parts.length > 0) {
      return parts.join(" · ");
    }
  }

  if (qs.length > 1) {
    return `${qs.length} questions`;
  }

  const t = shorten(qs[0]!.question.replace(/\?$/, "").trim(), 72);
  return `${t}?`;
}

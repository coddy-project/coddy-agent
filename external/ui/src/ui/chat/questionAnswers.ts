import type {
  CoddyQuestionItem,
  CoddyQuestionPayload,
} from "./questionTypes";
import { letterForOptionIndex } from "./questionTypes";
import { t as translate } from "../i18n/i18n";

export const OTHER_SENTINEL = "__coddy_other__";

export function buildAnswerRows(
  payload: CoddyQuestionPayload,
  multiSel: string[][],
  singleSel: string[],
  extraText: string[],
  skipped: boolean,
): string[][] {
  if (skipped) return payload.questions.map(() => []);
  const out: string[][] = [];
  for (let qi = 0; qi < payload.questions.length; qi++) {
    const q = payload.questions[qi];
    if (!q) {
      out.push([]);
      continue;
    }
    const cells: string[] = [];
    if (q.multiple) {
      const sel = multiSel[qi] || [];
      const wantsFree = sel.includes(OTHER_SENTINEL);
      for (const lab of sel) {
        if (lab !== OTHER_SENTINEL) cells.push(lab);
      }
      const ex = String(extraText[qi] ?? "").trim();
      if (wantsFree && ex.length > 0) cells.push(ex);
    } else {
      const sel = String(singleSel[qi] ?? "").trim();
      const ex = String(extraText[qi] ?? "").trim();
      if (sel === OTHER_SENTINEL) {
        if (ex.length > 0) cells.push(ex);
      } else if (sel) {
        cells.push(sel);
      }
    }
    out.push(cells);
  }
  return out;
}

export function allAnsweredNonEmpty(rows: string[][]): boolean {
  return rows.every((r) =>
    [...r].map((x) => String(x).trim()).some((x) => x.length > 0),
  );
}

export function readyToSubmit(args: {
  questions: CoddyQuestionItem[];
  multiSel: string[][];
  singleSel: string[];
  extraText: string[];
}): boolean {
  const minimalPayload: CoddyQuestionPayload = {
    sessionId: "",
    requestId: "",
    questions: args.questions,
  };

  const rows = buildAnswerRows(
    minimalPayload,
    args.multiSel,
    args.singleSel,
    args.extraText,
    false,
  );

  const n = args.questions.length;
  for (let qi = 0; qi < n; qi++) {
    const q = args.questions[qi];
    if (!q) return false;

    if (q.multiple) {
      const sel = args.multiSel[qi] || [];
      const wantsFree = sel.includes(OTHER_SENTINEL);
      const picks = sel.filter((l) => l !== OTHER_SENTINEL);
      const ex = String(args.extraText[qi] ?? "").trim();
      if (wantsFree && ex.length === 0) return false;
      const ok = picks.length > 0 || (wantsFree && ex.length > 0);
      if (!ok) return false;
    } else {
      const pick = String(args.singleSel[qi] ?? "").trim();
      if (pick === OTHER_SENTINEL) {
        if (String(args.extraText[qi] ?? "").trim().length === 0) {
          return false;
        }
      }
    }
  }

  return allAnsweredNonEmpty(rows);
}

export function formatResolvedSummaryLine(
  questions: CoddyQuestionItem[],
  skipped: boolean,
  answersMatrix: string[][],
): string {
  if (!questions.length || skipped) {
    return translate("prompts.skipped");
  }
  const parts: string[] = [];
  for (let qi = 0; qi < questions.length; qi++) {
    const q = questions[qi];
    if (!q) continue;
    const joined = [...(answersMatrix[qi] ?? [])]
      .map((s) => String(s).trim())
      .filter((s) => s.length > 0)
      .join(", ");
    const ansText = joined.length > 0 ? joined : translate("prompts.noAnswer");
    let stem = q.question.trim().replace(/\s+/g, " ");
    if (stem.length > 112) stem = `${stem.slice(0, 109)}...`;
    const qDisp = stem.endsWith("?") ? stem : `${stem}?`;
    parts.push(`${qDisp} ${ansText}`);
  }
  return parts.length > 0 ? parts.join(" · ") : translate("prompts.answered");
}

export function rowLettersForQuestion(q: CoddyQuestionItem): readonly string[] {
  const opts = Math.max(q.options?.length ?? 0, 0);
  const total = opts + (q.custom ? 1 : 0);
  const list: string[] = [];
  for (let i = 0; i < Math.min(total, 26); i++)
    list.push(letterForOptionIndex(i));
  return list;
}

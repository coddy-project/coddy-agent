import { expect, test } from "vitest";
import {
  OTHER_SENTINEL,
  allAnsweredNonEmpty,
  buildAnswerRows,
  formatResolvedSummaryLine,
  readyToSubmit,
  rowLettersForQuestion,
} from "./questionAnswers";
import type {
  CoddyQuestionItem,
  CoddyQuestionPayload,
} from "./questionTypes";

function q(over: Partial<CoddyQuestionItem>): CoddyQuestionItem {
  return {
    question: "Pick one?",
    options: [{ label: "A" }, { label: "B" }],
    multiple: false,
    custom: false,
    ...over,
  } as CoddyQuestionItem;
}

function payload(questions: CoddyQuestionItem[]): CoddyQuestionPayload {
  return { sessionId: "s", requestId: "r", questions } as CoddyQuestionPayload;
}

test("buildAnswerRows maps single picks and folds the other-sentinel into free text", () => {
  const p = payload([q({}), q({ custom: true })]);
  const rows = buildAnswerRows(p, [[], []], ["A", OTHER_SENTINEL], ["", " custom answer "], false);
  expect(rows).toEqual([["A"], ["custom answer"]]);
});

test("buildAnswerRows multi keeps picked labels plus trimmed free text", () => {
  const p = payload([q({ multiple: true, custom: true })]);
  const rows = buildAnswerRows(
    p,
    [["A", OTHER_SENTINEL, "B"]],
    [""],
    [" extra "],
    false,
  );
  expect(rows).toEqual([["A", "B", "extra"]]);
});

test("buildAnswerRows returns empty rows when skipped", () => {
  const p = payload([q({}), q({})]);
  expect(buildAnswerRows(p, [[], []], ["A", "B"], ["", ""], true)).toEqual([
    [],
    [],
  ]);
});

test("allAnsweredNonEmpty requires a non-blank cell per row", () => {
  expect(allAnsweredNonEmpty([["A"], ["b"]])).toBe(true);
  expect(allAnsweredNonEmpty([["A"], ["  "]])).toBe(false);
  expect(allAnsweredNonEmpty([["A"], []])).toBe(false);
});

test("readyToSubmit gates the other-sentinel on non-empty free text", () => {
  const qs = [q({ custom: true })];
  expect(
    readyToSubmit({
      questions: qs,
      multiSel: [[]],
      singleSel: [OTHER_SENTINEL],
      extraText: [""],
    }),
  ).toBe(false);
  expect(
    readyToSubmit({
      questions: qs,
      multiSel: [[]],
      singleSel: [OTHER_SENTINEL],
      extraText: ["something"],
    }),
  ).toBe(true);
});

test("readyToSubmit multi requires a pick or filled free text", () => {
  const qs = [q({ multiple: true, custom: true })];
  expect(
    readyToSubmit({
      questions: qs,
      multiSel: [[OTHER_SENTINEL]],
      singleSel: [""],
      extraText: [""],
    }),
  ).toBe(false);
  expect(
    readyToSubmit({
      questions: qs,
      multiSel: [["A"]],
      singleSel: [""],
      extraText: [""],
    }),
  ).toBe(true);
});

test("formatResolvedSummaryLine joins question/answer pairs and truncates long stems", () => {
  const long = "x".repeat(200);
  const line = formatResolvedSummaryLine(
    [q({ question: "Pick one" }), q({ question: long })],
    false,
    [["A"], ["B"]],
  );
  const parts = line.split(" · ");
  expect(parts).toHaveLength(2);
  expect(parts[0]).toBe("Pick one? A");
  expect(parts[1]).toContain("...? B");
  expect((parts[1] ?? "").length).toBeLessThan(130);
});

test("rowLettersForQuestion counts options plus the custom row, capped at 26", () => {
  expect(rowLettersForQuestion(q({}))).toEqual(["A", "B"]);
  expect(rowLettersForQuestion(q({ custom: true }))).toEqual(["A", "B", "C"]);
  const many = q({
    options: Array.from({ length: 30 }, (_, i) => ({ label: `o${i}` })),
    custom: true,
  });
  expect(rowLettersForQuestion(many)).toHaveLength(26);
});

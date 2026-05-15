import { expect, test } from "vitest";

import {
  parseQuestionToolAnswersFromResult,
  parseQuestionToolQuestionsFromArgs,
  questionToolSummaryLabel,
} from "./questionToolDisplay";

test("parses nested questions text from question tool arguments", () => {
  const args = JSON.stringify({
    questions: [{ question: "  Ok? ", options: [{ label: "Yes" }] }],
  });
  expect(parseQuestionToolQuestionsFromArgs(args)).toEqual([
    { question: "Ok?" },
  ]);
});

test("parses nested answers arrays from stored tool JSON", () => {
  expect(
    parseQuestionToolAnswersFromResult(
      JSON.stringify({ answers: [["Yes", " maybe "], []] }),
    ),
  ).toEqual([["Yes", "maybe"], []]);
});

test("summary label joins question and answer when terminal", () => {
  const label = questionToolSummaryLabel({
    argsText: JSON.stringify({
      questions: [{ question: "Sure?", options: [{ label: "Yes" }] }],
    }),
    resultText: JSON.stringify({ answers: [["Yes"]] }),
    pendingLike: false,
    terminal: true,
  });
  expect(label).toBe("Sure? Yes");
});

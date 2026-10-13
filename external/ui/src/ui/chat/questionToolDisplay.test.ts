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
    {
      question: "Ok?",
      options: [{ label: "Yes", description: "" }],
      multiple: false,
      custom: true,
    },
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

// The row that records a question is the only place the offer survives: the card
// the reader answered keeps just the question and their answer. So the arguments
// have to carry the options the model put up, in the order it put them up, and
// whether the reader's own answer was offered: on unless the model turned it off
// (issue #257).
test("the parsed question carries the options the model offered", () => {
  const items = parseQuestionToolQuestionsFromArgs(
    JSON.stringify({
      questions: [
        {
          question: "Which scheduler did you mean?",
          options: [
            { label: "Todo plan", description: "A checklist of tasks" },
            { label: "Background tasks" },
          ],
          custom: true,
        },
        {
          question: "Pick any that apply",
          options: [{ label: "One" }],
          multiple: true,
        },
        {
          question: "Delete the branch?",
          options: [{ label: "yes" }, { label: "no" }],
          custom: false,
        },
      ],
    }),
  );

  expect(items).toEqual([
    {
      question: "Which scheduler did you mean?",
      options: [
        { label: "Todo plan", description: "A checklist of tasks" },
        { label: "Background tasks", description: "" },
      ],
      multiple: false,
      custom: true,
    },
    {
      question: "Pick any that apply",
      options: [{ label: "One", description: "" }],
      multiple: true,
      custom: true,
    },
    {
      question: "Delete the branch?",
      options: [
        { label: "yes", description: "" },
        { label: "no", description: "" },
      ],
      multiple: false,
      custom: false,
    },
  ]);
});

test("a question with no options still parses, and junk entries are dropped", () => {
  expect(
    parseQuestionToolQuestionsFromArgs(
      JSON.stringify({
        questions: [{ question: "Bare", options: [{ label: "" }, 7, null] }],
      }),
    ),
  ).toEqual([{ question: "Bare", options: [], multiple: false, custom: true }]);
});

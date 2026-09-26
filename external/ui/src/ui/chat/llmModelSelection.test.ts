import { expect, test } from "vitest";
import {
  firstAlphabeticalBackend,
  pickDefaultLlmModelForNewChat,
  pickLlmModelForOpenSession,
  sessionScopedModelCommand,
} from "./llmModelSelection";

const backends = ["openai/gpt-4o", "openai/gpt-4o-mini"] as const;

test("new chat prefers remembered cookie", () => {
  expect(
    pickDefaultLlmModelForNewChat({
      backends,
      cookie: "openai/gpt-4o-mini",
    }),
  ).toBe("openai/gpt-4o-mini");
});

test("new chat without cookie takes the alphabetically first backend", () => {
  const unordered = ["zed/m", "aaa/m", "mid/m"];
  expect(
    pickDefaultLlmModelForNewChat({ backends: unordered, cookie: null }),
  ).toBe("aaa/m");
});

test("new chat ignores a cookie naming a model that is gone", () => {
  expect(
    pickDefaultLlmModelForNewChat({
      backends: ["zed/m", "aaa/m"],
      cookie: "removed/m",
    }),
  ).toBe("aaa/m");
});

test("open session uses its stored model", () => {
  expect(
    pickLlmModelForOpenSession({
      backends,
      sessionModel: "openai/gpt-4o-mini",
    }),
  ).toBe("openai/gpt-4o-mini");
});

test("open session without a listed model falls back to the alphabetically first backend", () => {
  expect(
    pickLlmModelForOpenSession({
      backends: ["zed/m", "aaa/m"],
      sessionModel: "",
    }),
  ).toBe("aaa/m");
  expect(
    pickLlmModelForOpenSession({
      backends: ["zed/m", "aaa/m"],
      sessionModel: "removed/m",
    }),
  ).toBe("aaa/m");
});

test("firstAlphabeticalBackend skips blanks and sorts", () => {
  expect(firstAlphabeticalBackend([" z ", " a "]), "trims").toBe("a");
  expect(firstAlphabeticalBackend([]), "empty").toBe("");
});

test("sessionScopedModelCommand reads a session-scoped pick", () => {
  expect(sessionScopedModelCommand("/model openai/gpt-4o")).toBe(
    "openai/gpt-4o",
  );
  expect(sessionScopedModelCommand("/model openai/gpt-4o review this")).toBe(
    "openai/gpt-4o",
  );
});

test("sessionScopedModelCommand skips non-picks and turn-scoped forms", () => {
  expect(sessionScopedModelCommand("/model")).toBeNull();
  expect(sessionScopedModelCommand("/model --once openai/gpt-4o")).toBeNull();
  expect(sessionScopedModelCommand("/model openai/gpt-4o --once hi")).toBeNull();
  expect(
    sessionScopedModelCommand("/model openai/gpt-4o --count=3 hi"),
  ).toBeNull();
  expect(sessionScopedModelCommand("/model openai/gpt-4o --count 3 hi")).toBeNull();
  expect(sessionScopedModelCommand("say /model openai/gpt-4o")).toBeNull();
});

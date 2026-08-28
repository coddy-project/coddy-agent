import { describe, expect, it } from "vitest";
import { openAIStreamErrorMessage } from "./streamError";
import { setLocale } from "../i18n/i18n";

describe("openAIStreamErrorMessage", () => {
  it("returns null when there is no error field", () => {
    expect(openAIStreamErrorMessage({ choices: [] })).toBeNull();
    expect(openAIStreamErrorMessage(null)).toBeNull();
  });

  it("reads message from error object", () => {
    expect(
      openAIStreamErrorMessage({
        error: { message: "LLM error: context too long" },
      }),
    ).toBe("LLM error: context too long");
  });

  it("trims message", () => {
    expect(openAIStreamErrorMessage({ error: { message: "  oops  " } })).toBe(
      "oops",
    );
  });

  it("handles string error", () => {
    expect(openAIStreamErrorMessage({ error: "plain" })).toBe("plain");
  });

  it("fallback when error object has no message", () => {
    expect(openAIStreamErrorMessage({ error: { code: "x" } })).toBe(
      "Request failed",
    );
  });

  it("localizes the fallback error", () => {
    setLocale("ru");
    expect(openAIStreamErrorMessage({ error: { code: "x" } })).toBe(
      "Запрос не выполнен",
    );
    setLocale("en");
  });
});

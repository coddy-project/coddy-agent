import { afterEach, expect, test } from "vitest";
import {
  CODDY_LLM_REASONING_COOKIE,
  readReasoningCookie,
  writeReasoningCookie,
} from "./reasoningCookie";

afterEach(() => {
  document.cookie = `${CODDY_LLM_REASONING_COOKIE}=; Path=/; Max-Age=0`;
});

test("write then read round-trips the level", () => {
  writeReasoningCookie("high");
  expect(readReasoningCookie()).toBe("high");
});

test("empty value is not written", () => {
  writeReasoningCookie("");
  expect(readReasoningCookie()).toBeNull();
});

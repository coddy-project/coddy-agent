import { expect, test } from "vitest";
import {
  isNoLiveTurnRelayError,
  NO_ACTIVE_STREAM_CODE,
} from "./composerStreamError";

test("the relay code marks a missing turn as a state, not a failure", () => {
  expect(isNoLiveTurnRelayError(NO_ACTIVE_STREAM_CODE, "anything")).toBe(true);
});

test("a server without the code field is recognised by its message", () => {
  expect(isNoLiveTurnRelayError(null, "no active composer stream")).toBe(true);
  expect(isNoLiveTurnRelayError(null, "  No Active Composer Stream  ")).toBe(
    true,
  );
});

test("a real streaming failure is not mistaken for a missing turn", () => {
  expect(isNoLiveTurnRelayError(null, "model exploded")).toBe(false);
  expect(isNoLiveTurnRelayError("rate_limit", "slow down")).toBe(false);
  expect(isNoLiveTurnRelayError(null, null)).toBe(false);
  expect(isNoLiveTurnRelayError(undefined, undefined)).toBe(false);
});

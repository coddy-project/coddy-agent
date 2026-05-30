import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { createDebouncedSessionStatsRefresh } from "./sessionStatsPoll";

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

test("calls refresh after debounce delay", () => {
  const refresh = vi.fn();
  const poll = createDebouncedSessionStatsRefresh(refresh, 400);

  poll("sess-1");
  expect(refresh).not.toHaveBeenCalled();

  vi.advanceTimersByTime(400);
  expect(refresh).toHaveBeenCalledOnce();
  expect(refresh).toHaveBeenCalledWith("sess-1");
});

test("debounces rapid calls — only last sid fires", () => {
  const refresh = vi.fn();
  const poll = createDebouncedSessionStatsRefresh(refresh, 400);

  poll("sess-1");
  poll("sess-2");
  poll("sess-3");

  vi.advanceTimersByTime(400);
  expect(refresh).toHaveBeenCalledOnce();
  expect(refresh).toHaveBeenCalledWith("sess-3");
});

test("ignores empty sid", () => {
  const refresh = vi.fn();
  const poll = createDebouncedSessionStatsRefresh(refresh, 400);

  poll("  ");
  vi.advanceTimersByTime(1000);
  expect(refresh).not.toHaveBeenCalled();
});

test("two separate calls after each delay fire independently", () => {
  const refresh = vi.fn();
  const poll = createDebouncedSessionStatsRefresh(refresh, 400);

  poll("sess-a");
  vi.advanceTimersByTime(400);
  expect(refresh).toHaveBeenCalledTimes(1);

  poll("sess-b");
  vi.advanceTimersByTime(400);
  expect(refresh).toHaveBeenCalledTimes(2);
  expect(refresh).toHaveBeenNthCalledWith(2, "sess-b");
});

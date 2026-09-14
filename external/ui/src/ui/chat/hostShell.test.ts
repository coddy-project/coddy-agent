import { afterEach, expect, test, vi } from "vitest";

import {
  setHostShell,
  snapshotHostShell,
  subscribeHostShell,
} from "./hostShell";

afterEach(() => setHostShell(""));

test("the host shell is empty until the server answers", () => {
  expect(snapshotHostShell()).toBe("");
});

test("a path is trimmed, published once, and announced to subscribers", () => {
  const onChange = vi.fn();
  const stop = subscribeHostShell(onChange);

  setHostShell("  /usr/bin/bash  ");
  expect(snapshotHostShell()).toBe("/usr/bin/bash");
  expect(onChange).toHaveBeenCalledTimes(1);

  // The same value on every session change must not re-render the transcript.
  setHostShell("/usr/bin/bash");
  expect(onChange).toHaveBeenCalledTimes(1);

  stop();
  setHostShell("/bin/sh");
  expect(onChange).toHaveBeenCalledTimes(1);
  expect(snapshotHostShell()).toBe("/bin/sh");
});

test("a server that does not report a shell leaves the value empty", () => {
  setHostShell(undefined);
  expect(snapshotHostShell()).toBe("");
  setHostShell(42);
  expect(snapshotHostShell()).toBe("");
});

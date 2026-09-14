import { expect, test } from "vitest";
import {
  acceptQueueVersion,
  resetQueueVersion,
  type QueueVersions,
} from "./messageQueueState";

test("a newer delivery is applied and remembered", () => {
  const versions: QueueVersions = new Map();
  expect(acceptQueueVersion(versions, "sess_a", 4)).toBe(true);
  expect(acceptQueueVersion(versions, "sess_a", 5)).toBe(true);
  expect(versions.get("sess_a")).toBe(5);
});

// The whole point of the version: the turn stream and the event stream are
// separate connections, so the older of two deliveries must not win.
test("an older delivery is dropped", () => {
  const versions: QueueVersions = new Map();
  acceptQueueVersion(versions, "sess_a", 9);
  expect(acceptQueueVersion(versions, "sess_a", 3)).toBe(false);
  expect(versions.get("sess_a")).toBe(9);
});

// Two clients can be told about the same change; applying it twice is harmless
// because every delivery carries the whole list.
test("the same version is applied again", () => {
  const versions: QueueVersions = new Map();
  acceptQueueVersion(versions, "sess_a", 7);
  expect(acceptQueueVersion(versions, "sess_a", 7)).toBe(true);
});

test("sessions are ordered independently", () => {
  const versions: QueueVersions = new Map();
  acceptQueueVersion(versions, "sess_a", 12);
  expect(acceptQueueVersion(versions, "sess_b", 1)).toBe(true);
  expect(versions.get("sess_a")).toBe(12);
  expect(versions.get("sess_b")).toBe(1);
});

// An unversioned delivery must neither be dropped nor poison the high-water
// mark, so a NaN or a missing field cannot silence every later frame.
test("an unversioned delivery is applied without raising the mark", () => {
  const versions: QueueVersions = new Map();
  acceptQueueVersion(versions, "sess_a", 6);
  expect(acceptQueueVersion(versions, "sess_a", 0)).toBe(true);
  expect(acceptQueueVersion(versions, "sess_a", Number.NaN)).toBe(true);
  expect(versions.get("sess_a")).toBe(6);
  expect(acceptQueueVersion(versions, "sess_a", 8)).toBe(true);
  expect(versions.get("sess_a")).toBe(8);
});

test("a reset lets a restarted server start counting again", () => {
  const versions: QueueVersions = new Map();
  acceptQueueVersion(versions, "sess_a", 900);
  resetQueueVersion(versions, "sess_a");
  expect(acceptQueueVersion(versions, "sess_a", 1)).toBe(true);
  expect(versions.get("sess_a")).toBe(1);
});

test("an empty session id is never applied", () => {
  const versions: QueueVersions = new Map();
  expect(acceptQueueVersion(versions, "   ", 3)).toBe(false);
  expect(versions.size).toBe(0);
});

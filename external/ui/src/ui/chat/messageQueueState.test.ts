import { expect, test } from "vitest";
import {
  acceptQueueVersion,
  QueueDeliveryOrder,
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

test.each([0, 1])(
  "a fresh snapshot recovers version %i and fences old sources",
  (version) => {
    const order = new QueueDeliveryOrder();
    order.accept("sess_a", 100);
    const old = order.capture("sess_a");
    expect(order.acceptSnapshot("sess_a", version, old)).toBe(true);
    expect(order.capture("sess_a").epoch).toBe(old.epoch + 1);
    expect(order.accept("sess_a", 101, old.epoch)).toBe(false);
    expect(order.acceptSnapshot("sess_a", 101, old)).toBe(false);
    expect(order.accept("sess_a", version + 1)).toBe(true);
  },
);

test.each([2, 100, 101])(
  "a pushed version %i fences a snapshot already in flight",
  (version) => {
    const order = new QueueDeliveryOrder();
    order.accept("sess_a", 100);
    const reading = order.capture("sess_a");
    order.accept("sess_a", version);
    expect(order.acceptSnapshot("sess_a", 1, reading)).toBe(false);
    expect(order.capture("sess_a").epoch).toBe(reading.epoch);
  },
);

test("a same-server snapshot keeps the epoch and high-water mark", () => {
  const order = new QueueDeliveryOrder();
  order.accept("sess_a", 100);
  const reading = order.capture("sess_a");
  expect(order.acceptSnapshot("sess_a", 100, reading)).toBe(true);
  expect(order.capture("sess_a").epoch).toBe(reading.epoch);
  expect(order.accept("sess_a", 99, reading.epoch)).toBe(false);
});

test("a restart recovery does not invalidate another session's sources or reads", () => {
  const order = new QueueDeliveryOrder();
  order.accept("sess_a", 100);
  order.accept("sess_b", 100);
  const other = order.capture("sess_b");
  expect(order.acceptSnapshot("sess_a", 1, order.capture("sess_a"))).toBe(true);
  expect(order.acceptSnapshot("sess_b", 100, other)).toBe(true);
  expect(order.accept("sess_b", 101, other.epoch)).toBe(true);
});

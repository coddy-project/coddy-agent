import { afterEach, expect, test } from "vitest";
import {
  formatUtcForLocalDisplay,
  formatUtcToLocalFullDetail,
  formatUtcToLocalHM,
} from "./formatMessageTime";

const prevTZ = process.env.TZ;

afterEach(() => {
  if (prevTZ === undefined) {
    delete process.env.TZ;
  } else {
    process.env.TZ = prevTZ;
  }
});

test("formatUtcForLocalDisplay returns empty for invalid input", () => {
  expect(formatUtcForLocalDisplay("")).toBe("");
  expect(formatUtcForLocalDisplay("not-a-date")).toBe("");
});

test("formatUtcForLocalDisplay emits locale string for valid UTC", () => {
  process.env.TZ = "Europe/Moscow";
  const s = formatUtcForLocalDisplay("2026-05-08T08:51:00Z");
  expect(s.length).toBeGreaterThan(6);
  expect(s).toMatch(/2026/);
});

test("formatUtcToLocalHM returns empty for invalid input", () => {
  expect(formatUtcToLocalHM("")).toBe("");
});

test("formatUtcToLocalHM is short wall time in local TZ", () => {
  process.env.TZ = "Europe/Moscow";
  const s = formatUtcToLocalHM("2026-05-08T15:51:00Z");
  expect(s).toMatch(/\d/);
  expect(s.length).toBeLessThan(16);
});

test("formatUtcToLocalFullDetail includes date parts and offset or zone", () => {
  process.env.TZ = "Europe/Moscow";
  const s = formatUtcToLocalFullDetail("2026-05-08T15:51:07Z");
  expect(s).toMatch(/2026/);
  expect(s).toMatch(/51/);
  expect(s).toMatch(/07/);
  expect(s.length).toBeGreaterThan(12);
});

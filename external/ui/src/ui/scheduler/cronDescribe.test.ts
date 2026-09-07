import { describe, expect, it } from "vitest";
import {
  describeCronScheduleOrError,
  describeCronScheduleUTC,
} from "./cronDescribe";
import { setLocale } from "../i18n/i18n";

describe("describeCronScheduleUTC", () => {
  it("returns human text for valid 5-field cron", () => {
    const out = describeCronScheduleUTC("0 * * * *");
    expect(out).toBeTruthy();
    expect(out!.toLowerCase()).toMatch(/hour/);
  });

  it("returns null for empty or invalid", () => {
    expect(describeCronScheduleUTC("")).toBeNull();
    expect(describeCronScheduleUTC("not a cron")).toBeNull();
  });
});

describe("describeCronScheduleOrError", () => {
  it("returns ok for standard expression", () => {
    const r = describeCronScheduleOrError("* * * * *");
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect(r.text.length).toBeGreaterThan(5);
    }
  });

  it("describes step minute */2 (UTC five-field) like cronstrue verbose", () => {
    const r = describeCronScheduleOrError("*/2 * * * *");
    expect(r.ok).toBe(true);
    if (r.ok) {
      expect(r.text).toBe("Every 2 minutes, every hour, every day");
    }
  });

  it("returns error for empty", () => {
    const r = describeCronScheduleOrError("  ");
    expect(r.ok).toBe(false);
    if (!r.ok) {
      expect(r.error).toMatch(/cron/i);
    }
  });

  it("returns error for garbage", () => {
    const r = describeCronScheduleOrError("hello world");
    expect(r.ok).toBe(false);
  });

  it("localizes validation errors", () => {
    setLocale("ru");
    const r = describeCronScheduleOrError("  ");
    expect(r).toEqual({
      ok: false,
      error: "Введите cron-выражение (5 полей, UTC).",
    });
    setLocale("en");
  });
});

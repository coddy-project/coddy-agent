import cronstrue from "cronstrue";
import "cronstrue/locales/ru";
import { getLocale, t } from "../i18n/i18n";

function cronLocale(): "en" | "ru" {
  return getLocale() === "ru" ? "ru" : "en";
}

/**
 * Human-readable description of a 5-field cron (UTC on server).
 * Returns null when spec is empty or invalid.
 */
export function describeCronScheduleUTC(spec: string): string | null {
  const s = spec.trim();
  if (!s) {
    return null;
  }
  try {
    return cronstrue.toString(s, {
      use24HourTimeFormat: true,
      verbose: true,
      locale: cronLocale(),
    });
  } catch {
    return null;
  }
}

export function describeCronScheduleOrError(spec: string):
  | {
      ok: true;
      text: string;
    }
  | { ok: false; error: string } {
  const s = spec.trim();
  if (!s) {
    return { ok: false, error: t("scheduler.cron.required") };
  }
  try {
    const text = cronstrue.toString(s, {
      use24HourTimeFormat: true,
      verbose: true,
      locale: cronLocale(),
    });
    return { ok: true, text };
  } catch (e) {
    // cronstrue names the offending field ("Expression has only 3 parts..."), which is worth
    // more to the author than a generic sentence, so the translated copy is only a fallback.
    const msg = e instanceof Error ? e.message.trim() : "";
    return { ok: false, error: msg || t("scheduler.cron.invalid") };
  }
}

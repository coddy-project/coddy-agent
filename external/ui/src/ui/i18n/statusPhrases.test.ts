import { describe, expect, test } from "vitest";

import { UI_LOCALES, UI_LOCALE_IDS } from "./locales";

/**
 * The live status line beside the typing dots carries the phase and nothing it acts on
 * (DESIGN.md, States -> Working): no path, no command, no url, no tool id. A phrase
 * written to be completed by a target that follows it - "Reading", "Running" - therefore
 * reads as a fragment, so every `status.*` value has to be a complete phrase on its own,
 * in every dictionary.
 *
 * The check is mechanical on purpose: a hand-kept list of the phrases that matter rots as
 * soon as a tool is added. Two words is the bar; the keys below are the only ones that
 * legitimately stand alone in a single word, and the second test keeps that list honest.
 */
const SINGLE_WORD_KEYS = new Set([
  // A phase of the model's own work. Nothing can follow it, in any language.
  "status.thinking",
]);

function statusEntries(locale: (typeof UI_LOCALE_IDS)[number]): [string, string][] {
  const dict = UI_LOCALES[locale].messages as Record<string, string>;
  return Object.entries(dict).filter(([key]) => key.startsWith("status."));
}

function wordCount(phrase: string): number {
  return phrase.trim().split(/\s+/).filter((word) => word !== "").length;
}

describe("live status phrases stand on their own", () => {
  for (const locale of UI_LOCALE_IDS) {
    test(`${locale} carries no bare verb fragment`, () => {
      const fragments = statusEntries(locale)
        .filter(([key]) => !SINGLE_WORD_KEYS.has(key))
        .filter(([, value]) => wordCount(value) < 2)
        .map(([key, value]) => `${key}: ${value}`)
        .sort();
      expect(fragments).toEqual([]);
    });
  }

  // An exclusion that has grown a second word is an exclusion nobody needs any more.
  test("the exclusion list names only keys that really are one word", () => {
    for (const locale of UI_LOCALE_IDS) {
      const dict = Object.fromEntries(statusEntries(locale));
      for (const key of SINGLE_WORD_KEYS) {
        expect({ locale, key, value: dict[key] }).toEqual({
          locale,
          key,
          value: expect.any(String),
        });
        expect({ locale, key, words: wordCount(dict[key] || "") }).toEqual({
          locale,
          key,
          words: 1,
        });
      }
    }
  });
});

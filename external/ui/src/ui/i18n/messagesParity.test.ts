import { expect, test } from "vitest";
import { messagesEn } from "./messages/en";
import { messagesRu } from "./messages/ru";

test("english and russian dictionaries expose the same keys", () => {
  const en = new Set(Object.keys(messagesEn));
  const ru = new Set(Object.keys(messagesRu));
  const missingInRu = [...en].filter((k) => !ru.has(k));
  const missingInEn = [...ru].filter((k) => !en.has(k));
  expect({ missingInRu, missingInEn }).toEqual({
    missingInRu: [],
    missingInEn: [],
  });
});

test("no dictionary value is the empty string", () => {
  for (const [key, value] of Object.entries(messagesEn)) {
    expect(value, `empty english value for ${key}`).not.toBe("");
  }
  for (const [key, value] of Object.entries(messagesRu)) {
    expect(value, `empty russian value for ${key}`).not.toBe("");
  }
});

test("every interpolation token used in english is present in russian", () => {
  const token = (s: string) =>
    (s.match(/\{(\w+)\}/g) ?? []).map((m) => m).sort();
  for (const [key, en] of Object.entries(messagesEn)) {
    const ru = messagesRu[key];
    if (ru === undefined) continue;
    expect(token(en), `token mismatch for ${key}`).toEqual(token(ru));
  }
});

import { expect, test } from "vitest";
import { SESSION_ORIGIN_FILTERS, isSessionOriginFilter } from "./sessionQuery";
import { messagesEn } from "../i18n/messages/en";
import { messagesRu } from "../i18n/messages/ru";

test("print runs are an origin the History filter can pick", () => {
  expect(SESSION_ORIGIN_FILTERS).toEqual(["", "local", "gateway", "print"]);
  expect(isSessionOriginFilter("print")).toBe(true);
  expect(isSessionOriginFilter("cli")).toBe(false);
});

test("the History row for print runs is named in every language", () => {
  expect(messagesEn["sessions.filter.env.print"]).toBe("CLI runs");
  expect(messagesRu["sessions.filter.env.print"]).toBe("Запуски CLI");
});

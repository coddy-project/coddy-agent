import { describe, expect, test } from "vitest";

import {
  appendQuoteToDraft,
  quoteMarkdown,
  splitQuoteBlocks,
} from "./quoteDraft";

describe("quoteMarkdown", () => {
  test("every line becomes a Markdown quote line, a blank one a bare marker", () => {
    expect(quoteMarkdown("line one\n\nline two ")).toBe(
      "> line one\n>\n> line two",
    );
  });

  test("the selection's surrounding blank lines and runs of them are dropped", () => {
    expect(quoteMarkdown("\n\n  first\n\n\n\nsecond\n\n")).toBe(
      "> first\n>\n> second",
    );
  });

  test("Windows line ends read as line ends", () => {
    expect(quoteMarkdown("a\r\nb")).toBe("> a\n> b");
  });

  test("nothing to quote is an empty string", () => {
    expect(quoteMarkdown(" \n \t")).toBe("");
  });
});

describe("appendQuoteToDraft", () => {
  test("an empty draft starts with the quote and leaves a line to write on", () => {
    expect(appendQuoteToDraft("", "> picked")).toBe("> picked\n\n");
    expect(appendQuoteToDraft("  \n", "> picked")).toBe("> picked\n\n");
  });

  test("a quote goes after what is already written, quotes pile up in order", () => {
    const once = appendQuoteToDraft("Why does this happen?", "> first");
    expect(once).toBe("Why does this happen?\n\n> first\n\n");
    expect(appendQuoteToDraft(once, "> second")).toBe(
      "Why does this happen?\n\n> first\n\n> second\n\n",
    );
  });

  test("an empty quote leaves the draft alone", () => {
    expect(appendQuoteToDraft("draft", "")).toBe("draft");
  });
});

describe("splitQuoteBlocks", () => {
  test("a prompt without quotes is one text block", () => {
    expect(splitQuoteBlocks("just text, a > b in the middle")).toEqual([
      { quote: false, text: "just text, a > b in the middle" },
    ]);
  });

  test("a quote line under text starts a quote block", () => {
    expect(splitQuoteBlocks("plain\n> quoted")).toEqual([
      { quote: false, text: "plain" },
      { quote: true, text: "quoted" },
    ]);
  });

  test("quote lines lose their marker and the blank line around them", () => {
    expect(
      splitQuoteBlocks("> first line\n>\n> second\n\nWhy is that?\n\n> again"),
    ).toEqual([
      { quote: true, text: "first line\n\nsecond" },
      { quote: false, text: "Why is that?" },
      { quote: true, text: "again" },
    ]);
  });

  test("a marker glued to the text is not a quote", () => {
    expect(splitQuoteBlocks(">=5 items")).toEqual([
      { quote: false, text: ">=5 items" },
    ]);
  });
});

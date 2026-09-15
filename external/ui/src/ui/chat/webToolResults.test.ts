import { expect, test } from "vitest";
import { webSearchResultMarkdown } from "./webToolResults";

test("a search result becomes a list of links with their snippets", () => {
  const md = webSearchResultMarkdown(
    JSON.stringify({
      query: "iPhone 18 price",
      page: 1,
      has_more_hint: "Call websearch again with page incremented.",
      results: [
        {
          title: "Ostrovok.ru",
          url: "https://ostrovok.ru/",
          description: "Hotel booking service.",
        },
        { title: "Otello", url: "https://otello.ru/", description: "" },
      ],
    }),
  );

  expect(md).toBe(
    [
      "- [Ostrovok.ru](https://ostrovok.ru/)",
      "  Hotel booking service.",
      "- [Otello](https://otello.ru/)",
      "",
      "Call websearch again with page incremented.",
    ].join("\n"),
  );
});

test("an empty result says so instead of rendering an empty list", () => {
  expect(
    webSearchResultMarkdown(
      JSON.stringify({ query: "x", page: 1, has_more_hint: "No results; try rephrasing the query.", results: [] }),
    ),
  ).toBe("No results; try rephrasing the query.");
});

test("a hit with no title falls back to its url", () => {
  expect(
    webSearchResultMarkdown(
      JSON.stringify({ results: [{ title: "", url: "https://coddy.dev/" }] }),
    ),
  ).toBe("- [https://coddy.dev/](https://coddy.dev/)");
});

test("brackets in a title cannot break out of the link", () => {
  expect(
    webSearchResultMarkdown(
      JSON.stringify({
        results: [{ title: "A [bracketed] title", url: "https://coddy.dev/" }],
      }),
    ),
  ).toBe("- [A \\[bracketed\\] title](https://coddy.dev/)");
});

test("anything that is not a search result keeps its plain text", () => {
  // A truncated preview, an error line, a shape the tool no longer returns.
  expect(webSearchResultMarkdown("error: http 503")).toBeNull();
  expect(webSearchResultMarkdown('{"query":"x","results":')).toBeNull();
  expect(webSearchResultMarkdown(JSON.stringify({ query: "x" }))).toBeNull();
  expect(webSearchResultMarkdown("")).toBeNull();
  expect(webSearchResultMarkdown(undefined)).toBeNull();
});

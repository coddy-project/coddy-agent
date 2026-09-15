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

// A transcript row carries the first nineteen lines of a tool's output, and a
// search answers with far more, so the payload the row holds is almost always cut
// mid-array. The hits that did arrive whole still read as links.
test("a preview cut mid-array renders the hits that arrived whole", () => {
  const full = JSON.stringify(
    {
      query: "iPhone 18 price",
      page: 1,
      results: [
        { title: "First", url: "https://one.example/", description: "One." },
        { title: "Second", url: "https://two.example/", description: "Two." },
        { title: "Third", url: "https://three.example/", description: "Three." },
      ],
    },
    null,
    2,
  );
  // Cut the way the server cuts it: the leading lines plus an ellipsis row.
  const cut = full.split("\n").slice(0, 14).join("\n") + "\n...";
  expect(cut).not.toContain("Third");

  expect(webSearchResultMarkdown(cut)).toBe(
    [
      "- [First](https://one.example/)",
      "  One.",
      "- [Second](https://two.example/)",
      "  Two.",
    ].join("\n"),
  );
});

test("a brace inside a title cannot run the scan past its own object", () => {
  const full = JSON.stringify({
    results: [
      { title: 'A } brace { inside', url: "https://one.example/", description: "" },
      { title: "Second", url: "https://two.example/", description: "" },
    ],
  });
  // Strip the closing bracket so the whole document no longer parses.
  const cut = full.slice(0, full.length - 2);
  expect(webSearchResultMarkdown(cut)).toBe(
    [
      "- [A } brace { inside](https://one.example/)",
      "- [Second](https://two.example/)",
    ].join("\n"),
  );
});

test("a preview cut before the first whole hit keeps its plain text", () => {
  expect(
    webSearchResultMarkdown('{\n  "query": "x",\n  "results": [\n    {\n      "title": "Fir\n...'),
  ).toBeNull();
});

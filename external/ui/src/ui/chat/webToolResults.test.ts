import { expect, test } from "vitest";
import { webSearchReport, webSearchResultMarkdown } from "./webToolResults";

test("a search result becomes a list of links with their snippets", () => {
  const md = webSearchResultMarkdown(
    JSON.stringify({
      query: "iPhone 18 price",
      page: 1,
      has_more_hint: "Call websearch again with page incremented",
      results: [
        {
          title: "Ostrovok.ru",
          url: "https://ostrovok.ru/",
          description: "Hotel booking service",
        },
        { title: "Otello", url: "https://otello.ru/", description: "" },
      ],
    }),
  );

  expect(md).toBe(
    [
      "- [Ostrovok\\.ru](https://ostrovok.ru/)\\",
      "  Hotel booking service",
      "- [Otello](https://otello.ru/)",
      "",
      "Call websearch again with page incremented",
    ].join("\n"),
  );
});

test("an empty result says so instead of rendering an empty list", () => {
  expect(
    webSearchResultMarkdown(
      JSON.stringify({ query: "x", page: 1, has_more_hint: "No results; try rephrasing the query.", results: [] }),
    ),
  ).toBe("No results; try rephrasing the query\\.");
});

test("a hit with no title falls back to its url", () => {
  expect(
    webSearchResultMarkdown(
      JSON.stringify({ results: [{ title: "", url: "https://coddy.dev/" }] }),
    ),
  ).toBe("- [https://coddy\\.dev/](https://coddy.dev/)");
});

// A hit is text somebody else wrote, rendered inside a Markdown document.
test("a hit cannot inject markdown into the transcript", () => {
  const md = webSearchResultMarkdown(
    JSON.stringify({
      results: [
        {
          title: "A [bracketed] title\nand a second line",
          url: "https://coddy.dev/",
          description: "- injected row\n\n**bold** and [pay here](https://evil.example/)",
        },
      ],
    }),
  );

  // One list item, one continuation line, and nothing that parses as syntax.
  expect(md!.split("\n")).toHaveLength(2);
  expect(md).toContain("\\[bracketed\\]");
  expect(md).not.toMatch(/\n- injected/);
  expect(md).not.toContain("[pay here](https://evil.example/)");
});

test("a url that would end its own link early is encoded", () => {
  expect(
    webSearchResultMarkdown(
      JSON.stringify({
        results: [
          { title: "Safe", url: "https://a.example/x) ![x](https://evil.example/x.png" },
        ],
      }),
    ),
  ).toBe("- [Safe](https://a.example/x%29%20![x]%28https://evil.example/x.png)");
});

test("a hit whose url is not http(s) renders as text, never as a link", () => {
  for (const url of ["javascript:alert(1)", "data:text/html,<script>", "notaurl"]) {
    const md = webSearchResultMarkdown(
      JSON.stringify({ results: [{ title: "Click me", url }] }),
    );
    expect(md, url).toBe("- Click me");
  }
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
        { title: "First", url: "https://one.example/", description: "One" },
        { title: "Second", url: "https://two.example/", description: "Two" },
        { title: "Third", url: "https://three.example/", description: "Three" },
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
      "- [First](https://one.example/)\\",
      "  One",
      "- [Second](https://two.example/)\\",
      "  Two",
    ].join("\n"),
  );
});

test("a brace inside a title cannot run the scan past its own object", () => {
  const full = JSON.stringify({
    results: [
      { title: "A } brace { inside", url: "https://one.example/", description: "" },
      { title: "Second", url: "https://two.example/", description: "" },
    ],
  });
  // Strip the closing bracket so the whole document no longer parses.
  const cut = full.slice(0, full.length - 2);
  expect(webSearchResultMarkdown(cut)).toBe(
    [
      "- [A \\} brace \\{ inside](https://one.example/)",
      "- [Second](https://two.example/)",
    ].join("\n"),
  );
});

test("a preview cut before the first whole hit keeps its plain text", () => {
  expect(
    webSearchResultMarkdown('{\n  "query": "x",\n  "results": [\n    {\n      "title": "Fir\n...'),
  ).toBeNull();
});

// The tool answers `hint`, which the transcript used to look for as `has_more_hint`
// and so never showed.
test("the tool's own hint closes the list", () => {
  expect(
    webSearchResultMarkdown(
      JSON.stringify({
        query: "x",
        page: 1,
        results: [{ title: "One", url: "https://one.example/" }],
        hint: "Call websearch again with page 2",
      }),
    ),
  ).toBe(
    [
      "- [One](https://one.example/)",
      "",
      "Call websearch again with page 2",
    ].join("\n"),
  );
});

const searchOutput = {
  query: "iPhone 18 announcement September 2026 preorder",
  page: 1,
  engines: [
    {
      engine: "brave",
      status: "blocked",
      results: 0,
      reason: "http 429",
      took_ms: 214,
    },
    { engine: "bing", status: "ok", results: 10, took_ms: 158 },
  ],
  results: [
    {
      title: "First",
      url: "https://one.example/",
      description: "One",
      source: "bing",
    },
    {
      title: "Second",
      url: "https://two.example/",
      description: "Two",
      source: "bing",
    },
  ],
};

test("the engine report is read off a whole answer", () => {
  expect(webSearchReport(JSON.stringify(searchOutput))).toEqual({
    query: "iPhone 18 announcement September 2026 preorder",
    page: 1,
    engines: [
      {
        engine: "brave",
        status: "blocked",
        results: 0,
        reason: "http 429",
        tookMs: 214,
        cached: false,
      },
      {
        engine: "bing",
        status: "ok",
        results: 10,
        reason: "",
        tookMs: 158,
        cached: false,
      },
    ],
  });
});

// The row's preview is the first nineteen lines, and the engines take most of them:
// the cut lands on `"results": [` with not one hit whole. The report is still there.
test("the engine report survives a preview cut before the first hit", () => {
  const cut =
    JSON.stringify(searchOutput, null, 2).split("\n").slice(0, 19).join("\n") +
    "\n...";
  expect(cut).not.toContain("First");
  expect(webSearchResultMarkdown(cut)).toBeNull();
  expect(
    webSearchReport(cut)?.engines.map((e) => [e.engine, e.status]),
  ).toEqual([
    ["brave", "blocked"],
    ["bing", "ok"],
  ]);
});

test("anything that is not a search answer has no report", () => {
  expect(webSearchReport("error: all engines blocked")).toBeNull();
  expect(webSearchReport(undefined)).toBeNull();
});

test("a bracket inside an engine's reason does not cut the report short", () => {
  const cut =
    JSON.stringify(
      {
        query: "x",
        page: 2,
        engines: [
          {
            engine: "brave",
            status: "error",
            results: 0,
            reason: "bad ] answer",
            took_ms: 1,
          },
          { engine: "bing", status: "ok", results: 3, took_ms: 2 },
        ],
        results: [{ title: "Cut", url: "https://c" }],
      },
      null,
      2,
    ).slice(0, -40) + "\n...";
  const report = webSearchReport(cut);
  expect(report?.page).toBe(2);
  expect(report?.engines.map((e) => e.engine)).toEqual(["brave", "bing"]);
  expect(report?.engines[0]?.reason).toBe("bad ] answer");
});

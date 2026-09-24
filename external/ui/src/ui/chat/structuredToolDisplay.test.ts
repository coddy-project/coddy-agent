import { expect, test } from "vitest";
import {
  backgroundView,
  docsContentsMarkdown,
  fieldRows,
  httpRequestView,
  looksLikeMarkdown,
  memoryEntries,
  memoryHits,
  modelSwitchView,
  parseDocsHits,
  parseDocsPage,
  parseHttpExchange,
  parseJsonDocument,
  parsePlanList,
  parseTaskLine,
  parseToolArgs,
  planFileView,
  previewServerView,
  sessionFilingView,
} from "./structuredToolDisplay";

// Every fixture below is the text the Go tool returns, copied from its
// formatter, so a change of format on either side fails here rather than
// leaving a card that silently falls back to raw text.

test("tool arguments parse only as a JSON object", () => {
  expect(parseToolArgs('{"a":1}')).toEqual({ a: 1 });
  expect(parseToolArgs("")).toEqual({});
  expect(parseToolArgs(undefined)).toEqual({});
  expect(parseToolArgs('{"slug":')).toBeNull();
  expect(parseToolArgs("[1,2]")).toBeNull();
  expect(parseToolArgs("null")).toBeNull();
});

test("a model switch reads what took effect from the result", () => {
  expect(
    modelSwitchView(
      { reasoning: "high", scope: "Session" },
      "Switched for the rest of the session: model neuraldeep/qwen3.8-27b, reasoning high. It applies from your next request.",
    ),
  ).toEqual({
    model: "neuraldeep/qwen3.8-27b",
    reasoning: "high",
    scope: "session",
    applied: true,
  });
  expect(
    modelSwitchView(
      { model: "fast/m" },
      "Switched for the rest of this turn: model fast/m, reasoning none offered. It applies from your next request.",
    ),
  ).toEqual({ model: "fast/m", reasoning: "", scope: "turn", applied: true });
});

test("a model switch still running shows what was asked, scope in any case", () => {
  expect(
    modelSwitchView(
      { model: " fast/m ", reasoning: "low", scope: "SESSION" },
      "",
    ),
  ).toEqual({
    model: "fast/m",
    reasoning: "low",
    scope: "session",
    applied: false,
  });
  expect(modelSwitchView({ model: "fast/m" }, "")).toMatchObject({
    scope: "turn",
    applied: false,
  });
});

test("an http request shows the address it really goes to and every setting that changes it", () => {
  const view = httpRequestView({
    url: "https://api.example.com/v1/items?x=1",
    query: { tag: ["a", "b c"], dry_run: true, n: 3 },
    headers: {
      Authorization: "Bearer sk-live",
      Accept: "application/json",
      "X-Retries": 3,
      "User-Agent": "",
    },
    json: { name: "demo" },
    proxy: "http://user:secret@proxy.local:3128",
    verify_tls: false,
    follow_redirects: true,
    timeout_seconds: 20,
    output_file: " out/body.json ",
    permission_rationale: " probe the staging API ",
  });
  expect(view.method).toBe("POST");
  // Query names in sorted order, the values of one name in the order given,
  // appended to a query the url already has - the way the tool builds it.
  expect(view.url).toBe(
    "https://api.example.com/v1/items?x=1&dry_run=true&n=3&tag=a&tag=b+c",
  );
  expect(view.headers).toEqual([
    {
      name: "Accept",
      value: "application/json",
      masked: false,
      removed: false,
    },
    { name: "Authorization", value: "", masked: true, removed: false },
    { name: "User-Agent", value: "", masked: false, removed: true },
    { name: "X-Retries", value: "3", masked: false, removed: false },
  ]);
  // The payload itself: the generic preview showed it inside the arguments.
  expect(view.body).toEqual({
    kind: "json",
    bytes: null,
    detail: "",
    content: '{\n  "name": "demo"\n}',
  });
  expect(view.proxy).toBe("http://user:•••@proxy.local:3128");
  expect(view.proxy).not.toContain("secret");
  expect(view.insecureTls).toBe(true);
  expect(view.followRedirects).toBe(true);
  expect(view.timeoutSeconds).toBe(20);
  expect(view.outputFile).toBe("out/body.json");
  expect(view.rationale).toBe("probe the staging API");
});

test("an http request defaults its method and names each kind of body", () => {
  expect(httpRequestView({ url: "https://x.test/" })).toMatchObject({
    method: "GET",
    url: "https://x.test/",
    body: null,
    proxy: "",
    insecureTls: false,
    followRedirects: false,
    timeoutSeconds: null,
  });
  expect(
    httpRequestView({ url: "https://x.test/", method: "put", body: "héllo" }),
  ).toMatchObject({
    method: "PUT",
    body: { kind: "text", bytes: 6, detail: "", content: "héllo" },
  });
  expect(
    httpRequestView({ url: "https://x.test/", body_base64: "aGVsbG8=" }).body,
  ).toEqual({
    kind: "base64",
    bytes: 5,
    detail: "",
    content: "",
  });
  expect(
    httpRequestView({ url: "https://x.test/", body_file: "dist/app.zip" }).body,
  ).toEqual({
    kind: "file",
    bytes: null,
    detail: "dist/app.zip",
    content: "",
  });
  expect(
    httpRequestView({
      url: "https://x.test/",
      form: { b: 1, a: "x", password: "hunter2", tag: ["p", "q"] },
    }).body,
  ).toEqual({
    kind: "form",
    bytes: null,
    // Values as the form sends them, a credential-named field hidden.
    detail: "a=x, b=1, password=•••, tag=p, tag=q",
    content: "",
  });
  expect(
    httpRequestView({
      url: "https://x.test/",
      form_data: [
        { name: "note", value: "hi" },
        { name: "api_key", value: "sk-1" },
        { name: "upload", file: "img/logo.png" },
      ],
    }).body,
  ).toEqual({
    kind: "multipart",
    bytes: null,
    detail: "note=hi, api_key=•••, upload=@img/logo.png",
    content: "",
  });
  expect(
    httpRequestView({ url: "https://x.test/", proxy: "DIRECT" }).proxy,
  ).toBe("direct");
  expect(
    httpRequestView({
      url: "https://x.test/",
      proxy: "socks5://proxy.local:1080",
    }).proxy,
  ).toBe("socks5://proxy.local:1080");
});

test("an http request address never shows the password written into it", () => {
  expect(
    httpRequestView({ url: "https://user:pa%40ss@example.test/api?x=1" }).url,
  ).toBe("https://user:•••@example.test/api?x=1");
  expect(httpRequestView({ url: "https://token@example.test/" }).url).toBe(
    "https://•••@example.test/",
  );
  expect(httpRequestView({ url: "https://example.test/a@b" }).url).toBe(
    "https://example.test/a@b",
  );
});

test("an http answer splits into status, headers, body and the tool's notes", () => {
  const exchange = parseHttpExchange(
    'HTTP/2.0 201 Created\r\nContent-Type: application/json\r\nSet-Cookie: session=abc\r\n\r\n{"id":42}\r\n\r\n[followed redirects: https://a.test/x -> https://a.test/x/]',
  );
  expect(exchange).toEqual({
    status: "201 Created",
    headers: [
      {
        name: "Content-Type",
        value: "application/json",
        masked: false,
        removed: false,
      },
      { name: "Set-Cookie", value: "", masked: true, removed: false },
    ],
    body: '{"id":42}',
    notes: ["followed redirects: https://a.test/x -> https://a.test/x/"],
  });
});

test("an http body that is itself a bracketed line is not taken for a note", () => {
  expect(
    parseHttpExchange(
      "HTTP/1.1 200 OK\nContent-Type: application/json\n\n[1,2,3]",
    ),
  ).toMatchObject({
    body: "[1,2,3]",
    notes: [],
  });
  expect(
    parseHttpExchange(
      "HTTP/1.1 200 OK\nContent-Length: 9\n\n[body: saved 9 bytes to /work/out.bin]",
    ),
  ).toMatchObject({
    body: "",
    notes: ["body: saved 9 bytes to /work/out.bin"],
  });
  expect(parseHttpExchange("HTTP/1.1 204 No Content\nDate: x")).toMatchObject({
    status: "204 No Content",
    body: "",
    notes: [],
  });
});

test("a truncated http preview does not turn its marker into a header", () => {
  expect(
    parseHttpExchange("HTTP/1.1 200 OK\nContent-Type: text/plain\n..."),
  ).toEqual({
    status: "200 OK",
    headers: [
      {
        name: "Content-Type",
        value: "text/plain",
        masked: false,
        removed: false,
      },
    ],
    body: "...",
    notes: [],
  });
  expect(parseHttpExchange("dial tcp: connection refused")).toBeNull();
});

test("a background task line splits into id, status, label, address, detail and note", () => {
  expect(
    parseTaskLine(
      "bg_1 [running] npm run dev at http://127.0.0.1:5173/ (elapsed 1m35s, estimated 2m) wakes you when it ends",
    ),
  ).toEqual({
    id: "bg_1",
    status: "running",
    label: "npm run dev",
    url: "http://127.0.0.1:5173/",
    detail: "elapsed 1m35s, estimated 2m",
    note: "wakes you when it ends",
  });
  expect(
    parseTaskLine("bg_2 [timed_out] go test ./... (elapsed 42s, exit 2)"),
  ).toEqual({
    id: "bg_2",
    status: "timed_out",
    label: "go test ./...",
    url: "",
    detail: "elapsed 42s, exit 2",
    note: "",
  });
  expect(
    parseTaskLine(
      "bg_0 [running] vite (elapsed 3h5m) still alive from an earlier run (pid 4312), reap with background_reap",
    ),
  ).toMatchObject({
    id: "bg_0",
    label: "vite",
    note: "still alive from an earlier run (pid 4312), reap with background_reap",
  });
  expect(parseTaskLine("No background tasks in this session.")).toBeNull();
});

test("background_list reads as tasks, and its empty answer as none", () => {
  expect(
    backgroundView(
      "background_list",
      "bg_1 [running] build (elapsed 4s)\nbg_2 [succeeded] tests (elapsed 9s, exit 0)",
    ),
  ).toEqual({
    kind: "tasks",
    tasks: [
      {
        id: "bg_1",
        status: "running",
        label: "build",
        url: "",
        detail: "elapsed 4s",
        note: "",
      },
      {
        id: "bg_2",
        status: "succeeded",
        label: "tests",
        url: "",
        detail: "elapsed 9s, exit 0",
        note: "",
      },
    ],
  });
  expect(
    backgroundView("background_list", "No background tasks in this session."),
  ).toEqual({
    kind: "tasks",
    tasks: [],
  });
  expect(backgroundView("background_list", "")).toEqual({ kind: "pending" });
});

test("background_output, background_wait and background_stop read as one task and its output", () => {
  expect(
    backgroundView(
      "background_output",
      "bg_2 [succeeded] go test ./... (elapsed 42s, exit 0)\n(earlier output dropped from the in-memory window; the full log is in the session bundle)\n\nok  \tpkg/a\t0.4s\nok  \tpkg/b\t1.8s",
    ),
  ).toMatchObject({
    kind: "task",
    task: { id: "bg_2", status: "succeeded" },
    output: "ok  \tpkg/a\t0.4s\nok  \tpkg/b\t1.8s",
    earlierDropped: true,
    waitedSeconds: null,
  });
  expect(
    backgroundView(
      "background_output",
      "bg_2 [running] build (elapsed 1s)\n\n(no output yet)",
    ),
  ).toMatchObject({ kind: "task", output: "", earlierDropped: false });
  expect(
    backgroundView(
      "background_wait",
      "bg_1 [running] build (elapsed 2m5s, estimated 2m) overdue\nStill running after 30s. Wait again or check background_output later.",
    ),
  ).toMatchObject({
    kind: "task",
    task: { note: "overdue" },
    output: "",
    waitedSeconds: 30,
  });
  expect(
    backgroundView(
      "background_wait",
      "bg_1 [succeeded] build (elapsed 50s, exit 0)\n\ndone",
    ),
  ).toMatchObject({ kind: "task", output: "done", waitedSeconds: null });
  expect(
    backgroundView("background_stop", "bg_1 [stopped] build (elapsed 2m9s)"),
  ).toMatchObject({
    kind: "task",
    task: { status: "stopped" },
    output: "",
  });
  expect(backgroundView("background_stop", "task bg_9 not found")).toEqual({
    kind: "raw",
    text: "task bg_9 not found",
  });
});

test("background_reap reads as the processes it killed", () => {
  expect(
    backgroundView(
      "background_reap",
      "Killed 2 leftover background process group(s):\n- bg_1 (pid 123) npm run dev\n- bg_2 (pid 456) make watch",
    ),
  ).toEqual({
    kind: "reaped",
    tasks: [
      { id: "bg_1", pid: 123, label: "npm run dev" },
      { id: "bg_2", pid: 456, label: "make watch" },
    ],
  });
  expect(
    backgroundView(
      "background_reap",
      "No leftover background processes from an earlier run.",
    ),
  ).toEqual({
    kind: "reaped",
    tasks: [],
  });
});

test("preview_server reads as its address, task, directory and lifetime", () => {
  expect(
    previewServerView(
      "Preview server started: http://127.0.0.1:43215/index%20page.html\nServing /work/site as background task bg_4.\nGive the user this link and invite them to open http://127.0.0.1:43215/index%20page.html in their browser and try it themselves.\nIt stops by itself 600s after it started, or earlier with background_stop. Files are read on every request, so a reload shows your edits; background_output returns the request log.",
    ),
  ).toEqual({
    url: "http://127.0.0.1:43215/index%20page.html",
    taskId: "bg_4",
    directory: "/work/site",
    reused: false,
    stopsAfterSeconds: 600,
  });
  expect(
    previewServerView(
      "The preview server for this directory is already running: http://127.0.0.1:43215/\nServing /work/site as background task bg_4.\nGive the user this link and invite them to open http://127.0.0.1:43215/ in their browser and try it themselves.\nIt runs until it is stopped with background_stop, the session is deleted or coddy exits. Files are read on every request, so a reload shows your edits; background_output returns the request log.",
    ),
  ).toMatchObject({ reused: true, stopsAfterSeconds: null, taskId: "bg_4" });
  expect(
    previewServerView("Preview server started: javascript:alert(1)"),
  ).toBeNull();
  expect(previewServerView("")).toBeNull();
});

test("documentation hits read as references, titles and snippets", () => {
  expect(
    parseDocsHits(
      'Coddy dev documentation: 2 sections for "hooks", best first. Read one with coddy_docs_read and its reference.\n\n1. features/hooks#trust  (Hooks > Trust [advanced])\n   Project hooks run only after approval.\n2. operate/security  (Security)\n',
    ),
  ).toEqual([
    {
      ref: "features/hooks#trust",
      title: "Hooks > Trust [advanced]",
      snippet: "Project hooks run only after approval.",
    },
    { ref: "operate/security", title: "Security", snippet: "" },
  ]);
  expect(
    parseDocsHits(
      'No section of the Coddy dev documentation matches "zzz". Try other or fewer words, or call coddy_docs_read without a page for the contents.',
    ),
  ).toEqual([]);
  expect(parseDocsHits("")).toBeNull();
});

test("a documentation page read splits into its title, reference, lines and text", () => {
  expect(
    parseDocsPage(
      '[Coddy dev documentation] Hooks > Trust\nreference: features/hooks#trust, lines 120-131 of 240; public address: https://coddy.dev/docs/features/hooks#trust\n\n## Trust\n\nApprove first.\n\n[The section continues at line 132 of 240: call coddy_docs_read with page "features/hooks#trust" and offset=132.]\n',
    ),
  ).toEqual({
    title: "Hooks > Trust",
    ref: "features/hooks#trust",
    from: 120,
    to: 131,
    total: 240,
    body: "## Trust\n\nApprove first.",
    continuesAt: 132,
  });
  expect(
    parseDocsPage(
      "[Coddy dev documentation] Hooks\nreference: features/hooks, lines 1-40 of 40; public address: https://coddy.dev/docs/features/hooks\n\n# Hooks\n",
    ),
  ).toMatchObject({
    title: "Hooks",
    ref: "features/hooks",
    body: "# Hooks",
    continuesAt: null,
  });
  expect(parseDocsPage("# Just markdown")).toBeNull();
});

test("the documentation contents read as links into the reader", () => {
  expect(
    docsContentsMarkdown(
      '[Coddy dev documentation] Contents: every page as "- <page> - <title>: <summary>". Read one with coddy_docs_read, or search with coddy_docs_search.\n\n## Features\n- features/hooks - Hooks: Lifecycle hooks.\n- features/mcp - MCP: Servers and trust.\n',
    ),
  ).toBe(
    "## Features\n- [Hooks](coddy:features/hooks): Lifecycle hooks.\n- [MCP](coddy:features/mcp): Servers and trust.",
  );
  expect(
    docsContentsMarkdown("[Coddy dev documentation] Hooks\nreference: x"),
  ).toBeNull();
});

test("a plan list reads as its plans, and its empty answer as none", () => {
  expect(
    parsePlanList(
      JSON.stringify(
        [
          {
            slug: "release",
            name: "Release plan",
            overview: "Ship it",
            updatedAt: "2026-09-24T08:59:00Z",
          },
          { slug: "docs" },
        ],
        null,
        2,
      ),
    ),
  ).toEqual([
    {
      slug: "release",
      name: "Release plan",
      overview: "Ship it",
      updatedAt: "2026-09-24T08:59:00Z",
    },
    { slug: "docs", name: "", overview: "", updatedAt: "" },
  ]);
  expect(parsePlanList("No design plans in this session.")).toEqual([]);
  expect(parsePlanList('[\n  {\n    "slug": "release",\n...')).toBeNull();
});

test("a plan file reads its name and overview from the frontmatter, quotes and all", () => {
  expect(
    planFileView(
      "---\nname: \"Release plan\"\noverview: 'Ship 1.3'\ntodos:\n  - Check\n---\n# Release\n\n- Step\n",
    ),
  ).toEqual({
    name: "Release plan",
    overview: "Ship 1.3",
    body: "# Release\n\n- Step",
  });
  expect(planFileView("# No frontmatter")).toEqual({
    name: "",
    overview: "",
    body: "# No frontmatter",
  });
});

test("session_describe reads as the filing and what changed", () => {
  expect(
    sessionFilingView(
      '{"changed":["title"],"object":"session.filing","tags":["release","docs"],"title":"Release 1.3"}',
    ),
  ).toEqual({
    title: "Release 1.3",
    tags: ["release", "docs"],
    changed: ["title"],
  });
  expect(sessionFilingView("")).toBeNull();
  expect(sessionFilingView('{"object":"other"}')).toBeNull();
});

test("memory search hits and a memory listing read as rows", () => {
  expect(
    memoryHits(
      "### Hit 1 (global score=7 path=global:notes/release.md)\nAlways run make docs-check.\nTwice.\n\n### Hit 2 (project score=3 path=project:ci.md)\nThe matrix.\n\n",
    ),
  ).toEqual([
    {
      scope: "global",
      score: 7,
      path: "global:notes/release.md",
      snippet: "Always run make docs-check.\nTwice.",
    },
    {
      scope: "project",
      score: 3,
      path: "project:ci.md",
      snippet: "The matrix.",
    },
  ]);
  expect(memoryHits("No matching memory files.")).toEqual([]);
  expect(memoryHits("")).toBeNull();
  expect(
    memoryEntries(
      "- release.md (file) size=112 modified=2026-09-24T08:00:00Z\n- design (dir) modified=2026-09-23T10:00:00Z\n",
    ),
  ).toEqual([
    { name: "release.md", kind: "file", size: 112 },
    { name: "design", kind: "dir", size: null },
  ]);
  expect(memoryEntries("(empty directory)")).toEqual([]);
  expect(memoryEntries("")).toBeNull();
});

test("a JSON document is an object or an array, nothing else", () => {
  expect(parseJsonDocument(' {"a":1} ')).toEqual({ a: 1 });
  expect(parseJsonDocument("[1]")).toEqual([1]);
  expect(parseJsonDocument("42")).toBeUndefined();
  expect(parseJsonDocument('"text"')).toBeUndefined();
  expect(parseJsonDocument('{"a":')).toBeUndefined();
  expect(parseJsonDocument("")).toBeUndefined();
});

test("an object reads as rows: text, literals, short lists and nested JSON", () => {
  expect(
    fieldRows({
      object: "session.filing",
      title: "Release",
      count: 3,
      ok: true,
      none: null,
      tags: ["a", 2, false],
      empty: [],
      nested: { a: 1 },
      objects: [{ a: 1 }],
    }),
  ).toEqual([
    { key: "title", value: { kind: "text", text: "Release" } },
    { key: "count", value: { kind: "literal", text: "3" } },
    { key: "ok", value: { kind: "literal", text: "true" } },
    { key: "none", value: { kind: "literal", text: "null" } },
    { key: "tags", value: { kind: "list", items: ["a", "2", "false"] } },
    { key: "empty", value: { kind: "literal", text: "[]" } },
    { key: "nested", value: { kind: "json", text: '{\n  "a": 1\n}' } },
    {
      key: "objects",
      value: { kind: "json", text: '[\n  {\n    "a": 1\n  }\n]' },
    },
  ]);
});

test("text reads as Markdown when it carries Markdown's own structure", () => {
  expect(looksLikeMarkdown("### Result\n- Page URL: http://x")).toBe(true);
  expect(looksLikeMarkdown("Ran:\n```js\nawait page.goto('x');\n```")).toBe(
    true,
  );
  expect(
    looksLikeMarkdown(
      "Available Libraries:\n\n- Title: react-markdown\n- Trust Score: 8.9",
    ),
  ).toBe(true);
  expect(looksLikeMarkdown("ok  \tpkg/a\t0.4s\nFAIL\tpkg/b")).toBe(false);
  expect(looksLikeMarkdown("- just one dash line")).toBe(false);
  expect(looksLikeMarkdown("#hashtag without space")).toBe(false);
});

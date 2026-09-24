import { useState } from "react";
import { afterEach, expect, test } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { ToolCallMessage } from "./ToolCallMessage";
import { setLocale } from "../i18n/i18n";

afterEach(() => {
  cleanup();
  setLocale("en");
});

// Results below are what the Go tools write, copied from their formatters; see
// chat/structuredToolDisplay.test.ts for the parsing itself.
function show(
  title: string,
  args: object,
  result: string,
  status = "completed",
) {
  render(
    <ToolCallMessage
      toolCallId={title}
      title={title}
      status={status}
      argsText={JSON.stringify(args)}
      resultText={result}
    />,
  );
  fireEvent.click(screen.getByLabelText("Tool summary"));
  return screen.getByTestId("structured-tool-card");
}

test("switch_model names the choice and lifetime", () => {
  const card = show(
    "switch_model",
    { reasoning: "high", scope: "Session" },
    "Switched for the rest of the session: model fast/model, reasoning high. It applies from your next request.",
  );
  expect(screen.getByText("switching the model")).toBeInTheDocument();
  // The model that took effect, read from the answer: the call only named a level.
  expect(within(card).getByText("fast/model")).toBeInTheDocument();
  expect(within(card).getByText("high")).toBeInTheDocument();
  expect(
    within(card).getByText("For the rest of the session"),
  ).toBeInTheDocument();
  // The sentence written for the model is not repeated under its own fields.
  expect(card.textContent).not.toContain("It applies from your next request");
  expect(within(card).queryByText(/"model"/)).toBeNull();
});

test("http_request separates response status, headers and body and masks credentials", () => {
  const card = show(
    "http_request",
    {
      method: "POST",
      url: "https://example.test/items",
      headers: { Authorization: "Bearer secret", Accept: "application/json" },
      json: { a: 1 },
    },
    'HTTP/1.1 201 Created\nContent-Type: application/json\nSet-Cookie: token=secret\n\n{"ok":true}',
  );
  expect(
    within(card).getByText("POST https://example.test/items"),
  ).toBeInTheDocument();
  expect(within(card).getByText("201 Created")).toBeInTheDocument();
  expect(within(card).getByText("JSON")).toBeInTheDocument();
  expect(
    within(card).getByText(/Accept: application\/json/),
  ).toBeInTheDocument();
  expect(card.textContent).not.toContain("secret");
  const body = within(card).getByText('{"ok":true}');
  expect(body.tagName).toBe("PRE");
  expect(body).toHaveClass("structured-tool-output");
});

test("http_request shows every setting that changes where it goes and what it trusts", () => {
  const card = show(
    "http_request",
    {
      url: "https://api.test/v1/items",
      query: { page: 2 },
      proxy: "http://user:hunter2@proxy.local:3128",
      verify_tls: false,
      follow_redirects: true,
      body_file: "dist/payload.json",
      permission_rationale: "probe the staging API",
    },
    "HTTP/2.0 200 OK\nContent-Length: 2\n\nok\n\n[followed redirects: https://api.test/v1/items?page=2 -> https://api.test/v1/items/?page=2]",
  );
  // The heading is the address the request really went to, query included.
  expect(
    within(card).getByText("POST https://api.test/v1/items?page=2"),
  ).toBeInTheDocument();
  expect(
    within(card).getByText("http://user:•••@proxy.local:3128"),
  ).toBeInTheDocument();
  expect(card.textContent).not.toContain("hunter2");
  expect(within(card).getByText("not verified")).toHaveClass(
    "structured-tool-warning",
  );
  expect(
    within(card).getByText("followed within the same origin"),
  ).toBeInTheDocument();
  expect(within(card).getByText(/dist\/payload\.json/)).toBeInTheDocument();
  expect(within(card).getByText("probe the staging API")).toBeInTheDocument();
  expect(within(card).getByText(/^followed redirects: /)).toHaveClass(
    "scheduler-tool-muted",
  );
});

test("background task calls present the task, its status and output", () => {
  const card = show(
    "background_wait",
    { task_id: "bg_42", timeout_seconds: 10 },
    "bg_42 [running] build (elapsed 10s, estimated 1m)\nStill running after 10s. Wait again or check background_output later.",
  );
  expect(within(card).getByText("bg_42")).toBeInTheDocument();
  expect(within(card).getByText("Running")).toBeInTheDocument();
  expect(
    within(card).getByText("elapsed 10s, estimated 1m"),
  ).toBeInTheDocument();
  expect(
    within(card).getByText("Still running after a 10s wait"),
  ).toBeInTheDocument();
});

test("background_output shows the log in monospace", () => {
  const card = show(
    "background_output",
    { task_id: "bg_2" },
    "bg_2 [succeeded] go test ./... (elapsed 42s, exit 0)\n\nok  \tpkg/a\t0.4s",
  );
  expect(within(card).getByText("Succeeded")).toBeInTheDocument();
  expect(within(card).getByText(/ok\s+pkg\/a/)).toHaveClass(
    "structured-tool-output",
  );
});

test("background_list separates each task's id, state and detail", () => {
  const card = show(
    "background_list",
    {},
    "bg_1 [running] build (elapsed 4s)\nbg_2 [succeeded] tests (elapsed 9s, exit 0)",
  );
  expect(within(card).getAllByRole("listitem")).toHaveLength(2);
  expect(within(card).getByText("bg_1")).toBeInTheDocument();
  expect(within(card).getByText("Succeeded")).toBeInTheDocument();
  expect(within(card).getByText("elapsed 9s, exit 0")).toBeInTheDocument();
});

test("background task statuses read in the interface language", () => {
  setLocale("ru");
  render(
    <ToolCallMessage
      toolCallId="bg-ru"
      title="background_list"
      status="completed"
      argsText="{}"
      resultText="bg_1 [timed_out] build (elapsed 2m)"
    />,
  );
  fireEvent.click(screen.getByLabelText("Сводка инструмента"));
  expect(screen.getByTestId("structured-tool-card").textContent).not.toContain(
    "timed_out",
  );
});

test("background_reap lists what it killed, and says so when nothing was left", () => {
  let card = show(
    "background_reap",
    {},
    "Killed 1 leftover background process group(s):\n- bg_0 (pid 4312) vite",
  );
  expect(within(card).getByText("bg_0")).toBeInTheDocument();
  expect(within(card).getByText("pid 4312")).toBeInTheDocument();
  cleanup();
  card = show(
    "background_reap",
    {},
    "No leftover background processes from an earlier run.",
  );
  expect(
    within(card).getByText("No processes left over from an earlier run"),
  ).toBeInTheDocument();
});

test("preview_server exposes its address as a link", () => {
  const card = show(
    "preview_server",
    { path: "site" },
    "Preview server started: http://127.0.0.1:5000/\nServing /work/site as background task bg_2.\nGive the user this link and invite them to open http://127.0.0.1:5000/ in their browser and try it themselves.\nIt stops by itself 600s after it started, or earlier with background_stop. Files are read on every request, so a reload shows your edits; background_output returns the request log.",
  );
  const link = within(card).getByRole("link", {
    name: "http://127.0.0.1:5000/",
  });
  expect(link).toHaveAttribute("href", "http://127.0.0.1:5000/");
  expect(link).toHaveAttribute("rel", "noopener noreferrer");
  expect(within(card).getByText("bg_2")).toBeInTheDocument();
  expect(within(card).getByText("600s after it started")).toBeInTheDocument();
  // What the tool told the model to do with the link is not the user's to read.
  expect(card.textContent).not.toContain("invite them");
});

test("documentation search displays references as rows", () => {
  const card = show(
    "coddy_docs_search",
    { query: "hooks", limit: 8 },
    'Coddy dev documentation: 1 sections for "hooks", best first. Read one with coddy_docs_read and its reference.\n\n1. features/hooks#trust  (Hooks > Trust)\n   Approve project hooks\n',
  );
  const link = within(card).getByRole("link", { name: "Hooks > Trust" });
  expect(link).toHaveAttribute("href", "#/docs/features/hooks#trust");
  expect(within(card).getByText("features/hooks#trust")).toBeInTheDocument();
  expect(within(card).getByText("Approve project hooks")).toBeInTheDocument();
});

test("a documentation title is text, never Markdown that could retarget its link", () => {
  const card = show(
    "coddy_docs_search",
    { query: "x" },
    'Coddy dev documentation: 1 sections for "x", best first. Read one with coddy_docs_read and its reference.\n\n1. features/hooks  (x](https://evil.test/)(y)\n',
  );
  for (const link of within(card).getAllByRole("link")) {
    expect(link.getAttribute("href")).not.toContain("evil.test");
  }
});

test("documentation read renders the section and links to the reader", () => {
  const card = show(
    "coddy_docs_read",
    { page: "features/hooks#trust" },
    '[Coddy dev documentation] Hooks > Trust\nreference: features/hooks#trust, lines 120-131 of 240; public address: https://coddy.dev/docs/features/hooks#trust\n\n## Trust\n\nApprove **first**.\n\n[The section continues at line 132 of 240: call coddy_docs_read with page "features/hooks#trust" and offset=132.]\n',
  );
  expect(within(card).getByText("Hooks > Trust")).toBeInTheDocument();
  expect(
    within(card).getByRole("heading", { name: "Trust" }),
  ).toBeInTheDocument();
  expect(within(card).getByText("first").tagName).toBe("STRONG");
  expect(
    within(card).getByRole("link", { name: "features/hooks#trust" }),
  ).toHaveAttribute("href", "#/docs/features/hooks#trust");
  expect(within(card).getByText("lines 120-131 of 240")).toBeInTheDocument();
  expect(within(card).getByText("Continues at line 132")).toBeInTheDocument();
  expect(card.textContent).not.toContain("public address");
});

test("plan list, read and write have plan-specific bodies", () => {
  let card = show(
    "plan_list",
    {},
    JSON.stringify(
      [
        {
          slug: "launch",
          name: "Launch plan",
          updatedAt: "2026-09-23T12:00:00Z",
        },
      ],
      null,
      2,
    ),
  );
  expect(within(card).getByText("Launch plan")).toBeInTheDocument();
  cleanup();
  card = show(
    "plan_read",
    { slug: "launch" },
    "---\nname: Launch plan\noverview: Ship the launch\n---\n# Steps\n\n- Ship it",
  );
  expect(within(card).getByText("Steps")).toBeInTheDocument();
  expect(within(card).getByText("Ship the launch")).toBeInTheDocument();
  expect(card.textContent).not.toContain("name: Launch plan");
  cleanup();
  card = show(
    "plan_write",
    { slug: "launch", content: '---\nname: "Launch plan"\n---\n# Steps' },
    'wrote design plan "launch" (36 bytes)',
  );
  expect(within(card).getByText("Launch plan")).toBeInTheDocument();
  expect(within(card).getByText("launch")).toBeInTheDocument();
  expect(card.textContent).not.toContain('"content"');
});

test("session_describe shows the title, the tags and what changed", () => {
  const card = show(
    "session_describe",
    { title: "Release 1.3", add_tags: ["release"] },
    '{"changed":["title","tags"],"object":"session.filing","tags":["release","docs"],"title":"Release 1.3"}',
  );
  expect(within(card).getByText("Release 1.3")).toBeInTheDocument();
  expect(within(card).getByText("release")).toHaveClass("structured-tool-chip");
  expect(within(card).getByText("Changed: title, tags")).toBeInTheDocument();
  expect(card.textContent).not.toContain("session.filing");
});

test("config tools read their JSON answer as fields, with secrets as the server redacted them", () => {
  const card = show(
    "config_set",
    { commands: ["set providers.0.api_key=sk-123"] },
    '{"config_file":"/home/demo/.coddy/config.yaml","hint":"Review with config_changes.","ok":true,"pending":["set providers.0.api_key=<redacted>"]}',
  );
  expect(within(card).getByText("config_file")).toBeInTheDocument();
  expect(
    within(card).getByText("set providers.0.api_key=<redacted>"),
  ).toHaveClass("structured-tool-chip");
  expect(card.textContent).not.toContain("sk-123");
});

test("memory notes render as Markdown and search hits as rows", () => {
  let card = show(
    "coddy_memory_read",
    { path: "global:notes/release.md" },
    "# Release\n\n- Tag after CI",
  );
  expect(
    within(card).getByRole("heading", { name: "Release" }),
  ).toBeInTheDocument();
  cleanup();
  card = show(
    "coddy_memory_search",
    { query: "release", scope: "both" },
    "### Hit 1 (global score=7 path=global:notes/release.md)\nTag after CI.\n\n",
  );
  expect(within(card).getByText("global:notes/release.md")).toBeInTheDocument();
  expect(within(card).getByText("score 7")).toBeInTheDocument();
  expect(within(card).getByText("Tag after CI.")).toBeInTheDocument();
});

test("an MCP call shows its arguments as fields and a JSON answer as fields", () => {
  const card = show(
    "github__get_issue",
    { owner: "coddy-project", issue_number: 353, labels: ["ui", "tools"] },
    '{"number":353,"title":"Structured cards","user":{"login":"hijera"}}',
  );
  expect(within(card).getByText("github")).toHaveClass(
    "structured-tool-mcp-server",
  );
  // The separator is text a screen reader can read, not a pseudo-element.
  expect(card.querySelector(".permission-preview-location")?.textContent).toBe(
    "github · get_issue",
  );
  expect(within(card).getByText("get_issue")).toHaveClass(
    "structured-tool-mcp-tool",
  );
  expect(within(card).getByText("coddy-project")).toBeInTheDocument();
  expect(within(card).getByText("tools")).toHaveClass("structured-tool-chip");
  expect(within(card).getByText("Structured cards")).toBeInTheDocument();
  expect(within(card).getByText(/"login": "hijera"/)).toHaveClass(
    "structured-tool-output",
  );
  expect(card.textContent).not.toContain('{"owner"');
});

test("an MCP answer written in Markdown renders as a document", () => {
  const card = show(
    "playwright__browser_navigate",
    { url: "http://127.0.0.1:5000/" },
    "### Page\n- Page URL: http://127.0.0.1:5000/\n- Page Title: Coddy",
  );
  expect(
    within(card).getByRole("heading", { name: "Page" }),
  ).toBeInTheDocument();
  expect(within(card).getAllByRole("listitem")).toHaveLength(2);
});

test("an MCP answer in plain text stays monospace text", () => {
  const card = show("fs__stat", { path: "a.txt" }, "size 12\nmode 0644");
  expect(within(card).getByText(/size 12/)).toHaveClass(
    "structured-tool-output",
  );
});

// Raw text on purpose: a JS number cannot carry 12345678901234567891, so these
// answers are written the way a server sends them, not through JSON.stringify.
function showRaw(title: string, argsText: string, resultText: string) {
  render(
    <ToolCallMessage
      toolCallId={title}
      title={title}
      status="completed"
      argsText={argsText}
      resultText={resultText}
    />,
  );
  fireEvent.click(screen.getByLabelText("Tool summary"));
  return screen.getByTestId("structured-tool-card");
}

test("an MCP answer shows every number as the server wrote it", () => {
  // A snowflake id past 2^53 came out rounded (12345678901234567000), a
  // repeated key kept only its last value, and nested JSON was printed back
  // from the parsed value.
  const card = showRaw(
    "discord__get_messages",
    '{"channel_id":1234567890123456789,"limit":5}',
    '{"id":12345678901234567891,"name":"caf\\u00e9","k":1,"k":2,"items":[{"id":9007199254740993,"city":"Z\\u00fcrich"}]}',
  );
  const text = card.textContent ?? "";
  expect(text).toContain("1234567890123456789");
  expect(text).toContain("12345678901234567891");
  expect(text).toContain("9007199254740993");
  expect(text).not.toContain("1234567890123456800");
  expect(text).not.toContain("12345678901234567000");
  expect(text).not.toContain("9007199254740992");
  // A text row shows the string the literal encodes.
  expect(within(card).getByText("café")).toHaveClass("structured-tool-text");
  // Both values of a repeated key are rows of their own.
  expect(within(card).getAllByText("k")).toHaveLength(2);
  expect(within(card).getByText("1")).toHaveClass("structured-tool-mono");
  expect(within(card).getByText("2")).toHaveClass("structured-tool-mono");
  // Nested JSON is the server's text, indented: literals and escapes as sent.
  expect(within(card).getByText(/"id": 9007199254740993/)).toHaveTextContent(
    '"city": "Z\\u00fcrich"',
  );
});

test("an MCP answer that is a JSON array is the server's text, indented", () => {
  const card = showRaw(
    "discord__list_guilds",
    "{}",
    '[{"id":9007199254740993,"name":"a"},{"id":1}]',
  );
  expect(card.querySelector(".structured-tool-output")?.textContent).toBe(
    [
      "[",
      "  {",
      '    "id": 9007199254740993,',
      '    "name": "a"',
      "  },",
      "  {",
      '    "id": 1',
      "  }",
      "]",
    ].join("\n"),
  );
});

test("a failed MCP call keeps the raw panels", () => {
  render(
    <ToolCallMessage
      toolCallId="mcp-failed"
      title="playwright__browser_tabs"
      status="failed"
      argsText='{"action":"list"}'
      resultText="error: mcp tool error: Browser is already in use"
    />,
  );
  fireEvent.click(screen.getByLabelText("Tool summary"));
  expect(screen.queryByTestId("structured-tool-card")).toBeNull();
  expect(screen.getByText(/Browser is already in use/)).toBeInTheDocument();
});

test("a call still running shows its card without an empty body", () => {
  const card = show(
    "http_request",
    { url: "https://x.test/" },
    "",
    "in_progress",
  );
  expect(within(card).getByText("GET https://x.test/")).toBeInTheDocument();
  expect(card.querySelector(".scheduler-tool-body")).toBeNull();
});

test("malformed arguments retain the generic preview", () => {
  render(
    <ToolCallMessage
      toolCallId="bad"
      title="plan_read"
      status="completed"
      argsText='{"slug":'
      resultText="bad data"
    />,
  );
  fireEvent.click(screen.getByLabelText("Tool summary"));
  expect(screen.queryByTestId("structured-tool-card")).toBeNull();
  expect(screen.getByText("bad data")).toBeInTheDocument();
});

test("model change labels are localized", () => {
  setLocale("ru");
  render(
    <ToolCallMessage
      toolCallId="model-ru"
      title="switch_model"
      status="completed"
      argsText='{"model":"fast/model","scope":"turn"}'
      resultText="Switched for the rest of this turn: model fast/model, reasoning none offered. It applies from your next request."
    />,
  );
  fireEvent.click(screen.getByLabelText("Сводка инструмента"));
  expect(screen.getByText("переключаю модель")).toBeInTheDocument();
  expect(screen.getByText("До конца хода")).toBeInTheDocument();
});

test("a truncated structured result caps the card's body and keeps More and Less", async () => {
  function Harness() {
    const [full, setFull] = useState("");
    return (
      <ToolCallMessage
        toolCallId="long-plan"
        title="plan_read"
        status="completed"
        argsText='{"slug":"launch"}'
        resultText="# Steps\n..."
        fullResultText={full}
        resultWasTruncated
        onFetchToolCallFull={async () =>
          setFull(`# Steps\n${"- Task\n".repeat(80)}`)
        }
      />
    );
  }
  render(<Harness />);
  fireEvent.click(screen.getByLabelText("Tool summary"));
  const card = screen.getByTestId("structured-tool-card");
  const body = () => card.querySelector(".scheduler-tool-body");
  // The bar naming the call stays outside the capped viewport.
  expect(
    card
      .querySelector(".permission-preview-bar")
      ?.closest(".tool-result-viewport"),
  ).toBeNull();
  expect(body()).toHaveClass("tool-result-viewport--clip");
  fireEvent.click(screen.getByTestId("tool-result-more"));
  await waitFor(() =>
    expect(screen.getByTestId("tool-result-less")).toBeInTheDocument(),
  );
  expect(body()).toHaveClass("tool-result-viewport--scroll");
  fireEvent.click(screen.getByTestId("tool-result-less"));
  expect(body()).toHaveClass("tool-result-viewport--clip");
});

test("a plan preview cut inside its frontmatter stays text until the rest arrives", () => {
  const card = show(
    "plan_read",
    { slug: "long" },
    `---\nname: Long plan\ntodos:\n${"  - step\n".repeat(16)}...`,
  );
  expect(within(card).getByText(/name: Long plan/)).toHaveClass(
    "structured-tool-output",
  );
});

// jsdom has no layout, so the wrap itself is held by the stylesheet: the bar of a
// structured card must not end in an ellipsis the way the generic preview's does.
test("a structured card's bar wraps instead of clipping", async () => {
  const { readFileSync } = await import("node:fs");
  const { dirname, join } = await import("node:path");
  const { fileURLToPath } = await import("node:url");
  const css = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
    "utf8",
  );
  const rule = css.match(
    /^\.structured-tool-card \.permission-preview-location \{[^}]*\}/m,
  );
  expect(rule?.[0]).toMatch(/white-space:\s*normal/);
  expect(rule?.[0]).toMatch(/overflow-wrap:\s*anywhere/);
});

test("http_request shows the payload it sent, as the generic preview did", () => {
  const card = show(
    "http_request",
    { url: "https://x.test/items", json: { name: "demo", size: 3 } },
    "HTTP/1.1 201 Created\n\n{}",
  );
  expect(within(card).getByText(/"name": "demo"/)).toHaveClass(
    "structured-tool-output",
  );
});

test("plan_write shows the plan it wrote", () => {
  const card = show(
    "plan_write",
    {
      slug: "launch",
      content: "---\nname: Launch plan\n---\n# Steps\n\n- Ship it",
    },
    'wrote design plan "launch" (44 bytes)',
  );
  expect(
    within(card).getByRole("heading", { name: "Steps" }),
  ).toBeInTheDocument();
});

test("a plain-text memory note stays text", () => {
  const card = show(
    "coddy_memory_read",
    { path: "global:notes/names.txt" },
    "use_snake_case_names\n*not emphasis*",
  );
  expect(within(card).getByText(/use_snake_case_names/)).toHaveClass(
    "structured-tool-output",
  );
});

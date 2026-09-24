/**
 * The stand behind `scripts/phone-overflow-check.mjs`.
 *
 * Whether a row is wider than the transcript is a layout fact, so jsdom cannot
 * assert it. This page mounts the rows that used to be - a tool row named after
 * an MCP tool, an answer with a long link and a long identifier, a user message
 * of one unbroken word - from the real components against the real stylesheet,
 * in a column with the chat's own 14px margins, with no backend behind them,
 * plus a built-in tool whose raw output is one unbroken line and rows whose label
 * leaves little of its line at some width. Every tool row is open, so the
 * argument preview and the result are measured too.
 */
import { useEffect } from "react";
import ReactDOM from "react-dom/client";
import "./styles.css";
import { AssistantMessage } from "./ui/messages/AssistantMessage";
import { ToolCallMessage } from "./ui/messages/ToolCallMessage";
import { UserMessage } from "./ui/messages/UserMessage";
import { I18nProvider } from "./ui/i18n/I18nProvider";
import { initLocale } from "./ui/i18n/i18n";
import { UI_LOCALE_DEFAULT, isUiLocale } from "./ui/i18n/locales";

const lang = new URLSearchParams(location.search).get("lang") || "";
initLocale(isUiLocale(lang) ? lang : UI_LOCALE_DEFAULT);

const longRepo = "organization-with-a-long-name/repository-number-0-with-a-long-name";
const mcpAnswer = JSON.stringify({
  total_count: 2,
  items: [0, 1].map((i) => ({
    full_name: longRepo.replace("0", String(i)),
    html_url: `https://github.com/${longRepo}/tree/main/packages/some/deeply/nested/path`,
    // One unbroken token, the way a JWT or a base64 blob arrives.
    token: "Zm9vYmFy".repeat(40),
  })),
});

const answer = [
  "A table, a code block, a link and an identifier:",
  "",
  "| Column one | Column two | Column three | Column four | Column five |",
  "|---|---|---|---|---|",
  "| value | another value | yet another value | a fourth value | the fifth value |",
  "",
  "```go",
  'func main() { fmt.Println("a very long line of code that does not fit on a phone") }',
  "```",
  "",
  `See https://example.com/${"a".repeat(90)} and \`inline_code_that_is_very_long_and_unbroken_identifier_name\`.`,
].join("\n");

function Fixture() {
  // Open every row, as the reader does to see what a call did.
  useEffect(() => {
    for (const d of document.querySelectorAll("details")) d.open = true;
  }, []);
  return (
    // The chat column: 14px off either edge on the stacked shell, a centred
    // 920px stripe on the desktop.
    <div
      className="phone-overflow-stand"
      style={{
        boxSizing: "border-box",
        width: "100%",
        maxWidth: 920,
        margin: "0 auto",
        padding: "0 14px",
      }}
    >
      <div className="messages-inner">
        <UserMessage content={`search ${"x".repeat(80)}`} />
        <ToolCallMessage
          toolCallId="tc-mcp-search"
          title="github__search_repositories_with_extended_filters"
          status="completed"
          argsText={JSON.stringify({
            query: "language:go+topic:agent+stars:>1000+org:organization-with-a-long-name",
            per_page: 30,
          })}
          resultText={mcpAnswer}
          durationMs={12}
          onFetchToolCallFull={async () => {}}
        />
        <ToolCallMessage
          toolCallId="tc-mcp-navigate"
          title="mcp__playwright__browser_navigate"
          status="failed"
          argsText={JSON.stringify({
            url: `https://example.com/some/really/long/path/index.html?q=${"a".repeat(60)}`,
          })}
          resultText="navigation failed"
          durationMs={3_723_000}
          onFetchToolCallFull={async () => {}}
        />
        <ToolCallMessage
          toolCallId="tc-command"
          title="run_command"
          status="completed"
          argsText={JSON.stringify({
            command:
              "go test -run 'TestSomethingVeryLongNamedThatDescribesTheWholeScenario' ./internal/some/deeply/nested/package/...",
          })}
          resultText="ok"
          durationMs={125}
          onFetchToolCallFull={async () => {}}
        />
        <ToolCallMessage
          toolCallId="tc-glob"
          title="glob"
          status="completed"
          argsText={JSON.stringify({ pattern: "**/*.go" })}
          resultText={`internal/${"very_long_directory_name_".repeat(8)}/main.go`}
          durationMs={40}
          onFetchToolCallFull={async () => {}}
        />
        <ToolCallMessage
          toolCallId="tc-short-target"
          title="read"
          status="failed"
          argsText={JSON.stringify({ path: "go.mod" })}
          resultText="no such file"
          durationMs={3}
          onFetchToolCallFull={async () => {}}
        />
        <ToolCallMessage
          toolCallId="tc-read-range"
          title="read"
          status="completed"
          argsText={JSON.stringify({
            path: `internal/${"very_long_directory_name_".repeat(4)}/tools/fs/read.go`,
            offset: 1200,
            limit: 81,
          })}
          resultText="package fs"
          durationMs={3}
          onFetchToolCallFull={async () => {}}
        />
        <ToolCallMessage
          toolCallId="tc-mcp-mid"
          title="github__create_issue"
          status="failed"
          argsText={JSON.stringify({ title: "Crash on start when the config has no providers at all" })}
          resultText="rate limited"
          durationMs={65_000}
          onFetchToolCallFull={async () => {}}
        />
        <AssistantMessage content={answer} />
      </div>
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <Fixture />
  </I18nProvider>,
);

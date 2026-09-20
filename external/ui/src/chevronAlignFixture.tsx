/**
 * The stand behind `scripts/chevron-align-check.mjs`.
 *
 * The chevron's alignment is a layout fact, so jsdom cannot assert it: only a
 * real engine lays a glyph out. This page mounts the three surfaces the operator
 * keeps reporting - a thinking row, a tool row and the Tasks drawer's finished
 * counter - from the real components against the real stylesheet, with no
 * backend behind them, so the check needs nothing but `npx vite`.
 */
import ReactDOM from "react-dom/client";
import "./styles.css";
import { ThinkingMessage } from "./ui/messages/ThinkingMessage";
import { ToolCallMessage } from "./ui/messages/ToolCallMessage";
import { BackgroundTasksPanel } from "./ui/tasks/BackgroundTasksPanel";
import type { BackgroundTask } from "./ui/tasks/types";
import { I18nProvider } from "./ui/i18n/I18nProvider";
import { initLocale } from "./ui/i18n/i18n";
import { UI_LOCALE_DEFAULT, isUiLocale } from "./ui/i18n/locales";

const lang = new URLSearchParams(location.search).get("lang") || "";
initLocale(isUiLocale(lang) ? lang : UI_LOCALE_DEFAULT);

const finishedTask: BackgroundTask = {
  id: "task-finished",
  session_id: "sess_fixture",
  kind: "command",
  label: "npm run build",
  command: "npm run build",
  status: "succeeded",
  exit_code: 0,
  started_at: new Date(Date.now() - 120_000).toISOString(),
  finished_at: new Date(Date.now() - 60_000).toISOString(),
  timeout_seconds: 600,
  output_bytes: 128,
  output_truncated: false,
  elapsed_seconds: 60,
  overdue: false,
  running: false,
};

function Fixture() {
  return (
    <div className="chevron-align-stand">
      <div className="messages-inner">
        <ThinkingMessage
          status="completed"
          content="a fold body"
          durationMs={1200}
        />
        <ToolCallMessage
          toolCallId="tc-fixture"
          title="run_command"
          status="completed"
          argsText='{"command":"ls -la"}'
          resultText="ok"
          durationMs={125}
          onFetchToolCallFull={async () => {}}
        />
      </div>
      <BackgroundTasksPanel
        open
        tasks={[finishedTask]}
        listError={null}
        loading={false}
        nowMs={Date.now()}
        loadOutput={async () => null}
        onClose={() => {}}
        onStopTask={() => {}}
        onClearFinished={() => {}}
        onOpenSession={() => {}}
      />
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <Fixture />
  </I18nProvider>,
);

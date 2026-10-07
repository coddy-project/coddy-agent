/**
 * The stand behind `scripts/artifact-card-check.mjs`.
 *
 * Where a shared file's card and its actions menu land is a layout fact, so
 * jsdom cannot assert it: the menu used to open inside a card that clips what
 * overflows it, and nothing but a real engine shows that. This page mounts the
 * cards from the real components against the real stylesheet - a row of them
 * inline in an answer, one under a share_file tool row, and a last answer whose
 * card can be scrolled to the foot of the window - with no backend behind them,
 * so the check needs nothing but `npx vite`.
 */
import ReactDOM from "react-dom/client";
import "./styles.css";
import { AssistantMessage } from "./ui/messages/AssistantMessage";
import { ToolCallMessage } from "./ui/messages/ToolCallMessage";
import type { ToolArtifact } from "./ui/chat/toolArtifacts";
import { I18nProvider } from "./ui/i18n/I18nProvider";
import { initLocale } from "./ui/i18n/i18n";
import { UI_LOCALE_DEFAULT, isUiLocale } from "./ui/i18n/locales";

const lang = new URLSearchParams(location.search).get("lang") || "";
initLocale(isUiLocale(lang) ? lang : UI_LOCALE_DEFAULT);

const picture =
  "data:image/svg+xml," +
  encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" width="320" height="200"><rect width="320" height="200" fill="#3b4a6b"/><circle cx="160" cy="100" r="60" fill="#9fb4e0"/></svg>',
  );

function artifact(
  id: string,
  name: string,
  size: number,
  extra: Partial<ToolArtifact> = {},
): ToolArtifact {
  return {
    id,
    name,
    sha256: id
      .padEnd(64, "0")
      .slice(0, 64)
      .replace(/[^a-f0-9]/g, "a"),
    size,
    url: `/coddy/sessions/sess_fixture/artifacts/${id}`,
    sourcePath: `/workspace/reports/${name}`,
    relativePath: `reports/${name}`,
    ...extra,
  };
}

const report = artifact("a1", "release-notes-request.md", 8602);
const chart = artifact("a2", "throughput.png", 48211, { previewUrl: picture });
const sheet = artifact("a3", "q3.csv", 1204);
const shared = artifact("a4", "summary.pdf", 210_944);
const last = artifact("a5", "build.log", 30_120);

const byId = (...rows: ToolArtifact[]) =>
  new Map(rows.map((row) => [row.id, row]));

function Fixture() {
  return (
    <div className="artifact-card-stand">
      <div className="messages-inner">
        <AssistantMessage
          content={
            'The report is ready, a copy is kept in the session.\n\n<coddy_file id="a1"/>\n<coddy_file id="a2"/>\n<coddy_file id="a3"/>\n\nThe numbers are in the table.'
          }
          artifacts={byId(report, chart, sheet)}
          onMentionArtifact={() => {}}
        />
        <ToolCallMessage
          toolCallId="share-fixture"
          title="share_file"
          status="completed"
          artifacts={[shared]}
        />
        {/* Room enough that the last card can be scrolled to the foot of the window. */}
        <div style={{ height: "110vh" }} aria-hidden="true" />
        <AssistantMessage
          content={'The log of the build.\n\n<coddy_file id="a5"/>'}
          artifacts={byId(last)}
          onMentionArtifact={() => {}}
        />
      </div>
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <Fixture />
  </I18nProvider>,
);

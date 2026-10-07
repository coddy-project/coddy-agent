/**
 * The stand behind `scripts/diagram-check.mjs` and the screenshots of the
 * "Diagrams and formulas" section of docs/surfaces/web-ui.md.
 *
 * Whether Mermaid draws, whether the picture fits the transcript and whether
 * KaTeX's fonts arrive are facts of a real engine, so jsdom cannot assert them.
 * This page mounts an answer holding a flowchart, a sequence diagram, a mindmap, a
 * Gantt chart, a pie, a class diagram, a raw SVG,
 * a broken diagram, inline and display formulas and a price that must stay text,
 * from the real components against the real stylesheet, with no backend.
 * `?theme=<id>` picks the appearance, `?lang=ru` the language, `?case=<name>`
 * mounts one answer of CASES instead of the whole page.
 */
import ReactDOM from "react-dom/client";
import "./styles.css";
import { AssistantMessage } from "./ui/messages/AssistantMessage";
import { I18nProvider } from "./ui/i18n/I18nProvider";
import { initLocale } from "./ui/i18n/i18n";
import { UI_LOCALE_DEFAULT, isUiLocale } from "./ui/i18n/locales";
import { applyUiTheme, resolveUiThemeMode } from "./ui/theme/uiTheme";
import type { UiThemeMode } from "./ui/theme/themeCookie";

const params = new URLSearchParams(location.search);
const lang = params.get("lang") || "";
initLocale(isUiLocale(lang) ? lang : UI_LOCALE_DEFAULT);
applyUiTheme(
  resolveUiThemeMode((params.get("theme") as UiThemeMode | null) || null),
);

const fence = (lang: string, body: string) =>
  ["```" + lang, body, "```"].join("\n");

export const CASES: Record<string, string> = {
  flowchart: [
    "The request path through the server:",
    "",
    fence(
      "mermaid",
      [
        "flowchart LR",
        "  A[Browser] -->|POST /v1/responses| B(HTTP server)",
        "  B --> C{Session busy?}",
        "  C -->|no| D[Agent loop]",
        "  C -->|yes| E[Queue]",
        "  D --> F[(Session store)]",
      ].join("\n"),
    ),
  ].join("\n"),
  sequence: fence(
    "mermaid",
    [
      "sequenceDiagram",
      "  participant U as User",
      "  participant A as Agent",
      "  participant T as Tool",
      "  U->>A: prompt",
      "  A->>T: run_command",
      "  T-->>A: result",
      "  A-->>U: answer",
    ].join("\n"),
  ),
  // Types that may still write HTML labels in a foreignObject: they must
  // draw inside an <img> too, WebKit included.
  mindmap: fence(
    "mermaid",
    [
      "mindmap",
      "  root((Coddy))",
      "    Surfaces",
      "      Web UI",
      "      Console",
      "    Features",
      "      Diagrams",
      "      Formulas",
    ].join("\n"),
  ),
  gantt: fence(
    "mermaid",
    [
      "gantt",
      "  title Release 1.3",
      "  dateFormat YYYY-MM-DD",
      "  section Web UI",
      "  Diagrams    :done, d1, 2026-10-01, 3d",
      "  Formulas    :active, d2, after d1, 2d",
      "  section Docs",
      "  Screenshots :crit, d3, 2026-10-05, 2d",
    ].join("\n"),
  ),
  pie: fence(
    "mermaid",
    [
      "pie title Where the week went",
      '  "Code" : 45',
      '  "Review" : 25',
      '  "Docs" : 20',
      '  "Meetings" : 10',
    ].join("\n"),
  ),
  classes: fence(
    "mermaid",
    [
      "classDiagram",
      "  class Session {",
      "    +id string",
      "    +run(prompt)",
      "  }",
      "  class Message {",
      "    +role string",
      "  }",
      '  Session "1" --> "*" Message',
    ].join("\n"),
  ),
  svg: fence(
    "svg",
    '<svg viewBox="0 0 120 60"><rect x="4" y="4" width="112" height="52" rx="10" fill="#7c3aed"/><text x="60" y="36" font-size="16" text-anchor="middle" fill="#fff">SVG</text></svg>',
  ),
  broken: fence("mermaid", "flowchart LR\n  A --> \n  ((("),
  math: [
    "Mass-energy equivalence $E = mc^2$, and Euler's identity \\(e^{i\\pi} + 1 = 0\\).",
    "",
    "$$",
    "\\int_{-\\infty}^{\\infty} e^{-x^2}\\,dx = \\sqrt{\\pi}",
    "$$",
    "",
    "The same as LaTeX's own delimiters:",
    "",
    "\\[",
    "\\sum_{k=1}^{n} k = \\frac{n(n+1)}{2}",
    "\\]",
    "",
    "A price is not a formula: it costs $5 and $10 a month.",
  ].join("\n"),
  wide: [
    "$$",
    Array.from({ length: 18 }, (_, i) => `a_{${i}} x^{${i}}`).join(" + ") +
      " = 0",
    "$$",
  ].join("\n"),
};

function Fixture() {
  const only = params.get("case");
  const answers = only && CASES[only] ? [CASES[only]] : Object.values(CASES);
  return (
    // The chat column: 14px off either edge on the stacked shell, a centred
    // 920px stripe on the desktop.
    <div
      className="diagram-check-stand"
      style={{
        boxSizing: "border-box",
        width: "100%",
        maxWidth: 920,
        margin: "0 auto",
        padding: "0 14px",
      }}
    >
      <div className="messages-inner">
        {answers.map((text, i) => (
          <AssistantMessage key={i} content={text} showFoot={false} />
        ))}
      </div>
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <Fixture />
  </I18nProvider>,
);

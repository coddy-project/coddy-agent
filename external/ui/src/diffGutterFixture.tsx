/**
 * The stand behind `scripts/diff-gutter-check.mjs`.
 *
 * Where a diff's code starts is a layout fact, so jsdom cannot assert it: only
 * a real engine sizes the line number cells. This page mounts one file of the
 * edits window from the real component against the real stylesheet, in both
 * views, with no backend behind it, so the check needs nothing but `npx vite`.
 *
 * The patch mixes what used to set the code apart: a removal followed by
 * additions (one number cell of each row blank), numbers of two, three and
 * four digits, and a context row whose old and new numbers differ in length.
 */
import ReactDOM from "react-dom/client";
import "./styles.css";
import { DiffFileSection } from "./ui/changes/DiffFileSection";
import type { ChangedFile } from "./ui/changes/types";
import { I18nProvider } from "./ui/i18n/I18nProvider";
import { initLocale } from "./ui/i18n/i18n";
import { UI_LOCALE_DEFAULT, isUiLocale } from "./ui/i18n/locales";

const lang = new URLSearchParams(location.search).get("lang") || "";
initLocale(isUiLocale(lang) ? lang : UI_LOCALE_DEFAULT);

const PATCH = [
  "--- a/README.md",
  "+++ b/README.md",
  "@@ -95,7 +95,8 @@",
  " ## Hosts",
  " ",
  "-- [`ndt-msk-1`](msk01/ndt-msk-1.md)",
  "+- [`ndt-msk-1`](msk01/ndt-msk-1.md) - primary",
  "+- [`ndt-msk-2`](msk01/ndt-msk-2.md)",
  " - [`ndt-msk-3`](msk01/ndt-msk-3.md)",
  " <!-- hosts:start -->",
  " <!-- hosts:end -->",
  " ",
  "@@ -998,4 +999,4 @@",
  " Checks did not change the unit.",
  "-Run disk self-tests weekly.",
  "+Run disk self-tests daily.",
  " Pending workloads move first.",
  " ## Notes",
].join("\n");

const FILE: ChangedFile = {
  path: "README.md",
  status: "modified",
  additions: 3,
  deletions: 2,
  binary: false,
  truncated: false,
};

function Fixture() {
  return (
    <div className="diff-gutter-stand">
      {(["unified", "split"] as const).map((view) => (
        <div key={view} data-view={view}>
          <DiffFileSection
            file={FILE}
            patch={PATCH}
            loading={false}
            error=""
            collapsed={false}
            view={view}
            onToggle={() => {}}
            registerRef={() => {}}
          />
        </div>
      ))}
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <Fixture />
  </I18nProvider>,
);

import { afterEach, expect, test } from "vitest";
import { cleanup, render } from "@testing-library/react";
import { DiffFileSection } from "./DiffFileSection";
import type { DiffView } from "./diffViewPrefs";

afterEach(cleanup);

// A removal followed by an addition, around numbers that grow from two
// digits to three: the rows whose code used to start left of the others.
const PATCH = [
  "--- a/README.md",
  "+++ b/README.md",
  "@@ -98,3 +99,3 @@",
  " keep",
  "-old",
  "+new",
  " tail",
].join("\n");

function renderView(view: DiffView) {
  return render(
    <DiffFileSection
      file={{
        path: "README.md",
        status: "modified",
        additions: 1,
        deletions: 1,
        binary: false,
        truncated: false,
      }}
      patch={PATCH}
      loading={false}
      error=""
      collapsed={false}
      view={view}
      onToggle={() => {}}
      registerRef={() => {}}
    />,
  );
}

// Every number cell takes its width from the file, so the code of a removed
// or an added line (one cell blank) starts where the context's does. Where it
// lands is measured in a real engine by scripts/diff-gutter-check.mjs.
test("every number cell of a diff takes its width from the file's widest number", () => {
  for (const view of ["unified", "split"] as const) {
    const { container, unmount } = renderView(view);
    const diff = container.querySelector<HTMLElement>(".dv-diff");
    expect(diff, view).not.toBeNull();
    // The new column reaches 101 while the old one stops at 100.
    expect(diff!.style.getPropertyValue("--dv-num-digits"), view).toBe("3");
    expect(container.querySelectorAll(".dv-num").length, view).toBeGreaterThan(
      0,
    );
    unmount();
  }
});

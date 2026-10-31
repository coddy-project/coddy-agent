import { afterEach, expect, test, vi } from "vitest";
import { act, cleanup, fireEvent, render } from "@testing-library/react";
import { StrictMode } from "react";
import * as clipboard from "../clipboard";
import { DiffFileSection } from "./DiffFileSection";
import type { DiffView } from "./diffViewPrefs";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

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
    <StrictMode>
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
      />
    </StrictMode>,
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

test("cancels copy feedback timer on unmount", async () => {
  vi.useFakeTimers();
  vi.spyOn(clipboard, "copyText").mockResolvedValue(true);
  const { getByTestId, unmount } = renderView("unified");
  await act(async () => {
    fireEvent.click(getByTestId("dv-copy-README.md"));
  });
  expect(vi.getTimerCount()).toBe(1);
  unmount();
  expect(vi.getTimerCount()).toBe(0);
});

test("does not schedule feedback when copying finishes after unmount", async () => {
  vi.useFakeTimers();
  let resolve!: (ok: boolean) => void;
  vi.spyOn(clipboard, "copyText").mockReturnValue(
    new Promise((r) => {
      resolve = r;
    }),
  );
  const timeout = vi.spyOn(window, "setTimeout");
  const { getByTestId, unmount } = renderView("unified");
  fireEvent.click(getByTestId("dv-copy-README.md"));
  unmount();
  await act(async () => {
    resolve(true);
  });
  expect(timeout).not.toHaveBeenCalled();
  expect(vi.getTimerCount()).toBe(0);
});

test("repeated copies reset the 1200 ms feedback timer", async () => {
  vi.useFakeTimers();
  vi.spyOn(clipboard, "copyText").mockResolvedValue(true);
  const { getByTestId } = renderView("unified");
  const button = getByTestId("dv-copy-README.md");
  await act(async () => {
    fireEvent.click(button);
  });
  act(() => {
    vi.advanceTimersByTime(800);
  });
  await act(async () => {
    fireEvent.click(button);
  });
  expect(vi.getTimerCount()).toBe(1);
  act(() => {
    vi.advanceTimersByTime(1199);
  });
  expect(button.textContent).toBe("✓");
  act(() => {
    vi.advanceTimersByTime(1);
  });
  expect(button.textContent).toBe("⧉");
});

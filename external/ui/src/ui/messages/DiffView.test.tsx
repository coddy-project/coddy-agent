import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { DiffView, DIFF_INITIAL_LINES } from "./DiffView";

afterEach(() => cleanup());

const SIMPLE_PATCH = [
  "--- a/src/foo.ts",
  "+++ b/src/foo.ts",
  "@@ -1,3 +1,4 @@",
  " line1",
  "-removed",
  "+added a",
  "+added b",
  " line4",
].join("\n");

test("DiffView renders file path", () => {
  render(<DiffView patch={SIMPLE_PATCH} filePath="src/foo.ts" />);
  expect(document.querySelector(".diff-file-path")?.textContent).toContain("src/foo.ts");
});

test("DiffView renders add/del/ctx lines with correct classes", () => {
  const { container } = render(<DiffView patch={SIMPLE_PATCH} filePath="src/foo.ts" />);
  const adds = container.querySelectorAll(".diff-line--add");
  const dels = container.querySelectorAll(".diff-line--del");
  const ctxs = container.querySelectorAll(".diff-line--ctx");
  expect(adds.length).toBeGreaterThanOrEqual(2);
  expect(dels.length).toBeGreaterThanOrEqual(1);
  expect(ctxs.length).toBeGreaterThanOrEqual(2);
});

test("DiffView shows old and new line numbers", () => {
  const { container } = render(<DiffView patch={SIMPLE_PATCH} filePath="src/foo.ts" />);
  const nums = Array.from(container.querySelectorAll(".diff-no")).map((el) => el.textContent?.trim());
  // ctx line1: oldNo=1, newNo=1
  expect(nums).toContain("1");
  // del: oldNo=2, newNo empty
  expect(nums).toContain("2");
});

test("DiffView shows hunk header", () => {
  const { container } = render(<DiffView patch={SIMPLE_PATCH} filePath="src/foo.ts" />);
  const header = container.querySelector(".diff-hunk-header");
  expect(header?.textContent).toContain("@@");
});

test("DiffView shows no load-more button when lines <= DIFF_INITIAL_LINES", () => {
  render(<DiffView patch={SIMPLE_PATCH} filePath="src/foo.ts" />);
  expect(screen.queryByTestId("diff-load-more")).toBeNull();
  expect(screen.queryByTestId("diff-hide-link")).toBeNull();
});

function makeLargePatch(lineCount: number): string {
  const body = Array.from({ length: lineCount }, (_, i) => `+new line ${i + 1}`).join("\n");
  return [`--- a/big.ts`, `+++ b/big.ts`, `@@ -0,0 +1,${lineCount} @@`, body].join("\n");
}

test("DiffView shows load-more when lines > DIFF_INITIAL_LINES", () => {
  render(<DiffView patch={makeLargePatch(DIFF_INITIAL_LINES + 5)} filePath="big.ts" />);
  expect(screen.getByTestId("diff-load-more")).toBeInTheDocument();
});

test("DiffView load-more expands all lines; hide restores clipped view", () => {
  const totalLines = DIFF_INITIAL_LINES + 5;
  const { container } = render(
    <DiffView patch={makeLargePatch(totalLines)} filePath="big.ts" />,
  );
  const beforeExpand = container.querySelectorAll(".diff-line").length;
  expect(beforeExpand).toBe(DIFF_INITIAL_LINES);

  fireEvent.click(screen.getByTestId("diff-load-more"));
  expect(container.querySelectorAll(".diff-line").length).toBe(totalLines);
  expect(screen.getByTestId("diff-hide-link")).toBeInTheDocument();

  fireEvent.click(screen.getByTestId("diff-hide-link"));
  expect(container.querySelectorAll(".diff-line").length).toBe(DIFF_INITIAL_LINES);
  expect(screen.getByTestId("diff-load-more")).toBeInTheDocument();
});

test("DiffView viewport class toggles between clip and scroll on expand", () => {
  const { container } = render(
    <DiffView patch={makeLargePatch(DIFF_INITIAL_LINES + 3)} filePath="big.ts" />,
  );
  const block = container.querySelector(".diff-block");
  expect(block?.className).toContain("tool-result-viewport--clip");

  fireEvent.click(screen.getByTestId("diff-load-more"));
  expect(block?.className).toContain("tool-result-viewport--scroll");
  expect(block?.className).not.toContain("tool-result-viewport--clip");
});

test("DiffView with empty patch shows nothing", () => {
  const { container } = render(<DiffView patch="" filePath="" />);
  expect(container.querySelectorAll(".diff-line").length).toBe(0);
  expect(container.querySelectorAll(".diff-hunk-header").length).toBe(0);
});

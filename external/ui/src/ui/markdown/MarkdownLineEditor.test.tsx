import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import {
  MARKDOWN_LINE_EDITOR_MIN_ROWS,
  MarkdownLineEditor,
} from "./MarkdownLineEditor";

afterEach(() => cleanup());

test("default minimum row count is 10", () => {
  expect(MARKDOWN_LINE_EDITOR_MIN_ROWS).toBe(10);
});

test("renders editor chrome", () => {
  render(<MarkdownLineEditor value="a\nb" onChange={() => {}} />);
  expect(document.querySelector(".md-line-editor")).not.toBeNull();
  expect(document.querySelector(".md-line-editor-textarea")).not.toBeNull();
});

test("gutter pads line numbers to minimum rows when text has fewer lines", () => {
  render(<MarkdownLineEditor value="only one line" onChange={() => {}} />);
  const gutter = document.querySelector(".md-line-editor-gutter");
  const nums = Array.from(
    gutter?.querySelectorAll(".md-line-editor-gutter-line") ?? [],
  )
    .map((el) => el.textContent?.trim() ?? "")
    .filter((t) => t.length > 0);
  expect(nums.length).toBe(MARKDOWN_LINE_EDITOR_MIN_ROWS);
  expect(nums[nums.length - 1]).toBe(String(MARKDOWN_LINE_EDITOR_MIN_ROWS));
});

test("gutter grows past minimum when content has more lines", () => {
  const body = Array.from({ length: 14 }, (_, i) => `L${i + 1}`).join("\n");
  render(<MarkdownLineEditor value={body} onChange={() => {}} />);
  const nums = Array.from(
    document.querySelectorAll(".md-line-editor-gutter-line"),
  )
    .map((el) => el.textContent?.trim() ?? "")
    .filter((t) => t.length > 0);
  expect(nums.length).toBe(14);
  expect(nums[13]).toBe("14");
});

test("highlights active line after caret moves", () => {
  render(
    <MarkdownLineEditor
      value={"first\nsecond\nthird"}
      aria-label="Plan body"
      onChange={() => {}}
    />,
  );
  const ta = screen.getByRole("textbox", { name: /plan body/i });
  ta.focus();
  ta.setSelectionRange(8, 8);
  fireEvent.select(ta);
  const currents = document.querySelectorAll(".md-line-editor-hl-band.is-current");
  expect(currents.length).toBeGreaterThanOrEqual(1);
});

test("calls onChange when typing", () => {
  const onChange = vi.fn();
  render(
    <MarkdownLineEditor value="hi" aria-label="Plan body" onChange={onChange} />,
  );
  const ta = screen.getByRole("textbox", { name: /plan body/i });
  fireEvent.change(ta, { target: { value: "hello" } });
  expect(onChange).toHaveBeenCalledWith("hello");
});

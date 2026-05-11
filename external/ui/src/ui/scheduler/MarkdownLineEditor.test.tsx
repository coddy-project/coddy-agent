import React from "react";
import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
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

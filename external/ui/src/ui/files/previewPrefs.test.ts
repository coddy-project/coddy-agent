import { afterEach, expect, test } from "vitest";
import {
  previewKindOf,
  readPreviewPrefs,
  writePreviewPrefs,
} from "./previewPrefs";

afterEach(() => {
  document.cookie = "coddy_files_preview=; Path=/; Max-Age=0";
});

test("SVG, HTML and .canvas files have a preview, other files do not", () => {
  expect(previewKindOf("art/logo.svg")).toBe("svg");
  expect(previewKindOf("design/flow.canvas")).toBe("canvas");
  expect(previewKindOf("site/Index.HTML")).toBe("html");
  expect(previewKindOf("site/page.htm")).toBe("html");
  expect(previewKindOf("site/page.xhtml")).toBe("html");
  expect(previewKindOf("notes.txt")).toBeNull();
  expect(previewKindOf("README.md")).toBeNull();
  expect(previewKindOf("svg")).toBeNull();
});

// An SVG is a picture first, a .canvas file its board, an HTML file source.
test("by default an SVG and a .canvas file show what they draw, an HTML file its source", () => {
  expect(readPreviewPrefs()).toEqual({ svg: true, html: false, canvas: true });
});

test("the choice is remembered in this browser, per kind", () => {
  writePreviewPrefs({ svg: false, html: true, canvas: true });
  expect(document.cookie).toContain("coddy_files_preview=");
  expect(readPreviewPrefs()).toEqual({ svg: false, html: true, canvas: true });
  writePreviewPrefs({ svg: false, html: false, canvas: false });
  expect(readPreviewPrefs()).toEqual({
    svg: false,
    html: false,
    canvas: false,
  });
});

test("a cookie written before .canvas files had a preview draws them", () => {
  document.cookie = "coddy_files_preview=svg; Path=/";
  expect(readPreviewPrefs()).toEqual({ svg: true, html: false, canvas: true });
});

test("a cookie it cannot read gives the defaults back", () => {
  document.cookie = "coddy_files_preview=%E0%A4%A; Path=/";
  expect(readPreviewPrefs()).toEqual({ svg: true, html: false, canvas: true });
});

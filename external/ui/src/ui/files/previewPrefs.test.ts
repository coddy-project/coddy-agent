import { afterEach, expect, test } from "vitest";
import {
  previewKindOf,
  readPreviewPrefs,
  writePreviewPrefs,
} from "./previewPrefs";

afterEach(() => {
  document.cookie = "coddy_files_preview=; Path=/; Max-Age=0";
});

test("SVG and HTML files have a preview, other files do not", () => {
  expect(previewKindOf("art/logo.svg")).toBe("svg");
  expect(previewKindOf("site/Index.HTML")).toBe("html");
  expect(previewKindOf("site/page.htm")).toBe("html");
  expect(previewKindOf("site/page.xhtml")).toBe("html");
  expect(previewKindOf("notes.txt")).toBeNull();
  expect(previewKindOf("README.md")).toBeNull();
  expect(previewKindOf("svg")).toBeNull();
});

// An SVG is a picture first, an HTML file is source first.
test("by default an SVG shows as a picture and an HTML file as its source", () => {
  expect(readPreviewPrefs()).toEqual({ svg: true, html: false });
});

test("the choice is remembered in this browser, per kind", () => {
  writePreviewPrefs({ svg: false, html: true });
  expect(document.cookie).toContain("coddy_files_preview=");
  expect(readPreviewPrefs()).toEqual({ svg: false, html: true });
  writePreviewPrefs({ svg: false, html: false });
  expect(readPreviewPrefs()).toEqual({ svg: false, html: false });
});

test("a cookie it cannot read gives the defaults back", () => {
  document.cookie = "coddy_files_preview=%E0%A4%A; Path=/";
  expect(readPreviewPrefs()).toEqual({ svg: true, html: false });
});

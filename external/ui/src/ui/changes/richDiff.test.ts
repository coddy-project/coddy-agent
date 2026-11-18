import { expect, test } from "vitest";
import {
  formatDiffBytes,
  formatExactDiffBytes,
  richDiffKind,
  sessionChangeImageUrl,
} from "./richDiff";

test("rich diff is offered for images and Markdown only", () => {
  expect(richDiffKind({ path: "picture.png", image: true })).toBe("image");
  expect(richDiffKind({ path: "README.md", image: false })).toBe("markdown");
  expect(richDiffKind({ path: "main.go", image: false })).toBeNull();
});

test("image versions use authenticated API addresses, never file paths directly", () => {
  expect(sessionChangeImageUrl("sess 1", "dir/a b.png", "before")).toBe(
    "/coddy/sessions/sess%201/changes/file?path=dir%2Fa%20b.png&side=before&raw=1",
  );
});

test("image sizes use compact decimal units", () => {
  expect(formatDiffBytes(0, "en")).toBe("0 B");
  expect(formatDiffBytes(999, "en")).toBe("999 B");
  expect(formatDiffBytes(1024, "en")).toBe("1.02 KB");
  expect(formatDiffBytes(174080, "en")).toBe("174 KB");
  expect(formatDiffBytes(1024, "ru")).toBe("1,02 КБ");
  expect(formatDiffBytes(999500, "en")).toBe("1 MB");
  expect(formatExactDiffBytes(1024, "en")).toBe("1,024 B");
});

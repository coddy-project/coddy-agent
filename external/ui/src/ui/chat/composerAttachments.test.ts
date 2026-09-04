import { expect, test } from "vitest";
import {
  clipboardImageFiles,
  imageExtFromMime,
  renamePastedImages,
} from "./composerAttachments";

test("imageExtFromMime maps well-known types and sanitizes odd ones", () => {
  expect(imageExtFromMime("image/png")).toBe("png");
  expect(imageExtFromMime("image/jpeg")).toBe("jpg");
  expect(imageExtFromMime("image/svg+xml")).toBe("svg");
  expect(imageExtFromMime("image/x-weird!type")).toBe("xweirdtype");
  expect(imageExtFromMime("")).toBe("png");
});

test("renamePastedImages names files sequentially per composer", () => {
  const seq = { current: 0 };
  const files = [
    new File(["a"], "image.png", { type: "image/png" }),
    new File(["b"], "image.png", { type: "image/jpeg" }),
  ];
  const out = renamePastedImages(files, seq);
  expect(out.map((f) => f.name)).toEqual(["pasted-1.png", "pasted-2.jpg"]);
  expect(seq.current).toBe(2);
  const more = renamePastedImages(
    [new File(["c"], "image.png", { type: "" })],
    seq,
  );
  expect(more[0]?.name).toBe("pasted-3.png");
  expect(more[0]?.type).toBe("image/png");
});

test("clipboardImageFiles is empty for missing data", () => {
  expect(clipboardImageFiles(null)).toEqual([]);
  expect(clipboardImageFiles(undefined)).toEqual([]);
});

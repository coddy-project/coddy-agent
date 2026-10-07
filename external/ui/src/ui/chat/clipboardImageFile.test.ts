import { expect, test } from "vitest";

import { normalizeClipboardImageFile } from "./clipboardImageFile";

test("infers a PNG MIME type for a pasted file with an empty type", () => {
  const input = new File([new Uint8Array([137, 80, 78, 71])], "pasted-1.png");
  const output = normalizeClipboardImageFile(input);

  expect(output.type).toBe("image/png");
  expect(output.name).toBe("pasted-1.png");
  expect(output.size).toBe(input.size);
});

test("keeps an explicit image MIME type and non-image files unchanged", () => {
  const image = new File(["image"], "shot.jpg", { type: "image/jpeg" });
  const text = new File(["notes"], "notes.txt");

  expect(normalizeClipboardImageFile(image)).toBe(image);
  expect(normalizeClipboardImageFile(text)).toBe(text);
});

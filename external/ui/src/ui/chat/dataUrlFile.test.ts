import { expect, test } from "vitest";
import { fileFromDataUrl } from "./dataUrlFile";

test("a base64 data URI becomes the file it carries", () => {
  const file = fileFromDataUrl("data:image/png;base64,aGVsbG8=", "shot.png");
  expect(file).not.toBeNull();
  expect(file!.name).toBe("shot.png");
  expect(file!.type).toBe("image/png");
  expect(file!.size).toBe(5);
});

test("anything but a base64 data URI is refused, never fetched", () => {
  expect(fileFromDataUrl("https://example.com/shot.png", "shot.png")).toBeNull();
  expect(fileFromDataUrl("data:image/png,raw", "shot.png")).toBeNull();
  expect(fileFromDataUrl("data:image/png;base64,***", "shot.png")).toBeNull();
});

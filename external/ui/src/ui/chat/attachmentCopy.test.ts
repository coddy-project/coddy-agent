import { afterEach, expect, test, vi } from "vitest";
import { inMemoryCopy } from "./attachmentCopy";

afterEach(() => vi.restoreAllMocks());

function diskFile(body: string, name = "shot.png"): File {
  const file = new File([body], name, { type: "image/png", lastModified: 7 });
  Object.defineProperty(file, "arrayBuffer", {
    value: () => Promise.resolve(new TextEncoder().encode(body).buffer),
  });
  return file;
}

test("a copy is a file of its own with the same name, type and date", async () => {
  const source = diskFile("pixels");
  const copy = await inMemoryCopy(source);
  expect(copy).not.toBe(source);
  expect(copy).toBeInstanceOf(File);
  expect([copy.name, copy.type, copy.lastModified, copy.size]).toEqual([
    "shot.png",
    "image/png",
    7,
    6,
  ]);
});

test("a file the browser can no longer read refuses the copy", async () => {
  const source = new File(["x"], "gone.png", { type: "image/png" });
  const reason = new DOMException("gone", "NotReadableError");
  Object.defineProperty(source, "arrayBuffer", {
    value: () => Promise.reject(reason),
  });
  await expect(inMemoryCopy(source)).rejects.toBe(reason);
});

test("without the File API the file stays as it is", async () => {
  const source = new File(["x"], "a.png", { type: "image/png" });
  Object.defineProperty(source, "arrayBuffer", { value: undefined });
  await expect(inMemoryCopy(source)).resolves.toBe(source);
});

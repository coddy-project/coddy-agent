import { afterEach, expect, test, vi } from "vitest";
import { optimisticUserFiles } from "./optimisticUserFiles";

afterEach(() => vi.unstubAllGlobals());

test("image files get a previewUrl object URL and full metadata", () => {
  const createObjectURL = vi.fn(() => "blob:coddy-user-thumb-1");
  vi.stubGlobal("URL", {
    ...URL,
    createObjectURL,
  });
  const f = new File(["data"], "img.png", { type: "image/png" });
  const out = optimisticUserFiles([f]);
  expect(out).toHaveLength(1);
  expect(out[0]).toEqual({
    name: "img.png",
    mimeType: "image/png",
    sizeBytes: f.size,
    previewUrl: "blob:coddy-user-thumb-1",
  });
  expect(createObjectURL).toHaveBeenCalledWith(f);
});

test("non-image files get metadata without previewUrl", () => {
  vi.stubGlobal("URL", { ...URL, createObjectURL: vi.fn() });
  const txt = new File(["data"], "notes.txt", { type: "text/plain" });
  const out = optimisticUserFiles([txt]);
  expect(out[0]).toEqual({
    name: "notes.txt",
    mimeType: "text/plain",
    sizeBytes: txt.size,
  });
  expect("previewUrl" in (out[0] as object)).toBe(false);
});

test("empty MIME type falls back to application/octet-stream", () => {
  vi.stubGlobal("URL", { ...URL, createObjectURL: vi.fn() });
  const out = optimisticUserFiles([new File(["data"], "blob.bin")]);
  expect(out[0]!.mimeType).toBe("application/octet-stream");
  expect("previewUrl" in (out[0] as object)).toBe(false);
});

test("no URL.createObjectURL capability means no previewUrl", () => {
  const urlCtor = URL as unknown as {
    createObjectURL?: (f: File) => string;
  };
  const orig = urlCtor.createObjectURL;
  urlCtor.createObjectURL = undefined;
  try {
    const out = optimisticUserFiles([
      new File(["data"], "img.png", { type: "image/png" }),
    ]);
    expect("previewUrl" in (out[0] as object)).toBe(false);
  } finally {
    urlCtor.createObjectURL = orig;
  }
});

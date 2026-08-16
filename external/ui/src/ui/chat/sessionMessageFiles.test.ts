import { expect, test } from "vitest";
import { sessionMessageFiles } from "./sessionMessageFiles";

test("sessionMessageFiles restores persisted thumbnail metadata", () => {
  expect(
    sessionMessageFiles(
      [
        {
          name: "photo.png",
          mime_type: "image/png",
          preview_url: "/coddy/sessions/sess_a/assets/photo.png/thumbnail",
        },
      ],
      "hello",
    ),
  ).toEqual([
    {
      name: "photo.png",
      mimeType: "image/png",
      previewUrl: "/coddy/sessions/sess_a/assets/photo.png/thumbnail",
    },
  ]);
});

test("sessionMessageFiles falls back to legacy session asset annotations", () => {
  const content = [
    "hello",
    "<coddy_session_assets>",
    "- /tmp/session/assets/notes_1.txt (notes.txt)",
    "</coddy_session_assets>",
  ].join("\n");

  expect(sessionMessageFiles(undefined, content)).toEqual([
    { name: "notes.txt", mimeType: "application/octet-stream" },
  ]);
});

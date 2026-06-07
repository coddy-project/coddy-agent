import { expect, test } from "vitest";
import { stripCoddyAttachmentsForUserDisplay, parseSessionAssetFiles } from "./stripCoddyAttachments";

test("replacing coddy_attachment with @path for display", () => {
  const raw =
    `see below\n\n<coddy_attachment path="docs/readme.txt" name="readme.txt">\n<![CDATA[hello]]>\n</coddy_attachment>`;
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe(
    "see below\n\n@docs/readme.txt",
  );
});

test("decoded XML entities in path attribute", () => {
  const raw = `<coddy_attachment path="odd&quot;x.txt" name="x">\n<![CDATA[]]>\n</coddy_attachment>`;
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe(`@odd"x.txt`);
});

test("strips coddy_session_assets block and preceding newlines", () => {
  const raw =
    "What is in the file?\n\n<coddy_session_assets>Uploaded files saved to session assets (read-only). You can read or copy them:\n- /home/user/.coddy/sessions/s1/assets/note.txt\n</coddy_session_assets>";
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe(
    "What is in the file?",
  );
});

test("strips coddy_session_assets when no preceding newline", () => {
  const raw =
    "<coddy_session_assets>- /some/path.txt\n</coddy_session_assets>";
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe("");
});

test("strips legacy bracket annotation", () => {
  const raw =
    "hello\n\n[Uploaded files saved to session assets (read-only):\n- /path/to/file.txt\nYou can read these files directly or copy them to the workspace as needed.]";
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe("hello");
});

test("parseSessionAssetFiles extracts names from coddy_session_assets", () => {
  const content =
    "msg\n\n<coddy_session_assets>Uploaded files saved to session assets (read-only). You can read or copy them:\n- /home/user/.coddy/sessions/s1/assets/note.txt\n- /home/user/.coddy/sessions/s1/assets/doc_1.txt (doc.txt)\n</coddy_session_assets>";
  const files = parseSessionAssetFiles(content);
  expect(files).toHaveLength(2);
  expect(files[0].name).toBe("note.txt");
  expect(files[1].name).toBe("doc.txt");
});

test("parseSessionAssetFiles returns empty for content without tag", () => {
  expect(parseSessionAssetFiles("plain message")).toHaveLength(0);
});

test("no duplicate @path when user text already mentioned the attachment", () => {
  const raw =
    `@http_todo_report.md что тут?\n\n` +
    `<coddy_attachment path="http_todo_report.md" name="http_todo_report.md">\n` +
    "<![CDATA[# Todo Report]]>\n" +
    `</coddy_attachment>`;
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe(
    "@http_todo_report.md что тут?\n\n",
  );
});

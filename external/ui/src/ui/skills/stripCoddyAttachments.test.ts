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
  expect(files[0]?.name).toBe("note.txt");
  expect(files[1]?.name).toBe("doc.txt");
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

test("attachment with lines attribute collapses to @path:range", () => {
  const raw =
    'see @Dockerfile:21-31\n<coddy_attachment path="Dockerfile" name="Dockerfile" lines="21-31">\n<![CDATA[FROM x]]>\n</coddy_attachment>';
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe("see @Dockerfile:21-31\n");
});

test("ranged attachment renders @path:range when text lacks the mention", () => {
  const raw =
    'look\n<coddy_attachment path="f.go" name="f.go" lines="2-4">\n<![CDATA[x]]>\n</coddy_attachment>';
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe("look\n@f.go:2-4");
});

test("plain and ranged mentions of the same path are distinct", () => {
  const raw =
    'see @f.go:1-2\n<coddy_attachment path="f.go" name="f.go">\n<![CDATA[x]]>\n</coddy_attachment>';
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe("see @f.go:1-2\n@f.go");
});

test("a malformed lines attribute falls back to the plain @path", () => {
  for (const bad of ["9-2", "0-3"]) {
    const raw = `look\n<coddy_attachment path="f.go" name="f.go" lines="${bad}">\n<![CDATA[x]]>\n</coddy_attachment>`;
    expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe("look\n@f.go");
  }
});

/** One element the way mention.Attachment.XML writes it. */
function attachment(attrs: string, body = "x"): string {
  return `<coddy_attachment ${attrs}>\n<![CDATA[${body}]]>\n</coddy_attachment>`;
}

test("a closing tag inside the CDATA body does not end the block", () => {
  const raw = `look\n\n${attachment('path="a.md" name="a.md"', "x </coddy_attachment> y")}\n\nafter`;
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe(
    "look\n\n@a.md\n\nafter",
  );
});

test("a body split into CDATA sections around ]]> is one block", () => {
  const raw = `look\n\n<coddy_attachment path="a.md" name="a.md">\n<![CDATA[x ]]]]><![CDATA[> </coddy_attachment> y]]>\n</coddy_attachment>`;
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe("look\n\n@a.md");
});

test("an opening tag without a well-formed block stays text", () => {
  const raw = 'see <coddy_attachment path="a.md"> and nothing closes it';
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe(raw);
});

test("numeric references in attributes are decoded", () => {
  // encoding/xml.EscapeText writes a quote as &#34;, not &quot;.
  const raw = attachment('path="odd&#34;x&#39;y&amp;lt;.txt" name="x"');
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe(`@odd"x'y&lt;.txt`);
});

test("the mention attribute is what the block collapses to", () => {
  const block = attachment(
    'path="/home/u/notes.md" name="notes.md" mention="~/notes.md"',
  );
  expect(stripCoddyAttachmentsForUserDisplay(`look\n\n${block}`)).toBe(
    "look\n\n@~/notes.md",
  );
  // Typed as the user wrote it: nothing to add.
  expect(
    stripCoddyAttachmentsForUserDisplay(`look at @~/notes.md\n\n${block}`),
  ).toBe("look at @~/notes.md\n\n");
});

test("a rule a mentioned path pulled in is dropped", () => {
  const raw =
    `fix @a.go\n\n${attachment('path="a.go" name="a.go"')}` +
    `\n\n${attachment('path=".cursor/rules/go.mdc" name="go" kind="rule"')}`;
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe("fix @a.go\n\n");
});

test("a rule the user mentioned collapses to its mention", () => {
  const block = attachment(
    'path=".cursor/rules/deploy.mdc" name="deploy" kind="rule" mention="rule:deploy"',
  );
  expect(
    stripCoddyAttachmentsForUserDisplay(`use @rule:deploy\n\n${block}`),
  ).toBe("use @rule:deploy\n\n");
  expect(stripCoddyAttachmentsForUserDisplay(`go\n\n${block}`)).toBe(
    "go\n\n@rule:deploy",
  );
});

test("the body of an invoked skill is dropped", () => {
  const raw = `/review now\n\n${attachment('path="review" name="review" kind="skill"')}`;
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe("/review now\n\n");
});

test("a folder mention covers its listing", () => {
  const raw = `list @src/\n\n${attachment('path="src/" name="src" kind="directory"', "src/a.go")}`;
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe("list @src/\n\n");
});

test("a session mention covers its digest", () => {
  const raw = `as in @session:sess_1\n\n${attachment('path="session:sess_1" name="Earlier work" kind="session"')}`;
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe(
    "as in @session:sess_1\n\n",
  );
});

test("a page mention covers the page it read", () => {
  // Go's mention.ForDisplay does not compare web pages yet (see mentionedBefore).
  const raw = `read @https://x.dev/a.\n\n${attachment('path="https://x.dev/a" name="https://x.dev/a" kind="url"')}`;
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe(
    "read @https://x.dev/a.\n\n",
  );
});

test("only the text before the first block counts as typed", () => {
  const raw = `${attachment('path="a.md" name="a.md"')}\n\nsee @a.md`;
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe("@a.md\n\nsee @a.md");
});

test("the display matches mention.ForDisplay on the Go round trip", () => {
  // internal/mention TestAttachmentXMLRoundTrip: a ranged typed mention whose
  // body holds "]]>" and a closing tag, then a folder nobody typed.
  const raw =
    `look at @~/notes.md:2-3\n\n` +
    attachment(
      'path="/home/u/notes.md" name="notes.md" lines="2-3" mention="~/notes.md"',
      "x ]]]]><![CDATA[> y </coddy_attachment> z",
    ) +
    `\n\n${attachment('path="src/" name="src" kind="directory"', "src/a.go")}`;
  expect(stripCoddyAttachmentsForUserDisplay(raw)).toBe(
    "look at @~/notes.md:2-3\n\n@src/",
  );
});

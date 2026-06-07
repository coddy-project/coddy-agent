import { listAtPathSpans } from "./draftAt";

/** Decodes a minimal subset of XML entities produced by **`encoding/xml.EscapeText`** for attributes. */
function decodeXmlAttrValue(s: string): string {
  return s
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
    .replace(/&amp;/g, "&");
}

function workspacePathsEqual(a: string, b: string): boolean {
  return (
    a.replace(/\\/g, "/").trim() === b.replace(/\\/g, "/").trim()
  );
}

/**
 * True when **`text`** already contains a completed **`@path`** workspace mention for **`path`** (same grammar as **`extractAtFileAttachments`**).
 */
function userBubbleAlreadyShowsAtPath(visiblePrefix: string, path: string): boolean {
  if (!path.trim()) {
    return false;
  }
  for (const sp of listAtPathSpans(visiblePrefix)) {
    if (workspacePathsEqual(sp.path, path)) {
      return true;
    }
  }
  return false;
}

/**
 * Parses **<coddy_session_assets>** block from user message content and returns
 * reconstructed file chip metadata for display.  Used after reload when the
 * original File objects are no longer available.
 */
export function parseSessionAssetFiles(
  content: string,
): { name: string; mimeType: string }[] {
  const m = /<coddy_session_assets>([\s\S]*?)<\/coddy_session_assets>/i.exec(content);
  if (!m) return [];
  const files: { name: string; mimeType: string }[] = [];
  for (const line of m[1].split("\n")) {
    const t = line.trim();
    if (!t.startsWith("- /")) continue;
    const body = t.slice(2); // remove "- "
    const parenIdx = body.indexOf(" (");
    let name: string;
    if (parenIdx >= 0 && body.endsWith(")")) {
      name = body.slice(parenIdx + 2, -1);
    } else {
      name = body.split("/").pop() || "file";
    }
    files.push({ name, mimeType: "application/octet-stream" });
  }
  return files;
}

/**
 * Extracts the raw **<coddy_session_assets>** XML block from user message content,
 * or returns an empty string if none is present.  Used in the edit flow so the
 * block can be re-appended to the edited message before sending.
 */
export function extractSessionAssetsXml(content: string): string {
  const m = /<coddy_session_assets>[\s\S]*?<\/coddy_session_assets>/i.exec(content);
  return m ? m[0] : "";
}

/**
 * Collapses persisted **<coddy_attachment>** blocks for transcript UI.
 * Drops the XML (and hides file bodies); inserts **`@path`** only when the user text portion
 * does not already mention that path (**`composer`** previews duplicate **`@`** otherwise).
 * Also strips **<coddy_session_assets>** blocks and the legacy bracket annotation entirely.
 */
export function stripCoddyAttachmentsForUserDisplay(raw: string): string {
  // Strip <coddy_session_assets> blocks — backend-injected, not for display.
  let s = raw.replace(/\n*<coddy_session_assets>[\s\S]*?<\/coddy_session_assets>/gi, "");
  // Strip legacy bracket annotation from older sessions.
  s = s.replace(
    /\n\n\[Uploaded files saved to session assets \(read-only\):\n[\s\S]*?You can read these files directly or copy them to the workspace as needed\.\]/g,
    "",
  );

  const re =
    /<coddy_attachment\b[^>]*\bpath="([^"]*)"[^>]*>[\s\S]*?<\/coddy_attachment\s*>/gi;
  let rebuilt = "";
  let lastIdx = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(s)) !== null) {
    rebuilt += s.slice(lastIdx, m.index);
    const pathEnc = m[1] ?? "";
    const path = decodeXmlAttrValue(pathEnc).trim();
    if (path !== "" && userBubbleAlreadyShowsAtPath(rebuilt, path)) {
      rebuilt += "";
    } else if (path !== "") {
      rebuilt += `@${path}`;
    }
    lastIdx = m.index + m[0].length;
  }
  rebuilt += s.slice(lastIdx);
  return rebuilt;
}

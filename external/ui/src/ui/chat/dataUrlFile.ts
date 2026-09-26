/**
 * Turns a base64 `data:` URI back into the File a composer attaches: the
 * images of a queued message the operator takes back arrive this way
 * (`inline_files` on the DELETE answer) and go back into the draft.
 *
 * Only a base64 data URI is decoded, never fetched: anything else gives null,
 * so taking a message back can not make the browser request a URL.
 */
export function fileFromDataUrl(dataUrl: string, name: string): File | null {
  const match = /^data:([^;,]*)((?:;[^;,]*)*);base64,([\s\S]*)$/i.exec(dataUrl);
  if (!match) return null;
  try {
    const binary = atob(match[3] ?? "");
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return new File([bytes], name || "image", {
      type: match[1] || "application/octet-stream",
    });
  } catch {
    return null;
  }
}

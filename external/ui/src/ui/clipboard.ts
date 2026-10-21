/**
 * Puts text on the clipboard and says whether it got there: the one copy every
 * text control of the app goes through, so each reports the real outcome.
 *
 * navigator.clipboard exists only in a secure context. A page served over
 * plain HTTP from another machine (a LAN address, a relay) has none, and there
 * the page copies by itself: it selects the text in a text area and runs the
 * copy command. That path is built for iOS Safari, which refuses anything
 * else: it runs before the first await, inside the tap that asked for the copy
 * (call this from the click handler, never after an await of your own), and
 * the text area is read-only at 16px, so the keyboard stays down and the page
 * is not zoomed, and is selected with setSelectionRange, since select() does
 * not select on iOS.
 */
export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Refused (a policy, a page that lost focus): the page tries by itself.
      // This runs after an await, outside the tap, so a browser that ties the
      // copy command to the tap may refuse it too, and the false says so.
    }
  }
  return copyThroughPage(text);
}

function copyThroughPage(text: string): boolean {
  const before = document.activeElement as HTMLElement | null;
  const area = document.createElement("textarea");
  area.value = text;
  area.readOnly = true;
  area.style.cssText = "position:fixed;top:0;left:0;opacity:0;font-size:16px;";
  document.body.appendChild(area);
  try {
    area.focus({ preventScroll: true });
    area.setSelectionRange(0, text.length);
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    area.remove();
    before?.focus({ preventScroll: true });
  }
}

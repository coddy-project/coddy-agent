/**
 * Telegram's back button stands for Escape.
 *
 * Escape already undoes one step everywhere in the web UI (`nav/railEscape.ts`):
 * what is nearer the key answers first - a menu, a picker, a dialog, the image
 * viewer - and a screen of the rail takes only an Escape nothing claimed. So
 * the button dispatches a cancelable Escape at the focused element and every
 * layer answers it through the handler it already has. One listener on
 * `document` in the bubble phase, added just before the dispatch and so run
 * after the rail's, sees whether anything claimed the key; if nothing did, it
 * claims it itself, because the question card listens on `window` and takes an
 * unclaimed Escape as "skip the question", and a conversation that is open is
 * left for the start screen instead.
 *
 * The button is shown while there is something to go back from: a route other
 * than the start screen (a conversation, a screen of the rail, History over a
 * conversation) or a modal layer in the page. With it hidden Telegram shows
 * Close, and Android's back key closes the app.
 */

import { parseAppHash, setSessionHashInLocation } from "../scheduler/hashRoute";
import type { TelegramBridge } from "./bridge";

/**
 * The layers that open over the page and close on Escape: the scrims of the
 * composer's menus and pickers, the confirmation dialog, the image viewer and
 * anything that says it is modal. A layer missing here only leaves Telegram's
 * Close in place of the back button while it is open.
 */
export const TELEGRAM_BACK_LAYERS =
  '.mode-menu-backdrop, .slash-sheet-backdrop, .confirm-dialog-backdrop, .docs-lightbox, [aria-modal="true"]';

/** backButtonWanted reports whether there is something to go back from. */
export function backButtonWanted(doc: Document): boolean {
  const route = parseAppHash();
  if (
    route.branch !== "none" ||
    ("historyOpen" in route && route.historyOpen)
  ) {
    return true;
  }
  return doc.querySelector(TELEGRAM_BACK_LAYERS) !== null;
}

/**
 * pressBack is one press of the back button: an Escape for whatever answers
 * it, and when nothing does, the start screen instead of an open conversation.
 * It returns what happened, for tests.
 */
export function pressBack(
  win: Window,
  doc: Document,
): "escape" | "home" | "nothing" {
  const ev = new KeyboardEvent("keydown", {
    key: "Escape",
    code: "Escape",
    bubbles: true,
    cancelable: true,
  });
  let unclaimed = false;
  const last = (e: Event) => {
    if (e !== ev) {
      return;
    }
    if (!e.defaultPrevented) {
      unclaimed = true;
      // Claimed here so the question card on window never takes it as a skip.
      e.preventDefault();
      e.stopPropagation();
    }
  };
  doc.addEventListener("keydown", last);
  try {
    const target =
      doc.activeElement instanceof HTMLElement ? doc.activeElement : doc.body;
    target.dispatchEvent(ev);
  } finally {
    doc.removeEventListener("keydown", last);
  }
  if (!unclaimed) {
    return "escape";
  }
  if (parseAppHash().branch !== "none" || win.location.hash) {
    setSessionHashInLocation("");
    return "home";
  }
  return "nothing";
}

/**
 * installTelegramBackButton keeps the back button in step with the page and
 * answers its presses; it returns what removes it.
 */
export function installTelegramBackButton(
  bridge: TelegramBridge,
  win: Window = window,
  doc: Document = document,
): () => void {
  let shown: boolean | null = null;
  const sync = () => {
    const want = backButtonWanted(doc);
    if (want !== shown) {
      shown = want;
      bridge.post("web_app_setup_back_button", { is_visible: want });
    }
  };
  const offPress = bridge.on("back_button_pressed", () => {
    pressBack(win, doc);
    // The press may have closed a layer synchronously or changed the route.
    queueMicrotask(sync);
  });
  win.addEventListener("hashchange", sync);
  // Menus, pickers and dialogs are portals: children of <body>.
  const layers = new MutationObserver(sync);
  if (doc.body) {
    layers.observe(doc.body, { childList: true });
  }
  sync();
  return () => {
    offPress();
    win.removeEventListener("hashchange", sync);
    layers.disconnect();
  };
}

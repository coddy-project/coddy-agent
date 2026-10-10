/*
 * Coddy's service worker, registered by src/ui/pwa/serviceWorker.ts at the
 * root of the page, so its scope is the whole web UI.
 *
 * It caches nothing and has no fetch handler: every request goes to the
 * server as it would without it, so a new build is never hidden behind an old
 * copy. It exists for two things a page cannot do on its own:
 *
 * - show a notification where the page cannot construct one (Chrome on
 *   Android) and decide, across every tab of the app, whether one is needed at
 *   all: a tab asks with a "coddy-notify" message, and nothing is shown while
 *   any window of the app has the focus, or twice for the same tag when
 *   several tabs heard the same event;
 * - open the chat a notification is about when it is clicked: the tab that
 *   asked (or any tab of the app) is focused and told which session to open
 *   with a "coddy-open" message, and with no tab left a new window opens on
 *   the chat's address.
 *
 * Contract: DESIGN.md, "Installable app and notifications".
 */

const NOTIFY = "coddy-notify";
const OPEN = "coddy-open";

/** How long a tag counts as shown: the tabs of one browser hear an event at once. */
const SHOWN_TAG_MS = 30 * 1000;

/** Tags shown lately, by the time they were shown. */
const shownTags = new Map();

self.addEventListener("install", () => {
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("message", (event) => {
  const msg = event.data;
  if (!msg || msg.type !== NOTIFY || typeof msg.title !== "string") {
    return;
  }
  const source = event.source && event.source.id ? event.source.id : "";
  event.waitUntil(showForTab(msg, source));
});

async function showForTab(msg, sourceId) {
  const windows = await self.clients.matchAll({
    type: "window",
    includeUncontrolled: true,
  });
  if (windows.some((client) => client.focused)) {
    return;
  }
  const tag = typeof msg.tag === "string" ? msg.tag : "";
  if (tag) {
    const now = Date.now();
    for (const [seen, at] of shownTags) {
      if (now - at > SHOWN_TAG_MS) shownTags.delete(seen);
    }
    if (shownTags.has(tag)) {
      return;
    }
    shownTags.set(tag, now);
  }
  const options = {
    body: typeof msg.body === "string" ? msg.body : "",
    icon: "/icon-192.png",
    data: {
      url: typeof msg.url === "string" ? msg.url : "/",
      sessionId: typeof msg.sessionId === "string" ? msg.sessionId : "",
      env: typeof msg.env === "string" ? msg.env : "",
      client: sourceId,
    },
  };
  if (tag) options.tag = tag;
  if (typeof msg.lang === "string" && msg.lang) options.lang = msg.lang;
  await self.registration.showNotification(msg.title, options);
}

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil(openFromNotification(event.notification.data || {}));
});

async function openFromNotification(data) {
  const windows = await self.clients.matchAll({
    type: "window",
    includeUncontrolled: true,
  });
  const target =
    windows.find((client) => client.id === data.client) || windows[0];
  if (target) {
    try {
      await target.focus();
    } catch {
      /* a browser may refuse; the message still opens the chat */
    }
    target.postMessage({
      type: OPEN,
      sessionId: data.sessionId || "",
      env: data.env || "",
    });
    return;
  }
  await self.clients.openWindow(data.url || "/");
}

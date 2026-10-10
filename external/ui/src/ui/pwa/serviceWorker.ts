/**
 * Registers the service worker of public/sw.js, which makes the web UI an
 * installable app and shows its notifications (notifications.ts), and hears
 * the clicks on them.
 *
 * Browsers run a service worker only in a secure context (https, localhost);
 * elsewhere, or where the registration fails, the web UI works as before and
 * the page shows its notifications itself. The worker caches nothing.
 *
 * Contract: DESIGN.md, "Installable app and notifications".
 */
import { SW_OPEN, adoptServiceWorker, emitOpen } from "./notifications";

let started = false;

/** registerServiceWorker runs once per page, after the app has mounted. */
export function registerServiceWorker(): void {
  if (started || typeof window === "undefined") {
    return;
  }
  started = true;
  if (!window.isSecureContext || !("serviceWorker" in navigator)) {
    return;
  }
  const container = navigator.serviceWorker;
  container.addEventListener("message", (event: MessageEvent) => {
    const data = event.data as
      | { type?: unknown; sessionId?: unknown; env?: unknown }
      | null
      | undefined;
    if (!data || data.type !== SW_OPEN) {
      return;
    }
    emitOpen(
      typeof data.sessionId === "string" ? data.sessionId : "",
      typeof data.env === "string" ? data.env : "",
    );
  });
  container
    .register("/sw.js", { scope: "/" })
    .then((reg) => adoptServiceWorker(reg))
    .catch(() => {
      /* the page notifies by itself */
    });
}

/** resetServiceWorkerForTests forgets the registration between tests. */
export function resetServiceWorkerForTests(): void {
  adoptServiceWorker(null);
  started = false;
}

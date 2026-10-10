/**
 * System notifications of the web UI (issue #508): while Coddy is in the
 * background, the browser says when the agent finished a turn or waits for an
 * answer, and a click on the notification opens that chat.
 *
 * Off until the person turns it on in Settings → Appearance: the switch asks
 * the browser for the permission (which needs a click) and remembers the
 * choice in the cookie `coddy_ui_notify`, client-side like the theme. The
 * browser's permission stays the browser's: the switch is on only while both
 * say so.
 *
 * A notice is shown only when this page does not have the person's attention
 * (hidden, or its window not focused), and, through the service worker, only
 * when no other window of the app has it either (public/sw.js). Every tab of
 * one environment hears the same server events, so a notice is tagged by what
 * it is about and the service worker shows a tag once. Without a service
 * worker (a browser that runs none here) the page shows the notice itself.
 *
 * Contract: DESIGN.md, "Installable app and notifications".
 */
import { envStorageSuffix } from "../env/remoteEnv";

export const CODDY_UI_NOTIFY_COOKIE = "coddy_ui_notify";

const MAX_AGE_SECONDS = 365 * 24 * 60 * 60;

/** The message a tab sends the service worker, and the one it gets back on a click. */
export const SW_NOTIFY = "coddy-notify";
export const SW_OPEN = "coddy-open";

/**
 * NotificationSupport is what this browser can do here: notifications exist
 * only in a secure context (https, localhost), and the permission is the
 * browser's ("denied" is changed in the browser's site settings only).
 */
export type NotificationSupport =
  | "unsupported"
  | "insecure"
  | "default"
  | "granted"
  | "denied";

export function notificationSupport(): NotificationSupport {
  if (typeof window === "undefined") {
    return "unsupported";
  }
  if (typeof Notification === "undefined") {
    return window.isSecureContext === false ? "insecure" : "unsupported";
  }
  const permission = Notification.permission;
  return permission === "granted" || permission === "denied"
    ? permission
    : "default";
}

export function readNotifyCookie(): boolean {
  if (typeof document === "undefined") {
    return false;
  }
  for (const part of document.cookie.split(";")) {
    const s = part.trim();
    if (s.startsWith(`${CODDY_UI_NOTIFY_COOKIE}=`)) {
      return s.slice(CODDY_UI_NOTIFY_COOKIE.length + 1).trim() === "on";
    }
  }
  return false;
}

function writeNotifyCookie(on: boolean): void {
  if (typeof document === "undefined") {
    return;
  }
  if (!on) {
    document.cookie = `${CODDY_UI_NOTIFY_COOKIE}=; Path=/; Max-Age=0; SameSite=Lax`;
    return;
  }
  const secure =
    typeof window !== "undefined" && window.location.protocol === "https:"
      ? "; Secure"
      : "";
  document.cookie = `${CODDY_UI_NOTIFY_COOKIE}=on; Path=/; Max-Age=${MAX_AGE_SECONDS}; SameSite=Lax${secure}`;
}

let workerRegistration: ServiceWorkerRegistration | null = null;

/** adoptServiceWorker hands notices to the registered worker (serviceWorker.ts). */
export function adoptServiceWorker(
  reg: ServiceWorkerRegistration | null,
): void {
  workerRegistration = reg;
}

/** activeServiceWorker is the worker a notice goes to, or null. */
export function activeServiceWorker(): ServiceWorker | null {
  return workerRegistration?.active ?? null;
}

const listeners = new Set<() => void>();

function emit(): void {
  for (const listener of [...listeners]) listener();
}

/** subscribeNotificationState and notificationStateSnapshot drive useSyncExternalStore. */
export function subscribeNotificationState(onChange: () => void): () => void {
  listeners.add(onChange);
  // The permission can change in the browser's own settings while the page is
  // open; the page reads it again when it comes back.
  const recheck = () => onChange();
  if (typeof window !== "undefined") {
    window.addEventListener("focus", recheck);
  }
  return () => {
    listeners.delete(onChange);
    if (typeof window !== "undefined") {
      window.removeEventListener("focus", recheck);
    }
  };
}

export function notificationStateSnapshot(): string {
  return `${readNotifyCookie() ? "on" : "off"}:${notificationSupport()}`;
}

/** notificationsActive: the switch is on and the browser allows them. */
export function notificationsActive(): boolean {
  return readNotifyCookie() && notificationSupport() === "granted";
}

/**
 * enableNotifications runs from the switch's click: it asks the browser when
 * it has not been asked yet, and turns notifications on only when they are
 * allowed. It answers what the browser said.
 */
export async function enableNotifications(): Promise<NotificationSupport> {
  let support = notificationSupport();
  if (support === "default") {
    try {
      const answer = await Notification.requestPermission();
      support =
        answer === "granted" || answer === "denied" ? answer : "default";
    } catch {
      support = notificationSupport();
    }
  }
  writeNotifyCookie(support === "granted");
  emit();
  return support;
}

export function disableNotifications(): void {
  writeNotifyCookie(false);
  emit();
}

/** pageHasAttention: the person is looking at this page right now. */
export function pageHasAttention(): boolean {
  if (typeof document === "undefined") {
    return false;
  }
  return document.visibilityState === "visible" && document.hasFocus();
}

export type AttentionKind =
  | "turn_finished"
  | "turn_failed"
  | "permission"
  | "question"
  | "subagent_permission";

export type AttentionNotice = {
  kind: AttentionKind;
  sessionId: string;
  /**
   * What makes this occurrence one: every tab that hears the same event builds
   * the same key, so the notice is shown once (a turn's start time, a prompt's
   * id). A later occurrence of the same kind gets a key of its own.
   */
  key: string;
  title: string;
  body: string;
};

/** sessionAddress is where a new window opens a chat. */
export function sessionAddress(sessionId: string): string {
  const origin =
    typeof window !== "undefined" ? window.location.origin : "http://localhost";
  return `${origin}/#/s/${encodeURIComponent(sessionId)}`;
}

/** noticeTag names one occurrence in one environment, the same in every tab. */
export function noticeTag(notice: AttentionNotice, env: string): string {
  return `coddy:${env}:${notice.sessionId}:${notice.kind}:${notice.key}`;
}

/**
 * notifyAttention shows the notice when notifications are on and the person
 * is not looking at this page. It answers whether the notice was handed on
 * (to the service worker, which can still decide another window has the
 * focus, or to the browser).
 */
export function notifyAttention(notice: AttentionNotice): boolean {
  if (!notificationsActive() || pageHasAttention()) {
    return false;
  }
  const env = envStorageSuffix();
  const tag = noticeTag(notice, env);
  const lang =
    typeof document !== "undefined" ? document.documentElement.lang : "";
  const worker = activeServiceWorker();
  if (worker) {
    worker.postMessage({
      type: SW_NOTIFY,
      title: notice.title,
      body: notice.body,
      tag,
      sessionId: notice.sessionId,
      env,
      url: sessionAddress(notice.sessionId),
      lang,
    });
    return true;
  }
  try {
    const shown = new Notification(notice.title, {
      body: notice.body,
      tag,
      icon: "/icon-192.png",
      ...(lang ? { lang } : {}),
    });
    shown.onclick = () => {
      try {
        window.focus();
      } catch {
        /* a browser may refuse */
      }
      emitOpen(notice.sessionId, env);
      shown.close();
    };
    return true;
  } catch {
    // Chrome on Android constructs no notification without a service worker.
    return false;
  }
}

const openListeners = new Set<(sessionId: string) => void>();

/**
 * onNotificationOpen subscribes to clicks on a notice of this environment:
 * the listener opens the chat. A notice of another environment only brings
 * the window forward.
 */
export function onNotificationOpen(
  listener: (sessionId: string) => void,
): () => void {
  openListeners.add(listener);
  return () => openListeners.delete(listener);
}

/** emitOpen hands a click on to the listeners when it names this environment. */
export function emitOpen(sessionId: string, env: string): void {
  if (!sessionId || env !== envStorageSuffix()) {
    return;
  }
  for (const listener of [...openListeners]) listener(sessionId);
}

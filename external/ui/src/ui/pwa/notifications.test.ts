import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import {
  CODDY_UI_NOTIFY_COOKIE,
  SW_NOTIFY,
  adoptServiceWorker,
  disableNotifications,
  emitOpen,
  enableNotifications,
  noticeTag,
  notificationSupport,
  notificationsActive,
  notifyAttention,
  onNotificationOpen,
  readNotifyCookie,
  type AttentionNotice,
} from "./notifications";
import { envStorageSuffix } from "../env/remoteEnv";
import {
  installFakeNotification,
  restorePageAttention,
  setPageAttention,
  type FakeNotificationClass,
} from "./notifications.fakes";

const notice: AttentionNotice = {
  kind: "turn_finished",
  sessionId: "sess_a",
  key: "2026-10-10T10:00:00Z",
  title: "Fix the build",
  body: "The agent finished its turn.",
};

let Fake: FakeNotificationClass;

beforeEach(() => {
  Fake = installFakeNotification("granted");
  document.cookie = `${CODDY_UI_NOTIFY_COOKIE}=; Path=/; Max-Age=0`;
  setPageAttention(false);
});

afterEach(() => {
  adoptServiceWorker(null);
  vi.unstubAllGlobals();
  restorePageAttention();
  document.cookie = `${CODDY_UI_NOTIFY_COOKIE}=; Path=/; Max-Age=0`;
});

describe("notificationSupport", () => {
  test("names what the browser allows", () => {
    expect(notificationSupport()).toBe("granted");
    Fake.permission = "denied";
    expect(notificationSupport()).toBe("denied");
    Fake.permission = "default";
    expect(notificationSupport()).toBe("default");
  });

  test("tells an insecure page from a browser without notifications", () => {
    vi.stubGlobal("Notification", undefined);
    // jsdom has no isSecureContext of its own.
    const secure = (value: boolean) =>
      Object.defineProperty(window, "isSecureContext", {
        configurable: true,
        get: () => value,
      });
    try {
      secure(false);
      expect(notificationSupport()).toBe("insecure");
      secure(true);
      expect(notificationSupport()).toBe("unsupported");
    } finally {
      delete (window as { isSecureContext?: boolean }).isSecureContext;
    }
  });
});

describe("the switch", () => {
  test("turning it on asks the browser once and remembers a yes", async () => {
    Fake.permission = "default";
    Fake.requestPermission.mockImplementation(async () => {
      Fake.permission = "granted";
      return "granted";
    });
    expect(await enableNotifications()).toBe("granted");
    expect(Fake.requestPermission).toHaveBeenCalledTimes(1);
    expect(readNotifyCookie()).toBe(true);
    expect(notificationsActive()).toBe(true);

    disableNotifications();
    expect(readNotifyCookie()).toBe(false);
    expect(notificationsActive()).toBe(false);
  });

  test("a refusal leaves it off", async () => {
    Fake.permission = "default";
    Fake.requestPermission.mockImplementation(async () => {
      Fake.permission = "denied";
      return "denied";
    });
    expect(await enableNotifications()).toBe("denied");
    expect(readNotifyCookie()).toBe(false);
  });

  test("a browser that took the permission back turns it off", async () => {
    await enableNotifications();
    expect(notificationsActive()).toBe(true);
    Fake.permission = "denied";
    expect(notificationsActive()).toBe(false);
  });
});

describe("notifyAttention", () => {
  test("says nothing while the switch is off", () => {
    expect(notifyAttention(notice)).toBe(false);
    expect(Fake.instances).toHaveLength(0);
  });

  test("says nothing while the person looks at the page", async () => {
    await enableNotifications();
    setPageAttention(true);
    expect(notifyAttention(notice)).toBe(false);
    expect(Fake.instances).toHaveLength(0);
  });

  test("hands the notice to the service worker with a tag every tab builds alike", async () => {
    await enableNotifications();
    const postMessage = vi.fn();
    adoptServiceWorker({
      active: { postMessage },
    } as unknown as ServiceWorkerRegistration);

    expect(notifyAttention(notice)).toBe(true);
    const env = envStorageSuffix();
    expect(postMessage).toHaveBeenCalledWith(
      expect.objectContaining({
        type: SW_NOTIFY,
        title: "Fix the build",
        body: "The agent finished its turn.",
        tag: `coddy:${env}:sess_a:turn_finished:2026-10-10T10:00:00Z`,
        sessionId: "sess_a",
        env,
        url: `${window.location.origin}/#/s/sess_a`,
      }),
    );
    expect(noticeTag(notice, env)).toBe(
      `coddy:${env}:sess_a:turn_finished:2026-10-10T10:00:00Z`,
    );
    expect(Fake.instances).toHaveLength(0);
  });

  test("without a service worker the page shows it, and a click opens the chat", async () => {
    await enableNotifications();
    const opened: string[] = [];
    const off = onNotificationOpen((sid) => opened.push(sid));
    const focus = vi.spyOn(window, "focus").mockImplementation(() => {});

    expect(notifyAttention(notice)).toBe(true);
    expect(Fake.instances).toHaveLength(1);
    const shown = Fake.instances[0]!;
    expect(shown.title).toBe("Fix the build");
    expect(shown.options.body).toBe("The agent finished its turn.");
    expect(shown.options.icon).toBe("/icon-192.png");

    shown.onclick?.(new Event("click"));
    expect(focus).toHaveBeenCalled();
    expect(opened).toEqual(["sess_a"]);
    expect(shown.close).toHaveBeenCalled();
    off();
    focus.mockRestore();
  });

  test("a click on a notice of another environment opens nothing here", () => {
    const opened: string[] = [];
    const off = onNotificationOpen((sid) => opened.push(sid));
    emitOpen("sess_a", "remote:https://elsewhere.example");
    emitOpen("sess_a", envStorageSuffix());
    expect(opened).toEqual(["sess_a"]);
    off();
  });
});

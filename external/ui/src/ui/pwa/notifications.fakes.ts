/**
 * Test doubles for the browser's Notification API and the page's attention,
 * which jsdom does not have: shared by the notification tests of the pwa
 * module and the App.
 */
import { vi } from "vitest";

export type FakeNotificationInstance = {
  title: string;
  options: NotificationOptions;
  onclick: ((event: Event) => void) | null;
  close: ReturnType<typeof vi.fn>;
};

export type FakeNotificationClass = {
  permission: NotificationPermission;
  requestPermission: ReturnType<typeof vi.fn>;
  instances: FakeNotificationInstance[];
};

/** installFakeNotification stubs window.Notification with the given permission. */
export function installFakeNotification(
  permission: NotificationPermission,
): FakeNotificationClass {
  const instances: FakeNotificationInstance[] = [];
  class FakeNotification {
    static permission: NotificationPermission = permission;
    static requestPermission = vi.fn(async () => FakeNotification.permission);
    static instances = instances;
    title: string;
    options: NotificationOptions;
    onclick: ((event: Event) => void) | null = null;
    close = vi.fn();
    constructor(title: string, options: NotificationOptions = {}) {
      this.title = title;
      this.options = options;
      instances.push(this);
    }
  }
  vi.stubGlobal("Notification", FakeNotification);
  return FakeNotification as unknown as FakeNotificationClass;
}

const attentionSpies: { mockRestore: () => void }[] = [];

/** setPageAttention makes the page look watched (visible and focused) or not. */
export function setPageAttention(watched: boolean): void {
  restorePageAttention();
  attentionSpies.push(
    vi
      .spyOn(document, "visibilityState", "get")
      .mockReturnValue(watched ? "visible" : "hidden"),
    vi.spyOn(document, "hasFocus").mockReturnValue(watched),
  );
}

/**
 * restorePageAttention puts the page's own attention back. Use it rather than
 * vi.restoreAllMocks(), which would also undo the matchMedia stand-in that
 * vitest.setup.ts installs for every test.
 */
export function restorePageAttention(): void {
  for (const spy of attentionSpies.splice(0)) spy.mockRestore();
}

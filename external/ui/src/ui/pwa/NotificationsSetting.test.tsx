import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { NotificationsSetting } from "./NotificationsSetting";
import { AppearanceThemePicker } from "../theme/AppearanceModal";
import { CODDY_UI_NOTIFY_COOKIE, readNotifyCookie } from "./notifications";
import {
  installFakeNotification,
  type FakeNotificationClass,
} from "./notifications.fakes";
import { initLocale } from "../i18n/i18n";
import { telegramLaunch } from "../telegram/launch";

vi.mock("../telegram/launch", () => ({ telegramLaunch: vi.fn(() => null) }));

let Fake: FakeNotificationClass;

beforeEach(() => {
  initLocale("en");
  document.cookie = `${CODDY_UI_NOTIFY_COOKIE}=; Path=/; Max-Age=0`;
  Fake = installFakeNotification("default");
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.mocked(telegramLaunch).mockReturnValue(null);
  document.cookie = `${CODDY_UI_NOTIFY_COOKIE}=; Path=/; Max-Age=0`;
  initLocale("en");
});

function theSwitch(): HTMLInputElement {
  return screen.getByTestId(
    "appearance-notifications-switch",
  ) as HTMLInputElement;
}

describe("Settings → Appearance → Notifications", () => {
  test("stands under the language picker", () => {
    render(<AppearanceThemePicker />);
    const language = screen.getByTestId("appearance-language-picker");
    const notifications = screen.getByTestId("appearance-notifications");
    expect(
      language.compareDocumentPosition(notifications) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.getByText("Notifications")).toBeInTheDocument();
  });

  test("turning the switch on asks the browser and stays on when allowed", async () => {
    Fake.requestPermission.mockImplementation(async () => {
      Fake.permission = "granted";
      return "granted";
    });
    render(<NotificationsSetting />);
    expect(theSwitch()).not.toBeChecked();
    expect(theSwitch()).toBeEnabled();

    await act(async () => {
      fireEvent.click(theSwitch());
    });
    await waitFor(() => expect(theSwitch()).toBeChecked());
    expect(Fake.requestPermission).toHaveBeenCalledTimes(1);
    expect(readNotifyCookie()).toBe(true);

    await act(async () => {
      fireEvent.click(theSwitch());
    });
    expect(theSwitch()).not.toBeChecked();
    expect(readNotifyCookie()).toBe(false);
  });

  test("a refusal leaves it off and says where to allow them", async () => {
    Fake.requestPermission.mockImplementation(async () => {
      Fake.permission = "denied";
      return "denied";
    });
    render(<NotificationsSetting />);
    await act(async () => {
      fireEvent.click(theSwitch());
    });
    await waitFor(() =>
      expect(
        screen.getByTestId("appearance-notifications-status"),
      ).toHaveTextContent("The browser blocks notifications for this site."),
    );
    expect(theSwitch()).not.toBeChecked();
    expect(theSwitch()).toBeDisabled();
  });

  test("a page without notifications says why the switch is off", () => {
    vi.stubGlobal("Notification", undefined);
    render(<NotificationsSetting />);
    expect(theSwitch()).toBeDisabled();
    expect(
      screen.getByTestId("appearance-notifications-status"),
    ).toHaveTextContent("This browser shows no notifications from web pages.");
  });

  test("reads in Russian", () => {
    initLocale("ru");
    render(<NotificationsSetting />);
    expect(screen.getByText("Уведомления")).toBeInTheDocument();
    expect(screen.getByText("Системные уведомления")).toBeInTheDocument();
  });

  test("is not there inside the Telegram Mini App", () => {
    vi.mocked(telegramLaunch).mockReturnValue({} as never);
    render(<NotificationsSetting />);
    expect(screen.queryByTestId("appearance-notifications")).toBeNull();
  });
});

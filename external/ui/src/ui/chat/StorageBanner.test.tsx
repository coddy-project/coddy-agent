import React from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { setLocale } from "../i18n/i18n";
import { messagesEn } from "../i18n/messages/en";
import { messagesRu } from "../i18n/messages/ru";
import { refreshStorageStatus } from "../env/storageStatus";
import { ChatScreen } from "./ChatScreen";
import { StorageBanner, formatStorageSize } from "./StorageBanner";

const MB = 1024 * 1024;
const GB = 1024 * MB;

let storage: unknown = undefined;

beforeEach(() => {
  storage = undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({ object: "coddy.info", version: "1", storage }),
          { status: 200 },
        ),
    ),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  setLocale("en");
});

async function show(next: unknown) {
  storage = next;
  await act(async () => {
    await refreshStorageStatus();
  });
}

const low = (extra: Record<string, unknown> = {}) => ({
  state: "low",
  volume: "sessions",
  freeBytes: 300 * MB,
  totalBytes: 100 * GB,
  minFreeBytes: 512 * MB,
  ...extra,
});

test("a disk with room says nothing, and neither does a server that reports no disk", async () => {
  render(<StorageBanner />);
  await show({ ...low(), state: "ok", freeBytes: 4 * GB });
  expect(screen.queryByTestId("storage-banner")).toBeNull();
  await show(undefined);
  expect(screen.queryByTestId("storage-banner")).toBeNull();
});

test("a low disk is the warn tone, named by the figures, and can be dismissed", async () => {
  render(<StorageBanner />);
  await show(low());
  const banner = await screen.findByTestId("storage-banner");
  expect(banner.getAttribute("data-tone")).toBe("warn");
  expect(banner.getAttribute("role")).toBe("status");
  expect(banner.getAttribute("data-state")).toBe("low");
  expect(banner.className).toContain("storage-banner--warn");
  expect(banner.textContent).toContain(
    "Low disk space: 300 MB left on the disk that stores Coddy sessions.",
  );

  fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
  expect(screen.queryByTestId("storage-banner")).toBeNull();
});

test("the home folder's disk is named when it is the one that is low", async () => {
  render(<StorageBanner />);
  await show(low({ volume: "home", freeBytes: 1.5 * GB }));
  const banner = await screen.findByTestId("storage-banner");
  expect(banner.getAttribute("data-volume")).toBe("home");
  expect(banner.textContent).toContain(
    "Low disk space: 1.5 GB left on the disk that holds the Coddy home folder.",
  );
});

test("a full disk is an alert in the error tone that cannot be dismissed", async () => {
  render(<StorageBanner />);
  await show({ ...low(), state: "full", freeBytes: 0 });
  const banner = await screen.findByTestId("storage-banner");
  expect(banner.getAttribute("data-tone")).toBe("error");
  expect(banner.getAttribute("role")).toBe("alert");
  expect(banner.className).toContain("storage-banner--error");
  expect(banner.textContent).toBe(
    "The disk that stores Coddy sessions is full. New chats cannot start and the latest turns are not saved. Free some space, then send a message again.",
  );
  expect(screen.queryByRole("button")).toBeNull();
});

test("a full disk with no figures still says so", async () => {
  render(<StorageBanner />);
  await show({ state: "full", minFreeBytes: 512 * MB });
  const banner = await screen.findByTestId("storage-banner");
  expect(banner.getAttribute("data-state")).toBe("full");
  expect(banner.textContent).toContain("is full");
});

test("the notice follows the language of the page", async () => {
  setLocale("ru");
  render(<StorageBanner />);
  await show(low());
  const banner = await screen.findByTestId("storage-banner");
  expect(banner.textContent).toContain(
    "Заканчивается место на диске: на диске с сессиями Coddy осталось 300 МБ.",
  );
  fireEvent.click(screen.getByRole("button", { name: "Скрыть" }));
  expect(screen.queryByTestId("storage-banner")).toBeNull();

  await show({ ...low(), state: "full", freeBytes: 0 });
  const full = await screen.findByTestId("storage-banner");
  expect(full.textContent).toContain("Диск с сессиями Coddy заполнен.");
});

test("every state and volume has copy in both languages, and the free figure is placed in the low ones", () => {
  for (const state of ["low", "full"]) {
    for (const volume of ["sessions", "home"]) {
      const key = `storage.banner.${state}.${volume}`;
      for (const dict of [messagesEn, messagesRu]) {
        const text = (dict as Record<string, string>)[key];
        expect(text, key).toBeTruthy();
        expect(text!.includes("{free}"), key).toBe(state === "low");
      }
    }
  }
});

test.each([
  [0, "0 MB"],
  [300 * MB, "300 MB"],
  [300.4 * MB, "300 MB"],
  [GB - 1, "1,024 MB"],
  [GB, "1 GB"],
  [1.5 * GB, "1.5 GB"],
  [1.54 * GB, "1.5 GB"],
  [250 * GB, "250 GB"],
])("%d bytes read as %s in English", (bytes, want) => {
  expect(formatStorageSize(bytes, "en")).toBe(want);
});

test("a size reads in the person's language", () => {
  expect(formatStorageSize(300 * MB, "ru")).toBe("300 МБ");
  expect(formatStorageSize(1.5 * GB, "ru")).toBe("1,5 ГБ");
});

const baseChat = {
  title: "",
  sessionId: "",
  heroAccentVerb: "know" as const,
  heroComposerFocusEpoch: 0,
  onTitleSave: () => {},
  draft: "",
  tokenUsage: null,
  mode: "agent",
  modes: ["agent"],
  onModeChange: () => {},
  onDraftChange: () => {},
  onSend: () => {},
};

// The notice stands where the usage notice stands: above the composer in the
// hero of a new chat and in the docked composer of a running one, and nowhere
// in a subagent transcript, which has no composer to protect.
test("the banner stands above the composer in the hero and in the docked layout", async () => {
  const hero = render(<ChatScreen {...baseChat} items={[]} />);
  await show({ ...low(), state: "full", freeBytes: 0 });
  const heroBanner = await screen.findByTestId("storage-banner");
  expect(heroBanner.closest(".hero-composer")).not.toBeNull();
  const heroField = document.querySelector("textarea#composer") as Node;
  expect(
    heroBanner.compareDocumentPosition(heroField) &
      Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  hero.unmount();

  const docked = render(
    <ChatScreen
      {...baseChat}
      sessionId="sess_a"
      title="Audit"
      items={[{ type: "user_message", id: "u1", content: "audit" }]}
    />,
  );
  const dockedBanner = await screen.findByTestId("storage-banner");
  expect(dockedBanner.closest(".chat-bottom-inner")).not.toBeNull();
  const dockedField = document.querySelector("textarea#composer") as Node;
  expect(
    dockedBanner.compareDocumentPosition(dockedField) &
      Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  docked.unmount();

  // A subagent's transcript has no composer to protect: no notice either.
  render(
    <ChatScreen
      {...baseChat}
      sessionId="sess_0a1b2c"
      title="agent explore"
      items={[{ type: "user_message", id: "1", content: "survey the repo" }]}
      subagentTranscript={{
        parentSessionId: "s_parent",
        name: "explore",
        taskId: "bg_3",
      }}
    />,
  );
  expect(screen.getByTestId("subagent-readonly-notice")).toBeInTheDocument();
  expect(screen.queryByTestId("storage-banner")).toBeNull();
});

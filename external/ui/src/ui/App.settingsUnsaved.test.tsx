import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";
import { resetSettingsConfigForTests } from "./settings/settingsConfigStore";

// Issue #485: the backdrop beside the Settings drawer closes it like its close
// button does, so over unsaved edits it asks first too.

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: () => <div data-testid="chat-screen-stub" />,
}));

const SID = "sess_a";
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
const schema = {
  type: "object",
  "x-coddy-property-order": ["agent"],
  properties: {
    agent: {
      type: "object",
      title: "ReAct loop",
      properties: { max_turns: { type: "integer", title: "Max turns" } },
    },
  },
};

beforeEach(() => {
  resetSettingsConfigForTests();
  initLocale("en");
  localStorage.clear();
  history.replaceState(null, "", "/");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === "/coddy/events") {
        return new Response(
          new ReadableStream<Uint8Array>({ start: () => {} }),
          { headers: { "Content-Type": "text/event-stream" } },
        );
      }
      if (path === "/coddy/config/schema") return json(schema);
      if (path === "/coddy/config") {
        return json({ revision: "r1", agent: { max_turns: 40 } });
      }
      if (path.startsWith("/coddy/sessions?")) {
        return json({
          active_count: 1,
          sessions: [{ id: SID, title: "A chat" }],
        });
      }
      if (path.startsWith(`/coddy/sessions/${SID}/messages`)) {
        return json({ session_id: SID, messages: [] });
      }
      return json({}, 404);
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  history.replaceState(null, "", "/");
});

async function openSettings() {
  history.replaceState(null, "", "/#/settings/agent");
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  return (await screen.findByLabelText("Max turns")) as HTMLInputElement;
}

const clickBackdrop = async () => {
  const backdrop = document.querySelector(".backdrop.is-open");
  expect(backdrop).not.toBeNull();
  await act(async () => {
    fireEvent.click(backdrop!);
  });
};

test("a click on the backdrop beside Settings asks first over unsaved edits", async () => {
  const input = await openSettings();
  fireEvent.change(input, { target: { value: "41" } });
  await clickBackdrop();
  const dialog = screen.getByTestId("settings-close-dialog");
  expect(screen.getByTestId("settings-save")).toBeInTheDocument();
  expect(window.location.hash).toBe("#/settings/agent");

  fireEvent.click(within(dialog).getByRole("button", { name: "Keep editing" }));
  expect(screen.getByTestId("settings-save")).toBeInTheDocument();
});

test("a click on the backdrop beside Settings closes it at once with nothing unsaved", async () => {
  await openSettings();
  await clickBackdrop();
  expect(screen.queryByTestId("settings-save")).toBeNull();
  expect(screen.queryByTestId("settings-close-dialog")).toBeNull();
});

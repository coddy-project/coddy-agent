import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";
import { resetSettingsConfigForTests } from "./settings/settingsConfigStore";

// A Settings address names the setting it opens on (`#/settings/<tab>?field=`):
// the app hands it to the drawer from the first load and from every change of
// the hash, and closing the drawer takes it out of the address.

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
      properties: {
        max_turns: { type: "integer", title: "Max turns" },
        llm_retry_max: { type: "integer", title: "Retries" },
      },
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
        return json({
          revision: "r1",
          agent: { max_turns: 40, llm_retry_max: 3 },
        });
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

const box = (anchor: string) =>
  [...document.querySelectorAll<HTMLElement>("[data-settings-anchor]")].find(
    (b) => b.dataset["settingsAnchor"] === anchor,
  ) ?? null;

function openApp(hash: string) {
  history.replaceState(null, "", `/${hash}`);
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
}

test("a load at an address that names a setting rings it and puts the cursor in it", async () => {
  openApp("#/settings/agent?field=agent%3Amax_turns");

  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;
  await waitFor(() =>
    expect(box("agent:max_turns")!.classList.contains("is-search-target")).toBe(
      true,
    ),
  );
  expect(document.activeElement).toBe(input);
  expect(window.location.hash).toBe("#/settings/agent?field=agent%3Amax_turns");
  expect(
    box("agent:llm_retry_max")!.classList.contains("is-search-target"),
  ).toBe(false);
});

test("a change of the hash takes the open drawer to the setting it names", async () => {
  openApp("#/settings/agent");
  await screen.findByLabelText("Max turns");
  expect(document.querySelector(".is-search-target")).toBeNull();

  await act(async () => {
    window.location.hash = "#/settings/agent?field=agent%3Allm_retry_max";
    await new Promise((resolve) => setTimeout(resolve, 20));
  });

  await waitFor(() =>
    expect(
      box("agent:llm_retry_max")!.classList.contains("is-search-target"),
    ).toBe(true),
  );
  expect(box("agent:llm_retry_max")!.contains(document.activeElement)).toBe(
    true,
  );
  expect(document.activeElement?.tagName).toBe("INPUT");
});

test("closing the drawer takes the setting out of the address", async () => {
  openApp("#/settings/agent?field=agent%3Amax_turns");
  await screen.findByLabelText("Max turns");

  await act(async () => {
    fireEvent.click(screen.getByTestId("settings-drawer-close"));
  });

  expect(screen.queryByTestId("settings-save")).toBeNull();
  expect(window.location.hash).not.toContain("field");
  expect(window.location.hash).not.toContain("settings");
});

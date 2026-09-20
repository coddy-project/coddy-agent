import React from "react";
import { cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { fakeChannels, fakeWorkers } from "./chat/sharedServerEvents.fakes";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

type ChatScreenProps = {
  sessionId: string | undefined;
  sessionLoading: boolean | undefined;
};
const chatScreenRenders: ChatScreenProps[] = [];

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: (props: {
    sessionId?: string;
    sessionLoading?: boolean;
  }) => {
    chatScreenRenders.push({
      sessionId: props.sessionId,
      sessionLoading: props.sessionLoading,
    });
    return <div data-testid="chat-screen-stub" />;
  },
}));

const fetchStub = vi.fn(async (input: RequestInfo | URL) => {
  const url =
    typeof input === "string"
      ? input
      : input instanceof URL
        ? input.href
        : input.url;
  if (url.includes("/coddy/sessions/") && url.endsWith("/messages")) {
    return json({
      messages: [],
      session_id: "sess_x",
      title: "",
    });
  }
  return json({});
});

beforeEach(() => {
  initLocale("en");
  chatScreenRenders.length = 0;
  fakeChannels();
  fakeWorkers(fetchStub);
  vi.stubGlobal("fetch", fetchStub);
  window.location.hash = "#/s/sess_x";
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.location.hash = "";
});

test("boot on #/s/<id> never paints the hero: first render already loads the session", async () => {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  expect(chatScreenRenders.length).toBeGreaterThan(0);
  const first = chatScreenRenders[0];
  expect(first).toBeDefined();
  expect(first?.sessionId).toBe("sess_x");
  expect(first?.sessionLoading).toBe(true);
});

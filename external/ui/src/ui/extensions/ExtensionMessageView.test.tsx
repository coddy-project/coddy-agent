import { afterEach, expect, test, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ExtensionMessageView } from "./ExtensionMessageView";
import { ExtensionRuntime, type ExtensionAPI } from "./runtime";
import { validateMessageRenderer } from "./messageRenderers";

let runtime: ExtensionRuntime | null = null;
vi.mock("./ExtensionsProvider", () => ({
  useExtensions: () => (runtime ? { runtime } : null),
}));
afterEach(() => {
  cleanup();
  runtime = null;
  vi.restoreAllMocks();
});

test("a message renderer draws an ext_frame row in the transcript", async () => {
  runtime = new ExtensionRuntime(() => {});
  const api = {
    id: "demo",
    signal: new AbortController().signal,
  } as ExtensionAPI;
  runtime.messageRenderers.register(
    "demo",
    api,
    {
      id: "usage",
      match: "ext_frame:usage",
      mount: (element, item) => {
        const line = document.createElement("strong");
        line.textContent = `Usage: ${item.data}`;
        element.append(line);
      },
    },
    validateMessageRenderer,
  );
  render(
    <ExtensionMessageView
      item={{
        sessionId: "s1",
        itemId: "row-1",
        type: "ext_frame",
        event: "usage",
        data: "85%",
      }}
    />,
  );
  await screen.findByText("Usage: 85%");
  // An ext_frame row no renderer draws leaves no row behind.
  const dropped = render(
    <ExtensionMessageView
      item={{
        sessionId: "s1",
        itemId: "row-2",
        type: "ext_frame",
        event: "other",
        data: "x",
      }}
    />,
  );
  expect(dropped.container.querySelector(".extension-message-row")).toBeNull();
});

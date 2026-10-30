import { afterEach, expect, test, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ExtensionPermissionPreview } from "./ExtensionPermissionPreview";
import { PermissionPromptSection } from "../chat/PermissionPromptSection";
import { ExtensionRuntime, type ExtensionAPI } from "./runtime";
import { validatePermissionPreview } from "./permissionPreviews";
import type { CoddyPermissionPayload } from "../chat/permissionTypes";

let runtime: ExtensionRuntime | null = null;
vi.mock("./ExtensionsProvider", () => ({
  useExtensions: () => (runtime ? { runtime } : null),
}));
afterEach(() => {
  cleanup();
  runtime = null;
  vi.restoreAllMocks();
});

const payload: CoddyPermissionPayload = {
  sessionId: "sess_x",
  toolCall: {
    toolCallId: "call_1",
    title: "mcp__demo__status",
    kind: "demo__status",
    content: [{ type: "content", content: { type: "text", text: "status" } }],
  },
  options: [
    { optionId: "allow", name: "Allow", kind: "allow_once" },
    { optionId: "reject", name: "Reject", kind: "reject_once" },
  ],
};

function registerPreview() {
  runtime = new ExtensionRuntime(() => {});
  const api = {
    id: "demo",
    signal: new AbortController().signal,
  } as ExtensionAPI;
  runtime.permissionPreviews.register(
    "demo",
    api,
    {
      id: "preview",
      // The call is named in the `server__tool` spelling.
      tool: "demo__status",
      mount: (element) => {
        const note = document.createElement("strong");
        note.textContent = "Extension preview";
        element.append(note);
      },
    },
    validatePermissionPreview,
  );
}

test("the preview appears under the host call preview and the host options remain", () => {
  registerPreview();
  render(
    <PermissionPromptSection
      itemId="pp_1"
      payload={payload}
      onResolved={() => {}}
    />,
  );
  expect(screen.getByText("Extension preview")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Allow" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Reject" })).toBeTruthy();
});

test("a preview of another tool leaves the card alone", () => {
  registerPreview();
  render(
    <ExtensionPermissionPreview
      payload={{
        ...payload,
        toolCall: { ...payload.toolCall, title: "mcp__other__tool" },
      }}
    />,
  );
  expect(screen.queryByText("Extension preview")).toBeNull();
});

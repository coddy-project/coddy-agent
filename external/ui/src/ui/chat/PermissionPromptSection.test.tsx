import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { PermissionPromptSection } from "./PermissionPromptSection";
import type { CoddyPermissionPayload } from "./permissionTypes";

afterEach(() => cleanup());

const payload: CoddyPermissionPayload = {
  sessionId: "sess_x",
  toolCall: {
    toolCallId: "call_1",
    title: "Run: run_command",
    kind: "run_command",
    content: [
      {
        type: "content",
        content: {
          type: "text",
          text: 'Arguments: {"command":"ls -la"}',
        },
      },
    ],
  },
  options: [
    { optionId: "allow", name: "Allow", kind: "allow_once" },
    { optionId: "reject", name: "Reject", kind: "reject_once" },
  ],
};

test("shows human title and command quote without Arguments JSON", () => {
  render(
    <PermissionPromptSection
      itemId="pp_1"
      payload={payload}
      onResolved={() => {}}
    />,
  );
  expect(screen.getByText("Run Command")).toBeTruthy();
  expect(screen.getByText("ls -la")).toBeTruthy();
  expect(screen.queryByText(/Arguments:/)).toBeNull();
  expect(screen.getByTestId("permission-prompt-copy")).toHaveTextContent("Copy");
});

test("resolved permission renders nothing", () => {
  const { container } = render(
    <PermissionPromptSection
      itemId="pp_1"
      payload={payload}
      resolved={{ optionId: "allow", summaryLine: "Allow" }}
      onResolved={() => {}}
    />,
  );
  expect(container.firstChild).toBeNull();
});

test("Allow calls onResolved", async () => {
  const onResolved = vi.fn();
  global.fetch = vi.fn().mockResolvedValue({ ok: true }) as typeof fetch;
  render(
    <PermissionPromptSection
      itemId="pp_1"
      payload={payload}
      onResolved={onResolved}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Allow" }));
  await vi.waitFor(() => expect(onResolved).toHaveBeenCalled());
});

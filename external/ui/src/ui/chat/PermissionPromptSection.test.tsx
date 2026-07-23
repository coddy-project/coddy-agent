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
    { optionId: "allow_always", name: "Allow always", kind: "allow_always" },
    { optionId: "reject", name: "Reject", kind: "reject_once" },
  ],
};

test("shows a human question, one technical tool badge, and the original buttons", () => {
  render(
    <PermissionPromptSection
      itemId="pp_1"
      payload={payload}
      onResolved={() => {}}
    />,
  );
  expect(screen.getByText("Run this command?")).toBeTruthy();
  expect(screen.getAllByText("run_command")).toHaveLength(1);
  expect(screen.getByText("ls -la")).toBeTruthy();
  expect(screen.queryByText(/Arguments:/)).toBeNull();
  expect(screen.getByTestId("permission-prompt-copy")).toHaveTextContent(
    "Copy",
  );
  expect(screen.getByRole("button", { name: "Allow" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Allow always" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Reject" })).toBeTruthy();
});

test("renders apply_patch as a colored inline diff", () => {
  const patchPayload: CoddyPermissionPayload = {
    ...payload,
    toolCall: {
      ...payload.toolCall,
      title: "Run: apply_patch",
      kind: "write",
      content: [
        {
          type: "content",
          content: {
            type: "text",
            text: `Arguments: ${JSON.stringify({
              path: "src/app.ts",
              patch: [
                "--- a/src/app.ts",
                "+++ b/src/app.ts",
                "@@ -4,3 +4,3 @@",
                " before();",
                "-oldValue();",
                "+newValue();",
                " after();",
              ].join("\n"),
            })}`,
          },
        },
      ],
    },
  };
  const { container } = render(
    <PermissionPromptSection
      itemId="pp_patch"
      payload={patchPayload}
      onResolved={() => {}}
    />,
  );
  expect(screen.getByText("Apply this patch?")).toBeTruthy();
  expect(screen.getAllByText("apply_patch")).toHaveLength(1);
  expect(screen.getByLabelText("Patch preview")).toBeTruthy();
  expect(container.querySelectorAll(".diff-line--ctx")).toHaveLength(2);
  expect(container.querySelectorAll(".diff-line--del")).toHaveLength(1);
  expect(container.querySelectorAll(".diff-line--add")).toHaveLength(1);
  expect(
    Array.from(container.querySelectorAll(".diff-no")).map((node) =>
      node.textContent?.trim(),
    ),
  ).toEqual(["4", "4", "5", "", "", "5", "6", "6"]);
});

test("shows More only when the preview actually overflows and toggles to Less", () => {
  const scrollHeight = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "scrollHeight",
  );
  const clientHeight = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "clientHeight",
  );
  Object.defineProperty(HTMLElement.prototype, "scrollHeight", {
    configurable: true,
    get() {
      return (this as HTMLElement).dataset.testid ===
        "permission-preview-viewport"
        ? 220
        : 0;
    },
  });
  Object.defineProperty(HTMLElement.prototype, "clientHeight", {
    configurable: true,
    get() {
      return (this as HTMLElement).dataset.testid ===
        "permission-preview-viewport"
        ? 120
        : 0;
    },
  });
  try {
    render(
      <PermissionPromptSection
        itemId="pp_more"
        payload={payload}
        onResolved={() => {}}
      />,
    );
    const more = screen.getByRole("button", { name: "More…" });
    expect(more).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(more);
    expect(screen.getByRole("button", { name: "Less" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  } finally {
    if (scrollHeight) {
      Object.defineProperty(
        HTMLElement.prototype,
        "scrollHeight",
        scrollHeight,
      );
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, "scrollHeight");
    }
    if (clientHeight) {
      Object.defineProperty(
        HTMLElement.prototype,
        "clientHeight",
        clientHeight,
      );
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, "clientHeight");
    }
  }
});

test("does not show More when a preview fits", () => {
  render(
    <PermissionPromptSection
      itemId="pp_short"
      payload={payload}
      onResolved={() => {}}
    />,
  );
  expect(screen.queryByRole("button", { name: "More…" })).toBeNull();
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

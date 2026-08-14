import React from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { ConfirmProvider, useConfirm } from "./useConfirm";
import { I18nProvider } from "../i18n/I18nProvider";
import { initLocale } from "../i18n/i18n";

beforeEach(() => initLocale("en"));

afterEach(() => {
  cleanup();
  initLocale("en");
});

function Harness({ onResult }: { onResult: (ok: boolean) => void }) {
  const confirm = useConfirm();
  return (
    <button
      onClick={async () => {
        const ok = await confirm({
          title: "Delete chat?",
          message: "This will be deleted.",
          confirmLabel: "Delete",
          variant: "danger",
        });
        onResult(ok);
      }}
    >
      trigger
    </button>
  );
}

test("confirm resolves true when the confirm button is clicked", async () => {
  const onResult = vi.fn();
  render(
    <ConfirmProvider>
      <Harness onResult={onResult} />
    </ConfirmProvider>,
  );

  fireEvent.click(screen.getByText("trigger"));
  // Dialog is now open.
  await screen.findByRole("dialog");
  fireEvent.click(screen.getByRole("button", { name: "Delete" }));

  await vi.waitFor(() => expect(onResult).toHaveBeenCalledWith(true));
});

test("confirm resolves false when cancel is clicked", async () => {
  const onResult = vi.fn();
  render(
    <ConfirmProvider>
      <Harness onResult={onResult} />
    </ConfirmProvider>,
  );

  fireEvent.click(screen.getByText("trigger"));
  await screen.findByRole("dialog");
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

  await vi.waitFor(() => expect(onResult).toHaveBeenCalledWith(false));
});

test("provider localizes the default cancel action", async () => {
  initLocale("ru");
  const onResult = vi.fn();
  render(
    <I18nProvider>
      <ConfirmProvider>
        <Harness onResult={onResult} />
      </ConfirmProvider>
    </I18nProvider>,
  );

  fireEvent.click(screen.getByText("trigger"));
  await screen.findByRole("dialog");
  fireEvent.click(screen.getByRole("button", { name: "Отмена" }));

  await vi.waitFor(() => expect(onResult).toHaveBeenCalledWith(false));
});

test("confirm resolves false on Escape", async () => {
  const onResult = vi.fn();
  render(
    <ConfirmProvider>
      <Harness onResult={onResult} />
    </ConfirmProvider>,
  );

  fireEvent.click(screen.getByText("trigger"));
  await screen.findByRole("dialog");
  fireEvent.keyDown(window, { key: "Escape" });

  await vi.waitFor(() => expect(onResult).toHaveBeenCalledWith(false));
});

test("dialog is reused across sequential confirms", async () => {
  const onResult = vi.fn();
  render(
    <ConfirmProvider>
      <Harness onResult={onResult} />
    </ConfirmProvider>,
  );

  // First round: confirm.
  fireEvent.click(screen.getByText("trigger"));
  await screen.findByRole("dialog");
  fireEvent.click(screen.getByRole("button", { name: "Delete" }));
  await vi.waitFor(() => expect(onResult).toHaveBeenCalledWith(true));
  expect(screen.queryByRole("dialog")).toBeNull();

  // Second round: cancel — same provider instance.
  onResult.mockClear();
  fireEvent.click(screen.getByText("trigger"));
  await screen.findByRole("dialog");
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await vi.waitFor(() => expect(onResult).toHaveBeenCalledWith(false));
});

test("a second confirm() supersedes the open one instead of hanging it", async () => {
  const onResult = vi.fn();
  render(
    <ConfirmProvider>
      <Harness onResult={onResult} />
    </ConfirmProvider>,
  );

  // Two overlapping confirms: the first must settle (false), not await forever.
  fireEvent.click(screen.getByText("trigger"));
  await screen.findByRole("dialog");
  fireEvent.click(screen.getByText("trigger"));
  await vi.waitFor(() => expect(onResult).toHaveBeenCalledWith(false));

  // The surviving dialog still resolves its own caller.
  onResult.mockClear();
  fireEvent.click(screen.getByRole("button", { name: "Delete" }));
  await vi.waitFor(() => expect(onResult).toHaveBeenCalledWith(true));
});

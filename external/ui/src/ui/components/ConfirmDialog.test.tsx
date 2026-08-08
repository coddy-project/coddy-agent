import React from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ConfirmDialog } from "./ConfirmDialog";

afterEach(() => cleanup());

const baseProps = {
  open: true,
  title: "Delete chat?",
  confirmLabel: "Delete",
  onConfirm: () => {},
  onCancel: () => {},
  ariaLabel: "Delete chat?",
};

test("renders nothing when closed", () => {
  const { container } = render(<ConfirmDialog {...baseProps} open={false} />);
  expect(container.firstChild).toBeNull();
  expect(document.querySelector(".confirm-dialog")).toBeNull();
});

test("renders title, message, and labelled buttons when open", () => {
  render(
    <ConfirmDialog
      {...baseProps}
      message="This conversation will be permanently deleted."
    />,
  );
  expect(screen.getByText("Delete chat?")).toBeTruthy();
  expect(
    screen.getByText("This conversation will be permanently deleted."),
  ).toBeTruthy();
  expect(
    screen.getByRole("button", { name: "Delete" }),
  ).toBeTruthy();
  expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
});

test("confirm click calls onConfirm", () => {
  const onConfirm = vi.fn();
  render(<ConfirmDialog {...baseProps} onConfirm={onConfirm} />);
  fireEvent.click(screen.getByRole("button", { name: "Delete" }));
  expect(onConfirm).toHaveBeenCalledTimes(1);
});

test("cancel click calls onCancel", () => {
  const onCancel = vi.fn();
  render(<ConfirmDialog {...baseProps} onCancel={onCancel} />);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(onCancel).toHaveBeenCalledTimes(1);
});

test("Escape key calls onCancel", () => {
  const onCancel = vi.fn();
  render(<ConfirmDialog {...baseProps} onCancel={onCancel} />);
  fireEvent.keyDown(window, { key: "Escape" });
  expect(onCancel).toHaveBeenCalledTimes(1);
});

test("backdrop click calls onCancel", () => {
  const onCancel = vi.fn();
  render(<ConfirmDialog {...baseProps} onCancel={onCancel} />);
  const backdrop = document.querySelector(
    ".confirm-dialog-backdrop",
  ) as HTMLElement;
  fireEvent.mouseDown(backdrop);
  expect(onCancel).toHaveBeenCalledTimes(1);
});

test("click inside the dialog card does not cancel", () => {
  const onCancel = vi.fn();
  render(<ConfirmDialog {...baseProps} onCancel={onCancel} />);
  fireEvent.mouseDown(screen.getByText("Delete chat?"));
  expect(onCancel).not.toHaveBeenCalled();
});

test("danger variant marks the confirm button as destructive", () => {
  render(<ConfirmDialog {...baseProps} variant="danger" />);
  const confirm = screen.getByRole("button", { name: "Delete" });
  expect(confirm).toHaveClass("confirm-dialog-btn--danger");
});

test("primary variant marks the confirm button as primary", () => {
  render(
    <ConfirmDialog
      {...baseProps}
      variant="primary"
      confirmLabel="Continue"
    />,
  );
  const confirm = screen.getByRole("button", { name: "Continue" });
  expect(confirm).not.toHaveClass("confirm-dialog-btn--danger");
  expect(confirm).toHaveClass("confirm-dialog-btn--primary");
});

test("confirming flag disables the action buttons", () => {
  render(<ConfirmDialog {...baseProps} confirming />);
  expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
});

test("dialog exposes role dialog with aria-modal true", () => {
  render(<ConfirmDialog {...baseProps} />);
  const dialog = screen.getByRole("dialog");
  expect(dialog).toHaveAttribute("aria-modal", "true");
});

test("Escape does not reach the drawer/sheet handler underneath", () => {
  // App.tsx keeps a window keydown listener that closes the sessions drawer and
  // the scheduler editor sheet on Escape. Both are the very surfaces this dialog
  // opens from, so dismissing the dialog must not close them too.
  const underneath = vi.fn();
  window.addEventListener("keydown", underneath);
  try {
    const onCancel = vi.fn();
    render(<ConfirmDialog {...baseProps} onCancel={onCancel} />);
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(underneath).not.toHaveBeenCalled();
  } finally {
    window.removeEventListener("keydown", underneath);
  }
});

test("Tab wraps inside the dialog instead of escaping to the page", () => {
  const outside = document.createElement("button");
  outside.textContent = "outside";
  document.body.appendChild(outside);
  try {
    render(<ConfirmDialog {...baseProps} variant="danger" />);
    const cancel = screen.getByRole("button", { name: "Cancel" });
    const confirm = screen.getByRole("button", { name: "Delete" });

    confirm.focus();
    fireEvent.keyDown(confirm, { key: "Tab" });
    expect(document.activeElement).toBe(cancel);

    fireEvent.keyDown(cancel, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(confirm);
  } finally {
    outside.remove();
  }
});

test("focus returns to the opener when the dialog closes", () => {
  const opener = document.createElement("button");
  document.body.appendChild(opener);
  opener.focus();
  try {
    const view = render(<ConfirmDialog {...baseProps} />);
    view.rerender(<ConfirmDialog {...baseProps} open={false} />);
    expect(document.activeElement).toBe(opener);
  } finally {
    opener.remove();
  }
});

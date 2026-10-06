import React from "react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Composer } from "./Composer";
import { initLocale } from "../i18n/i18n";

/**
 * Editing a sent message is a state of the composer the operator can see:
 * a banner names the message, the send button says what sending does, and
 * Escape or the cross leaves the edit. After the edit is sent the same slot
 * offers to take it back.
 */

beforeEach(() => initLocale("en"));
afterEach(() => cleanup());

function renderEditing(onCancel = vi.fn(), value = "second question") {
  render(
    <Composer
      value={value}
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
      editingMessage={{ snippet: "second question", onCancel }}
    />,
  );
  return onCancel;
}

describe("edit banner", () => {
  test("names the message being edited", () => {
    renderEditing();
    const banner = screen.getByTestId("composer-edit-banner");
    expect(banner).toHaveTextContent("Editing message");
    expect(banner).toHaveTextContent("second question");
  });

  test("the send button says it sends the edit", () => {
    renderEditing();
    expect(document.getElementById("btn-send")).toHaveAttribute(
      "aria-label",
      "Send edit",
    );
  });

  test("Escape in the field leaves the edit", () => {
    const onCancel = renderEditing();
    fireEvent.keyDown(screen.getByRole("textbox"), { key: "Escape" });
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  test("the cross leaves the edit", () => {
    const onCancel = renderEditing();
    fireEvent.click(screen.getByTestId("composer-edit-cancel"));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  test("Escape during IME composition does not leave the edit", () => {
    const onCancel = renderEditing();
    const field = screen.getByRole("textbox");
    fireEvent.keyDown(field, { key: "Escape", isComposing: true });
    expect(onCancel).not.toHaveBeenCalled();
  });

  test("without an edit there is no banner and Escape does nothing", () => {
    render(
      <Composer
        value="hello"
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={() => {}}
        onSend={() => {}}
      />,
    );
    expect(screen.queryByTestId("composer-edit-banner")).toBeNull();
    expect(document.getElementById("btn-send")).toHaveAttribute("aria-label", "Send");
  });
});

describe("undo banner", () => {
  test("offers to take the sent edit back and can be put away", () => {
    const onUndo = vi.fn();
    const onDismiss = vi.fn();
    render(
      <Composer
        value=""
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={() => {}}
        onSend={() => {}}
        rewindUndo={{ onUndo, onDismiss }}
      />,
    );
    const banner = screen.getByTestId("composer-undo-banner");
    expect(banner).toHaveTextContent("Message edited");
    expect(banner).toHaveTextContent("file changes are not reverted");
    fireEvent.click(screen.getByTestId("composer-undo-edit"));
    expect(onUndo).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTestId("composer-undo-dismiss"));
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  test("an edit in progress wins the slot", () => {
    render(
      <Composer
        value="x"
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={() => {}}
        onSend={() => {}}
        editingMessage={{ snippet: "x", onCancel: () => {} }}
        rewindUndo={{ onUndo: () => {}, onDismiss: () => {} }}
      />,
    );
    expect(screen.getByTestId("composer-edit-banner")).toBeInTheDocument();
    expect(screen.queryByTestId("composer-undo-banner")).toBeNull();
  });
});

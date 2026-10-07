import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { useRightDock, useRightDockEscape } from "./useRightDock";
afterEach(cleanup);

test("Escape closes the shared dock and returns focus to its opener", () => {
  function Stand() {
    const dock = useRightDock();
    useRightDockEscape(dock.open, () => dock.setOpen(false));
    return (
      <>
        <button onClick={() => dock.setOpen(true)}>Open files</button>
        {dock.open ? <aside>Files</aside> : null}
      </>
    );
  }
  render(<Stand />);
  const opener = screen.getByRole("button");
  opener.focus();
  fireEvent.click(opener);
  fireEvent.keyDown(document, { key: "Escape" });
  expect(screen.queryByText("Files")).toBeNull();
  expect(document.activeElement).toBe(opener);
});

test("a menu's claimed Escape does not close the dock behind it", () => {
  const close = vi.fn();
  function Stand() {
    useRightDockEscape(true, close);
    return <button onKeyDown={(e) => e.preventDefault()}>Menu</button>;
  }
  render(<Stand />);
  fireEvent.keyDown(screen.getByRole("button"), { key: "Escape" });
  expect(close).not.toHaveBeenCalled();
});

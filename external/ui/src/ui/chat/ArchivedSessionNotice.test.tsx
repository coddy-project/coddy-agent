import React from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ArchivedSessionNotice } from "./ArchivedSessionNotice";

afterEach(() => cleanup());

test("it says why there is nothing to type into, and offers the way out", () => {
  const onUnarchive = vi.fn();
  render(<ArchivedSessionNotice onUnarchive={onUnarchive} />);

  const notice = screen.getByTestId("archived-session-notice");
  expect(notice).toHaveTextContent("archived");
  fireEvent.click(screen.getByTestId("archived-session-unarchive"));
  expect(onUnarchive).toHaveBeenCalledTimes(1);
});

test("the action cannot be pressed twice while it is in flight", () => {
  const onUnarchive = vi.fn();
  render(<ArchivedSessionNotice onUnarchive={onUnarchive} busy />);

  const button = screen.getByTestId("archived-session-unarchive");
  expect(button).toBeDisabled();
  fireEvent.click(button);
  expect(onUnarchive).not.toHaveBeenCalled();
});

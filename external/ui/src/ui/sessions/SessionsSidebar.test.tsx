import React from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { SessionsSidebar } from "./SessionsSidebar";
import type { SessionRow } from "./types";

afterEach(() => cleanup());

const row = (id: string, title: string): SessionRow => ({
  id,
  title,
});

test("delete click does not bubble to row pick", async () => {
  const onPick = vi.fn();
  const onDelete = vi.fn().mockResolvedValue(undefined);
  render(
    <SessionsSidebar
      sessionId="current"
      sessions={[row("current", "A"), row("other", "B")]}
      open
      onPick={onPick}
      onDelete={onDelete}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
    />,
  );
  fireEvent.click(screen.getByTestId("session-delete-other"));
  expect(onDelete).toHaveBeenCalledTimes(1);
  expect(onDelete).toHaveBeenCalledWith("other");
  expect(onPick).not.toHaveBeenCalled();
});

test("clicking session row outside the text picks the session", () => {
  const onPick = vi.fn();
  render(
    <SessionsSidebar
      sessionId="current"
      sessions={[row("current", "A"), row("other", "B")]}
      open
      onPick={onPick}
      onDelete={() => Promise.resolve()}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
    />,
  );

  fireEvent.click(screen.getByTestId("session-row-other"));

  expect(onPick).toHaveBeenCalledTimes(1);
  expect(onPick).toHaveBeenCalledWith("other");
});

test("session row is a link with session hash href", () => {
  render(
    <SessionsSidebar
      sessionId="current"
      sessions={[row("sess-one", "Alpha")]}
      open
      onPick={() => {}}
      onDelete={() => Promise.resolve()}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
    />,
  );
  const link = screen.getByRole("link", { name: /Alpha/i });
  expect(link).toHaveAttribute("href", "#/s/sess-one");
});

test("draft session row links to #/draft/<id>", () => {
  render(
    <SessionsSidebar
      sessionId="draft_1"
      sessions={[row("draft_1", "Draft: hello")]}
      open
      onPick={() => {}}
      onDelete={() => Promise.resolve()}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
    />,
  );
  const link = screen.getByRole("link", { name: /Draft: hello/i });
  expect(link).toHaveAttribute("href", "#/draft/draft_1");
});

test("shows spinner and unread dot for other sessions", () => {
  render(
    <SessionsSidebar
      sessionId="current"
      sessions={[
        { id: "current", title: "A" },
        {
          id: "busy",
          title: "B",
          turnActive: true,
          unreadComplete: true,
        },
      ]}
      open
      onPick={() => {}}
      onDelete={() => Promise.resolve()}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
    />,
  );
  expect(screen.getByTestId("session-spinner-busy")).toBeInTheDocument();
  expect(screen.getByTestId("session-unread-busy")).toBeInTheDocument();
  expect(screen.queryByTestId("session-spinner-current")).toBeNull();
});

test("question pending hides spinner and shows animated question icon", () => {
  render(
    <SessionsSidebar
      sessionId="current"
      sessions={[
        { id: "current", title: "A" },
        { id: "q", title: "B", turnActive: true },
      ]}
      questionPendingSessionIds={new Set(["q"])}
      open
      onPick={() => {}}
      onDelete={() => Promise.resolve()}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
    />,
  );
  expect(screen.queryByTestId("session-spinner-q")).toBeNull();
  expect(screen.getByTestId("session-question-q")).toBeInTheDocument();
});

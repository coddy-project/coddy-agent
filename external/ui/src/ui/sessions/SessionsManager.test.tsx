import React from "react";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { ConfirmProvider } from "../components/useConfirm";
import { SessionsManager } from "./SessionsManager";
import type { SessionManagerRow } from "./sessionManagerRows";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const rows: SessionManagerRow[] = [
  {
    id: "sess_one",
    title: "Refactor the parser",
    cwd: "/home/u/projects/coddy",
    createdAt: "2026-09-01T10:00:00Z",
    updatedAt: "2026-09-02T10:00:00Z",
    model: "openai/gpt-4o",
    messageCount: 12,
    tokenUsage: { inputTokens: 1200, outputTokens: 300, totalTokens: 1500 },
  },
  {
    id: "sess_two",
    title: "Ship the release",
    cwd: "/home/u/projects/site",
    updatedAt: "2026-09-03T10:00:00Z",
    messageCount: 4,
  },
];

type Call = { url: string; init?: RequestInit };

/**
 * stubFetch answers the two routes the table uses and records what it was
 * asked, so a test can assert the request body rather than the rendering only.
 */
function stubFetch(deleted: string[] = []) {
  const calls: Call[] = [];
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    calls.push({ url, ...(init ? { init } : {}) });
    if (url.startsWith("/coddy/sessions/bulk-delete")) {
      return {
        ok: true,
        json: async () => ({ deleted, failed: [] }),
      } as unknown as Response;
    }
    const remaining = rows.filter((r) => !deleted.includes(r.id));
    const body = calls.some((c) =>
      c.url.startsWith("/coddy/sessions/bulk-delete"),
    )
      ? remaining
      : rows;
    return {
      ok: true,
      json: async () => ({ sessions: body, hasMore: false, nextCursor: null }),
    } as unknown as Response;
  });
  vi.stubGlobal("fetch", fetchMock);
  return calls;
}

function renderTable(props: Parameters<typeof SessionsManager>[0] = {}) {
  return render(
    <ConfirmProvider>
      <SessionsManager {...props} />
    </ConfirmProvider>,
  );
}

/** Clicks the affirmative button of the shared confirmation dialog. */
async function confirmDialog() {
  const dialog = await screen.findByTestId("app-confirm-dialog");
  fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
}

/** Dismisses the shared confirmation dialog. */
async function cancelDialog() {
  const dialog = await screen.findByTestId("app-confirm-dialog");
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
}

beforeEach(() => {
  vi.useRealTimers();
});

test("the table asks for the statistics and renders a row per session", async () => {
  const calls = stubFetch();
  renderTable();

  await screen.findByTestId("sessions-manager-row-sess_one");
  expect(calls[0]?.url).toContain("include_stats=true");

  const first = screen.getByTestId("sessions-manager-row-sess_one");
  expect(first).toHaveTextContent("Refactor the parser");
  expect(first).toHaveTextContent("openai/gpt-4o");
  expect(first).toHaveTextContent("12");
  expect(first).toHaveTextContent("1.5k");
  expect(first).toHaveTextContent("coddy");

  // A session that never overrode the model reads as the configured default,
  // and one stored before createdAt existed shows an em dash, not "now".
  const second = screen.getByTestId("sessions-manager-row-sess_two");
  expect(second).toHaveTextContent("default");
  expect(second).toHaveTextContent("—");
});

test("ticking rows enables the bulk button and posts exactly those ids", async () => {
  const calls = stubFetch(["sess_one"]);
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_one");

  const bulk = screen.getByTestId("sessions-manager-delete-selected");
  expect(bulk).toBeDisabled();

  fireEvent.click(screen.getByTestId("sessions-manager-pick-sess_one"));
  expect(bulk).not.toBeDisabled();
  // The button carries a glyph; what it does is its accessible name, and the
  // only text drawn on it is the selection count.
  expect(bulk).toHaveAccessibleName("Delete selected (1)");
  expect(
    screen.getByTestId("sessions-manager-selected-count"),
  ).toHaveTextContent("1");

  fireEvent.click(bulk);
  await confirmDialog();

  await waitFor(() => {
    const post = calls.find((c) => c.init?.method === "POST");
    expect(post?.url).toBe("/coddy/sessions/bulk-delete");
    expect(JSON.parse(String(post?.init?.body))).toEqual({
      ids: ["sess_one"],
    });
  });
});

test("cancelling the confirmation deletes nothing", async () => {
  const calls = stubFetch();
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_one");

  fireEvent.click(screen.getByTestId("sessions-manager-pick-sess_one"));
  fireEvent.click(screen.getByTestId("sessions-manager-delete-selected"));
  await cancelDialog();

  await waitFor(() =>
    expect(calls.some((c) => c.init?.method === "POST")).toBe(false),
  );
});

test("the header checkbox selects and clears every rendered row", async () => {
  stubFetch();
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_one");

  const header = screen.getByTestId(
    "sessions-manager-select-all",
  ) as HTMLInputElement;
  fireEvent.click(header);
  expect(
    (screen.getByTestId("sessions-manager-pick-sess_one") as HTMLInputElement)
      .checked,
  ).toBe(true);
  expect(
    (screen.getByTestId("sessions-manager-pick-sess_two") as HTMLInputElement)
      .checked,
  ).toBe(true);
  expect(
    screen.getByTestId("sessions-manager-delete-selected"),
  ).toHaveAccessibleName("Delete selected (2)");

  fireEvent.click(header);
  expect(screen.getByTestId("sessions-manager-delete-selected")).toBeDisabled();
  // With nothing ticked the count badge is gone rather than showing a zero.
  expect(screen.queryByTestId("sessions-manager-selected-count")).toBeNull();
});

test("delete all asks the server for the whole history, not the loaded page", async () => {
  const calls = stubFetch(["sess_one", "sess_two"]);
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_one");

  fireEvent.click(screen.getByTestId("sessions-manager-delete-all"));
  await confirmDialog();

  await waitFor(() => {
    const post = calls.find((c) => c.init?.method === "POST");
    expect(JSON.parse(String(post?.init?.body))).toEqual({ scope: "all" });
  });
});

test("delete all but the open one names it as the exception", async () => {
  const calls = stubFetch(["sess_two"]);
  renderTable({ activeSessionId: "sess_one" });
  await screen.findByTestId("sessions-manager-row-sess_one");

  fireEvent.click(screen.getByTestId("sessions-manager-delete-others"));
  await confirmDialog();

  await waitFor(() => {
    const post = calls.find((c) => c.init?.method === "POST");
    expect(JSON.parse(String(post?.init?.body))).toEqual({
      scope: "all",
      except: ["sess_one"],
    });
  });
});

test("without an open conversation there is nothing to spare", async () => {
  stubFetch();
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_one");
  const others = screen.getByTestId("sessions-manager-delete-others");
  expect(others).toBeDisabled();
  // Disabled for a reason, and the tooltip says which one.
  expect(others).toHaveAttribute("title", "No conversation is open to keep");
});

test("the bulk actions are named by their tooltip, not by a label", async () => {
  stubFetch();
  renderTable({ activeSessionId: "sess_one" });
  await screen.findByTestId("sessions-manager-row-sess_one");

  for (const [testid, label] of [
    ["sessions-manager-delete-others", "Delete all but the open one"],
    ["sessions-manager-delete-all", "Delete all"],
  ] as const) {
    const button = screen.getByTestId(testid);
    expect(button).toHaveAccessibleName(label);
    expect(button).toHaveAttribute("title", label);
    expect(button.textContent).toBe("");
  }
});

test("a client-only draft is not a session the server can spare", async () => {
  stubFetch();
  renderTable({ activeSessionId: "draft_local_1" });
  await screen.findByTestId("sessions-manager-row-sess_one");
  expect(screen.getByTestId("sessions-manager-delete-others")).toBeDisabled();
});

test("the deleted ids are reported to the shell and the list re-reads", async () => {
  const calls = stubFetch(["sess_one"]);
  const onSessionsDeleted = vi.fn();
  renderTable({ onSessionsDeleted });
  await screen.findByTestId("sessions-manager-row-sess_one");

  fireEvent.click(screen.getByTestId("sessions-manager-pick-sess_one"));
  fireEvent.click(screen.getByTestId("sessions-manager-delete-selected"));
  await confirmDialog();

  await waitFor(() =>
    expect(onSessionsDeleted).toHaveBeenCalledWith(["sess_one"]),
  );
  // The table re-reads after the delete instead of trusting its own state.
  await waitFor(() =>
    expect(
      calls.filter((c) => c.url.startsWith("/coddy/sessions?")).length,
    ).toBeGreaterThan(1),
  );
  await waitFor(() =>
    expect(screen.queryByTestId("sessions-manager-row-sess_one")).toBeNull(),
  );
});

test("a session that could not be removed is reported instead of hidden", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      if (url.startsWith("/coddy/sessions/bulk-delete")) {
        return {
          ok: true,
          json: async () => ({
            deleted: [],
            failed: [{ id: "sess_one", error: "turn did not settle" }],
          }),
        } as unknown as Response;
      }
      return {
        ok: true,
        json: async () => ({ sessions: rows, hasMore: false }),
      } as unknown as Response;
    }),
  );
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_one");

  fireEvent.click(screen.getByTestId("sessions-manager-pick-sess_one"));
  fireEvent.click(screen.getByTestId("sessions-manager-delete-selected"));
  await confirmDialog();

  const error = await screen.findByTestId("sessions-manager-error");
  expect(error).toHaveTextContent("turn did not settle");
  expect(screen.getByTestId("sessions-manager-row-sess_one")).toBeTruthy();
});

test("a failed listing says so instead of rendering an empty history", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({ ok: false, status: 503 }) as unknown as Response),
  );
  renderTable();
  const error = await screen.findByTestId("sessions-manager-error");
  expect(error).toHaveTextContent("503");
});

// A narrowed search is a new working set: the ticks that went with the rows it
// hid are dropped, so "delete selected" can never reach a row off screen and a
// tick cannot reappear later because it survived out of sight.
test("a search that hides a ticked row drops that tick", async () => {
  const calls: { url: string }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      calls.push({ url });
      const narrowed = url.includes("q=release");
      return {
        ok: true,
        json: async () => ({
          sessions: narrowed ? [rows[1]] : rows,
          hasMore: false,
        }),
      } as unknown as Response;
    }),
  );
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_one");

  fireEvent.click(screen.getByTestId("sessions-manager-pick-sess_one"));
  expect(
    screen.getByTestId("sessions-manager-selected-count"),
  ).toHaveTextContent("1");

  fireEvent.change(screen.getByTestId("sessions-manager-search"), {
    target: { value: "release" },
  });
  await waitFor(() =>
    expect(calls.some((c) => c.url.includes("q=release"))).toBe(true),
  );
  await waitFor(() =>
    expect(screen.queryByTestId("sessions-manager-row-sess_one")).toBeNull(),
  );

  expect(screen.queryByTestId("sessions-manager-selected-count")).toBeNull();
  expect(screen.getByTestId("sessions-manager-delete-selected")).toBeDisabled();

  // Clearing the search brings the row back unticked: the tick is gone, not
  // merely hidden while the filter was on.
  fireEvent.change(screen.getByTestId("sessions-manager-search"), {
    target: { value: "" },
  });
  const back = await screen.findByTestId("sessions-manager-row-sess_one");
  expect(back).toBeTruthy();
  expect(
    (screen.getByTestId("sessions-manager-pick-sess_one") as HTMLInputElement)
      .checked,
  ).toBe(false);
  expect(screen.queryByTestId("sessions-manager-selected-count")).toBeNull();
});

// The refresh after a delete reads the search the field shows now. A debounce
// that lands while the confirmation dialog is up used to be overwritten by the
// query captured when the dialog opened.
test("the refresh after a delete uses the current search", async () => {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push(url);
      if (url.startsWith("/coddy/sessions/bulk-delete")) {
        return {
          ok: true,
          json: async () => ({ deleted: ["sess_one"], failed: [] }),
        } as unknown as Response;
      }
      void init;
      return {
        ok: true,
        json: async () => ({ sessions: rows, hasMore: false }),
      } as unknown as Response;
    }),
  );
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_one");

  fireEvent.click(screen.getByTestId("sessions-manager-pick-sess_one"));
  fireEvent.click(screen.getByTestId("sessions-manager-delete-selected"));
  // The search moves while the dialog is open; the debounce fires before the
  // confirmation is answered.
  fireEvent.change(screen.getByTestId("sessions-manager-search"), {
    target: { value: "release" },
  });
  await waitFor(() =>
    expect(calls.some((u) => u.includes("q=release"))).toBe(true),
  );
  await confirmDialog();

  await waitFor(() =>
    expect(calls.some((u) => u.startsWith("/coddy/sessions/bulk-delete"))).toBe(
      true,
    ),
  );
  // Whatever the last listing was, it carried the query on screen.
  await waitFor(() => {
    const lastList = [...calls]
      .reverse()
      .find((u) => u.startsWith("/coddy/sessions?"));
    expect(lastList).toContain("q=release");
  });
});

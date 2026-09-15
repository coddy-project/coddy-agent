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

test("deleting is the one action, and only the ticked rows travel", async () => {
  const calls = stubFetch(["sess_one", "sess_two"]);
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_one");

  // There is no second or third destructive button to reach past the ticks.
  expect(screen.queryByTestId("sessions-manager-delete-others")).toBeNull();
  expect(screen.queryByTestId("sessions-manager-delete-all")).toBeNull();

  fireEvent.click(screen.getByTestId("sessions-manager-select-all"));
  fireEvent.click(screen.getByTestId("sessions-manager-delete-selected"));
  await confirmDialog();

  await waitFor(() => {
    const post = calls.find((c) => c.init?.method === "POST");
    expect(JSON.parse(String(post?.init?.body))).toEqual({
      ids: ["sess_one", "sess_two"],
    });
  });
});

test("the delete button is named by its tooltip, not by a label", async () => {
  stubFetch();
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_one");

  const button = screen.getByTestId("sessions-manager-delete-selected");
  expect(button).toHaveAccessibleName("Delete selected (0)");
  expect(button).toHaveAttribute("title", "Delete selected (0)");
  expect(button.textContent).toBe("");
});

// The conversation on screen behind the panel cannot be taken out from under
// the operator: it has no tick, and the header passes over it.
test("the open conversation is protected from every delete in the table", async () => {
  const calls = stubFetch(["sess_two"]);
  renderTable({ activeSessionId: "sess_one" });
  await screen.findByTestId("sessions-manager-row-sess_one");

  const protectedPick = screen.getByTestId(
    "sessions-manager-pick-sess_one",
  ) as HTMLInputElement;
  expect(protectedPick).toBeDisabled();
  expect(protectedPick).toHaveAccessibleName(
    "The conversation that is open cannot be deleted here. Switch to another one first.",
  );

  // Ticking the header takes the page apart from the protected row.
  fireEvent.click(screen.getByTestId("sessions-manager-select-all"));
  expect(protectedPick.checked).toBe(false);
  expect(
    (screen.getByTestId("sessions-manager-pick-sess_two") as HTMLInputElement)
      .checked,
  ).toBe(true);
  expect(
    screen.getByTestId("sessions-manager-selected-count"),
  ).toHaveTextContent("1");

  fireEvent.click(screen.getByTestId("sessions-manager-delete-selected"));
  await confirmDialog();
  await waitFor(() => {
    const post = calls.find((c) => c.init?.method === "POST");
    expect(JSON.parse(String(post?.init?.body))).toEqual({ ids: ["sess_two"] });
  });
});

// A client-only draft has no bundle, so no row of the table is its own and
// nothing is protected - the whole page stays selectable.
test("a client-only draft protects no row", async () => {
  stubFetch();
  renderTable({ activeSessionId: "draft_local_1" });
  await screen.findByTestId("sessions-manager-row-sess_one");

  fireEvent.click(screen.getByTestId("sessions-manager-select-all"));
  expect(
    screen.getByTestId("sessions-manager-selected-count"),
  ).toHaveTextContent("2");
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

// --- tags, the archive, and ordering ---

const archiveRows: SessionManagerRow[] = [
  {
    id: "sess_live",
    title: "Refactor the parser",
    cwd: "/home/u/projects/coddy",
    updatedAt: "2026-09-02T10:00:00Z",
    tags: ["backend", "parser"],
  },
  {
    id: "sess_filed",
    title: "Old experiment",
    cwd: "/home/u/projects/site",
    updatedAt: "2026-08-02T10:00:00Z",
    archived: true,
    archivedAt: "2026-09-01T10:00:00Z",
  },
];

/**
 * stubArchiveFetch answers the listing according to the query it is given, so a
 * test can assert that a control actually reached the server rather than only
 * that it re-rendered.
 */
function stubArchiveFetch() {
  const calls: Call[] = [];
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    calls.push({ url, ...(init ? { init } : {}) });
    if (url.startsWith("/coddy/sessions/bulk-delete")) {
      return {
        ok: true,
        json: async () => ({ deleted: [], failed: [] }),
      } as unknown as Response;
    }
    const query = new URLSearchParams(url.split("?")[1] ?? "");
    const archived = query.get("archived") ?? "exclude";
    const tags = (query.get("tags") ?? "").trim();
    let body = archiveRows.filter((r) => {
      if (archived === "only") {
        return !!r.archived;
      }
      if (archived === "all") {
        return true;
      }
      return !r.archived;
    });
    if (tags) {
      const wanted = tags.split(",");
      body = body.filter((r) => (r.tags ?? []).some((t) => wanted.includes(t)));
    }
    return {
      ok: true,
      json: async () => ({ sessions: body, hasMore: false, nextCursor: null }),
    } as unknown as Response;
  });
  vi.stubGlobal("fetch", fetchMock);
  return calls;
}

test("a row has no delete of its own: the tick and the one button are the whole surface", async () => {
  stubArchiveFetch();
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_live");

  expect(screen.queryByTestId("sessions-manager-delete-sess_live")).toBeNull();
  expect(screen.getByTestId("sessions-manager-pick-sess_live")).toBeEnabled();
});

test("the listing starts on the working list and the archive is one control away", async () => {
  const calls = stubArchiveFetch();
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_live");

  expect(calls[0]?.url).toContain("archived=exclude");
  expect(screen.queryByTestId("sessions-manager-row-sess_filed")).toBeNull();

  fireEvent.change(screen.getByTestId("sessions-manager-archive-filter"), {
    target: { value: "only" },
  });
  await screen.findByTestId("sessions-manager-row-sess_filed");
  expect(screen.queryByTestId("sessions-manager-row-sess_live")).toBeNull();

  fireEvent.change(screen.getByTestId("sessions-manager-archive-filter"), {
    target: { value: "all" },
  });
  await screen.findByTestId("sessions-manager-row-sess_live");
  expect(
    screen.getByTestId("sessions-manager-row-sess_filed"),
  ).toBeInTheDocument();
});

test("an archived row says so", async () => {
  stubArchiveFetch();
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_live");

  fireEvent.change(screen.getByTestId("sessions-manager-archive-filter"), {
    target: { value: "all" },
  });
  const filed = await screen.findByTestId("sessions-manager-row-sess_filed");
  expect(
    within(filed).getByTestId("sessions-manager-archived-sess_filed"),
  ).toHaveTextContent("archived");
  expect(
    screen.queryByTestId("sessions-manager-archived-sess_live"),
  ).toBeNull();
});

test("emptying the archive is one request with the archived scope", async () => {
  const calls = stubArchiveFetch();
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_live");

  fireEvent.click(screen.getByTestId("sessions-manager-delete-archived"));
  await confirmDialog();

  await waitFor(() => {
    const post = calls.find((c) => c.init?.method === "POST");
    expect(post?.url).toBe("/coddy/sessions/bulk-delete");
    expect(JSON.parse(String(post?.init?.body))).toEqual({ scope: "archived" });
  });
});

test("cancelling keeps the archive", async () => {
  const calls = stubArchiveFetch();
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_live");

  fireEvent.click(screen.getByTestId("sessions-manager-delete-archived"));
  await cancelDialog();
  await waitFor(() =>
    expect(calls.some((c) => c.init?.method === "POST")).toBe(false),
  );
});

test("a column header orders the whole listing and toggles direction", async () => {
  const calls = stubArchiveFetch();
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_live");

  fireEvent.click(screen.getByTestId("sessions-manager-sort-title"));
  await waitFor(() =>
    expect(calls.at(-1)?.url).toContain("sort=title&order=asc"),
  );

  fireEvent.click(screen.getByTestId("sessions-manager-sort-title"));
  await waitFor(() =>
    expect(calls.at(-1)?.url).toContain("sort=title&order=desc"),
  );

  // A different column starts from its own natural direction rather than
  // inheriting the one before it: dates read newest first.
  fireEvent.click(screen.getByTestId("sessions-manager-sort-updated"));
  await waitFor(() =>
    expect(calls.at(-1)?.url).toContain("sort=updated&order=desc"),
  );
});

test("the sorted column says which way it points", async () => {
  stubArchiveFetch();
  renderTable();
  await screen.findByTestId("sessions-manager-row-sess_live");

  const updated = screen.getByTestId("sessions-manager-sort-updated");
  expect(updated.closest("th")).toHaveAttribute("aria-sort", "descending");
  fireEvent.click(updated);
  await waitFor(() =>
    expect(updated.closest("th")).toHaveAttribute("aria-sort", "ascending"),
  );
});

test("a tag on a row filters the listing when clicked, and clears again", async () => {
  const calls = stubArchiveFetch();
  renderTable();
  const live = await screen.findByTestId("sessions-manager-row-sess_live");

  fireEvent.click(within(live).getByTestId("sessions-manager-tag-backend"));
  await waitFor(() => expect(calls.at(-1)?.url).toContain("tags=backend"));

  fireEvent.click(screen.getByTestId("sessions-manager-tag-filter-clear"));
  await waitFor(() => expect(calls.at(-1)?.url).not.toContain("tags="));
});

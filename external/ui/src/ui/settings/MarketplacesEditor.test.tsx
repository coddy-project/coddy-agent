import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { initLocale } from "../i18n/i18n";
import { MarketplacesEditor } from "./MarketplacesEditor";
import { showsEntryTrustControl, type MarketplaceEntry } from "./marketplaces";
import {
  noteSettingsConfigSaved,
  resetSettingsConfigForTests,
} from "./settingsConfigStore";

// The marketplaces list of Settings -> Skills reads what the two
// marketplaces.json files and Coddy itself declare for the viewed session's
// workspace (GET /coddy/skills/sources), and every action applies at once.

beforeEach(() => {
  initLocale("en");
});

afterEach(() => {
  cleanup();
  initLocale("en");
  vi.unstubAllGlobals();
  resetSettingsConfigForTests();
});

const SYSTEM: MarketplaceEntry = {
  kind: "source",
  source: "EvilFreelancer/rpa-skills",
  origin: "system",
  gated: false,
  trusted: true,
  status: "ready",
};

const HOME_CATALOG: MarketplaceEntry = {
  kind: "marketplace",
  name: "shop",
  source: "acme/shop-skills",
  origin: "home",
  source_path: "/home/me/.coddy/marketplaces.json",
  gated: false,
  trusted: true,
  status: "ready",
};

const PROJECT_SOURCE: MarketplaceEntry = {
  kind: "source",
  source: "team/data-skills",
  origin: "project",
  source_path: "/work/repo/.coddy/marketplaces.json",
  gated: true,
  trusted: false,
  status: "needs_approval",
  fingerprint: "a1b2c3d4e5f60718",
};

type Call = {
  url: string;
  method: string;
  headers: Record<string, string>;
  body: unknown;
};

function listing(entries: MarketplaceEntry[], projectTrust = "ask") {
  return {
    object: "coddy.skill_sources",
    workspace: "/work/repo",
    project_trust: projectTrust,
    entries,
    errors: [],
  };
}

// Answers GET /coddy/skills/sources with the current listing and records
// every request. `onWrite` decides what a write answers and may move the
// listing to its next state.
function stubApi(
  initial: unknown,
  onWrite?: (
    call: Call,
    setListing: (next: unknown) => void,
  ) => { ok: boolean; status: number; body: unknown } | undefined,
) {
  const calls: Call[] = [];
  let current = initial;
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      const call: Call = {
        url: String(url),
        method: init?.method ?? "GET",
        headers: (init?.headers ?? {}) as Record<string, string>,
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      };
      calls.push(call);
      if (call.method === "GET") {
        return Promise.resolve({ ok: true, json: async () => current });
      }
      const res = onWrite?.(call, (next) => {
        current = next;
      }) ?? { ok: true, status: 200, body: {} };
      return Promise.resolve({
        ok: res.ok,
        status: res.status,
        json: async () => res.body,
      });
    }),
  );
  return calls;
}

function renderEditor(sessionId = "sess_view", onSynced = vi.fn()) {
  render(
    <MarketplacesEditor activeSessionId={sessionId} onSynced={onSynced} />,
  );
  return onSynced;
}

test("lists every declaration with its kind and the file it comes from", async () => {
  const calls = stubApi(listing([SYSTEM, HOME_CATALOG, PROJECT_SOURCE]));
  renderEditor();
  await screen.findByTestId("skills-marketplace-shop");

  // Scoped like the MCP tab: the viewed session, never a cwd of its own.
  expect(calls[0]?.url).toBe("/coddy/skills/sources");
  expect(calls[0]?.headers["X-Coddy-Session-ID"]).toBe("sess_view");

  const system = screen.getByTestId(
    "skills-marketplace-EvilFreelancer/rpa-skills",
  );
  expect(system).toHaveTextContent("all plugins");
  expect(
    screen.getByTestId("skills-marketplace-origin-EvilFreelancer/rpa-skills"),
  ).toHaveTextContent("built in");

  // A marketplace is named by its name and shows the address under it.
  const shop = screen.getByTestId("skills-marketplace-shop");
  expect(shop).toHaveTextContent("catalog");
  expect(shop).toHaveTextContent("acme/shop-skills");
  const shopOrigin = screen.getByTestId("skills-marketplace-origin-shop");
  expect(shopOrigin).toHaveTextContent("yours");
  expect(shopOrigin).toHaveAttribute(
    "title",
    "/home/me/.coddy/marketplaces.json",
  );

  expect(
    screen.getByTestId("skills-marketplace-origin-team/data-skills"),
  ).toHaveTextContent("from the project");
});

test("the built-in marketplace is always trusted and cannot be removed", async () => {
  stubApi(listing([SYSTEM]));
  renderEditor();
  const key = "EvilFreelancer/rpa-skills";
  const shield = await screen.findByTestId(`skills-marketplace-trust-${key}`);
  expect(shield).toBeDisabled();
  expect(shield).toHaveClass("is-trusted");
  expect(shield).toHaveAttribute(
    "aria-label",
    "EvilFreelancer/rpa-skills is built into Coddy and always trusted",
  );
  const remove = screen.getByTestId(`skills-marketplace-remove-${key}`);
  expect(remove).toBeDisabled();
  expect(remove).toHaveAttribute(
    "title",
    "Built into Coddy: always in effect, always trusted, not removable",
  );
  // It syncs like any other.
  expect(screen.getByTestId(`skills-marketplace-sync-${key}`)).toBeEnabled();
});

test("a project entry waits for its approval: a note, no sync, and the shield", async () => {
  stubApi(listing([PROJECT_SOURCE]));
  renderEditor();
  const key = "team/data-skills";
  const row = await screen.findByTestId(`skills-marketplace-${key}`);
  expect(row).toHaveClass("is-held");
  expect(
    screen.getByTestId(`skills-marketplace-note-${key}`),
  ).toHaveTextContent(
    "Declared by /work/repo/.coddy/marketplaces.json, which travels with the checkout",
  );
  const sync = screen.getByTestId(`skills-marketplace-sync-${key}`);
  expect(sync).toBeDisabled();
  expect(sync).toHaveAttribute("title", "Approve it for this workspace first");
  const shield = screen.getByTestId(`skills-marketplace-trust-${key}`);
  expect(shield).toBeEnabled();
  expect(shield).toHaveClass("settings-btn-approve");
  expect(shield).toHaveAttribute(
    "title",
    "Approve syncing team/data-skills in this workspace",
  );
});

test("the shield approves the entry as it was shown, then withdraws it", async () => {
  const approved: MarketplaceEntry = {
    ...PROJECT_SOURCE,
    trusted: true,
    status: "ready",
  };
  const calls = stubApi(listing([PROJECT_SOURCE]), (call, setListing) => {
    if (call.url.endsWith("/trust")) setListing(listing([approved]));
    if (call.url.endsWith("/untrust")) setListing(listing([PROJECT_SOURCE]));
    return undefined;
  });
  renderEditor();
  const key = "team/data-skills";
  fireEvent.click(await screen.findByTestId(`skills-marketplace-trust-${key}`));

  await waitFor(() =>
    expect(screen.getByTestId(`skills-marketplace-trust-${key}`)).toHaveClass(
      "is-trusted",
    ),
  );
  const trust = calls.find((c) => c.method === "POST");
  expect(trust?.url).toBe("/coddy/skills/sources/trust");
  // The fingerprint binds the approval to the declaration the row showed.
  expect(trust?.body).toEqual({ key, fingerprint: "a1b2c3d4e5f60718" });
  expect(trust?.headers["X-Coddy-Session-ID"]).toBe("sess_view");
  expect(screen.queryByTestId(`skills-marketplace-note-${key}`)).toBeNull();
  expect(screen.getByTestId(`skills-marketplace-sync-${key}`)).toBeEnabled();

  fireEvent.click(screen.getByTestId(`skills-marketplace-trust-${key}`));
  await screen.findByTestId(`skills-marketplace-note-${key}`);
  const untrust = calls.filter((c) => c.method === "POST")[1];
  expect(untrust?.url).toBe("/coddy/skills/sources/untrust");
  expect(untrust?.body).toEqual({ key });
});

test("a refused approval says why", async () => {
  stubApi(listing([PROJECT_SOURCE]), () => ({
    ok: false,
    status: 409,
    body: {
      error: {
        message:
          "the declaration of team/data-skills changed since it was listed; review it again",
      },
    },
  }));
  renderEditor();
  fireEvent.click(
    await screen.findByTestId("skills-marketplace-trust-team/data-skills"),
  );
  await screen.findByText(
    "the declaration of team/data-skills changed since it was listed; review it again",
  );
});

test("a denied project entry says the policy switched it off and has no shield", async () => {
  stubApi(listing([{ ...PROJECT_SOURCE, status: "denied" }], "deny"));
  renderEditor();
  const key = "team/data-skills";
  expect(
    await screen.findByTestId(`skills-marketplace-note-${key}`),
  ).toHaveTextContent("skills.project_trust: deny");
  expect(screen.queryByTestId(`skills-marketplace-trust-${key}`)).toBeNull();
});

test("adding declares the entry where the operator chose", async () => {
  const added: MarketplaceEntry = {
    ...PROJECT_SOURCE,
    source: "team/new-skills",
    trusted: true,
    status: "ready",
  };
  const calls = stubApi(listing([SYSTEM]), (call, setListing) => {
    if (call.url === "/coddy/skills/sources")
      setListing(listing([SYSTEM, added]));
    return undefined;
  });
  renderEditor();
  const input = await screen.findByTestId("skills-marketplace-input");
  const add = screen.getByTestId("skills-marketplace-add");
  expect(add).toBeDisabled();
  fireEvent.change(input, { target: { value: " team/new-skills " } });
  fireEvent.change(screen.getByTestId("skills-marketplace-scope"), {
    target: { value: "local" },
  });
  fireEvent.click(add);

  await screen.findByTestId("skills-marketplace-team/new-skills");
  const post = calls.find((c) => c.method === "POST");
  expect(post?.url).toBe("/coddy/skills/sources");
  expect(post?.body).toEqual({ source: "team/new-skills", scope: "local" });
  expect(post?.headers["X-Coddy-Session-ID"]).toBe("sess_view");
  expect(input).toHaveValue("");
});

test("removing names the entry by its key", async () => {
  const calls = stubApi(listing([SYSTEM, HOME_CATALOG]), (call, setListing) => {
    if (call.method === "DELETE") setListing(listing([SYSTEM]));
    return undefined;
  });
  renderEditor();
  const remove = await screen.findByTestId("skills-marketplace-remove-shop");
  expect(remove).toHaveAttribute(
    "title",
    "Remove from /home/me/.coddy/marketplaces.json",
  );
  fireEvent.click(remove);
  await waitFor(() =>
    expect(screen.queryByTestId("skills-marketplace-shop")).toBeNull(),
  );
  const del = calls.find((c) => c.method === "DELETE");
  expect(del?.url).toBe("/coddy/skills/sources?source=shop&origin=home");
});

test("sync of one entry and sync of all refresh the installed list", async () => {
  const calls = stubApi(listing([SYSTEM, HOME_CATALOG]));
  const onSynced = renderEditor();
  fireEvent.click(await screen.findByTestId("skills-marketplace-sync-shop"));
  await waitFor(() => expect(onSynced).toHaveBeenCalledTimes(1));
  expect(calls.find((c) => c.method === "POST")?.url).toBe(
    "/coddy/skills/sync?source=shop",
  );
  await waitFor(() =>
    expect(screen.getByTestId("skills-marketplace-sync-shop")).toHaveClass(
      "is-synced",
    ),
  );

  fireEvent.click(screen.getByTestId("skills-sync-all"));
  await waitFor(() => expect(onSynced).toHaveBeenCalledTimes(2));
  expect(calls.filter((c) => c.method === "POST")[1]?.url).toBe(
    "/coddy/skills/sync",
  );
});

test("the list reads in Russian", async () => {
  initLocale("ru");
  stubApi(listing([SYSTEM, PROJECT_SOURCE]));
  renderEditor();
  await screen.findByTestId("skills-marketplace-team/data-skills");
  expect(screen.getByTestId("skills-marketplaces")).toHaveTextContent(
    "Маркетплейсы",
  );
  expect(
    screen.getByTestId("skills-marketplace-origin-team/data-skills"),
  ).toHaveTextContent("из проекта");
});

test("only a project entry under ask offers the shield", () => {
  expect(showsEntryTrustControl(PROJECT_SOURCE, "ask")).toBe(true);
  expect(showsEntryTrustControl(PROJECT_SOURCE, "allow")).toBe(false);
  expect(showsEntryTrustControl(PROJECT_SOURCE, "deny")).toBe(false);
  expect(showsEntryTrustControl(HOME_CATALOG, "ask")).toBe(false);
  expect(showsEntryTrustControl(SYSTEM, "ask")).toBe(false);
});

// A chat kept as a draft in the browser has no session on the server: its id
// is not sent (the server would answer 404), and a project entry cannot be
// written for it, since the server would take its own default folder.
test("a draft chat is no session: no header, and only your file can be written", async () => {
  const calls = stubApi(listing([SYSTEM]), (call, setListing) => {
    if (call.url === "/coddy/skills/sources") setListing(listing([SYSTEM]));
    return undefined;
  });
  renderEditor("draft_0123456789abcdef");
  const scope = await screen.findByTestId("skills-marketplace-scope");
  expect(calls[0]?.headers["X-Coddy-Session-ID"]).toBeUndefined();
  expect(scope).toBeDisabled();
  expect(scope).toHaveValue("global");
  expect(scope).toHaveAttribute(
    "title",
    expect.stringContaining("Send the first message of a chat in the project"),
  );
  fireEvent.change(screen.getByTestId("skills-marketplace-input"), {
    target: { value: "team/new-skills" },
  });
  fireEvent.click(screen.getByTestId("skills-marketplace-add"));
  await waitFor(() =>
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      source: "team/new-skills",
      scope: "global",
    }),
  );
});

// A refresh that fails after a sync (the connection dropped) says so and
// leaves the Sync buttons usable.
test("a failed refresh after a sync never leaves Sync disabled", async () => {
  stubApi(listing([SYSTEM, HOME_CATALOG]));
  const onSynced = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));
  renderEditor("sess_view", onSynced);
  fireEvent.click(await screen.findByTestId("skills-sync-all"));
  await screen.findByText("Sync failed");
  expect(screen.getByTestId("skills-sync-all")).toBeEnabled();
  expect(screen.getByTestId("skills-marketplace-sync-shop")).toBeEnabled();
});

// A save of the settings may change skills.project_trust: the list is read
// again, so the shields and the held notes follow the saved policy.
test("a save of the settings reads the list again", async () => {
  const calls = stubApi(listing([PROJECT_SOURCE]));
  renderEditor();
  await screen.findByTestId("skills-marketplace-team/data-skills");
  const gets = () => calls.filter((c) => c.method === "GET").length;
  expect(gets()).toBe(1);
  act(() => {
    noteSettingsConfigSaved({ skills: { project_trust: "allow" } });
  });
  await waitFor(() => expect(gets()).toBe(2));
});

// A denied row has no shield to press, so its Sync names the policy, not an
// approval.
test("the Sync of a denied row names the policy", async () => {
  stubApi(listing([{ ...PROJECT_SOURCE, status: "denied" }], "deny"));
  renderEditor();
  const sync = await screen.findByTestId(
    "skills-marketplace-sync-team/data-skills",
  );
  expect(sync).toBeDisabled();
  expect(sync).toHaveAttribute(
    "title",
    "Project marketplaces are switched off by skills.project_trust: deny",
  );
});

// Removing a row edits the file of that row only.
test("removing a row names its file", async () => {
  const calls = stubApi(listing([PROJECT_SOURCE]), (call, setListing) => {
    if (call.method === "DELETE") setListing(listing([]));
    return undefined;
  });
  renderEditor();
  fireEvent.click(
    await screen.findByTestId("skills-marketplace-remove-team/data-skills"),
  );
  await waitFor(() =>
    expect(calls.find((c) => c.method === "DELETE")?.url).toBe(
      "/coddy/skills/sources?source=team%2Fdata-skills&origin=project",
    ),
  );
});

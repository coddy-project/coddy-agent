import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { Settings } from "./Settings";
import {
  noteSettingsConfigReloaded,
  resetSettingsConfigForTests,
} from "./settingsConfigStore";
import { initLocale } from "../i18n/i18n";
import { setEnv } from "../env/remoteEnv";
import { OpenRailScreen } from "../nav/railEscape.fakes";

// Issue #485: a change in Settings applies only after Save, and nothing on
// screen used to say so. While the form holds unsaved edits, Save stands out
// with a line beside it, and closing over such edits asks first.

const schema = {
  type: "object",
  "x-coddy-property-order": ["agent"],
  properties: {
    agent: {
      type: "object",
      title: "ReAct loop",
      properties: { max_turns: { type: "integer", title: "Max turns" } },
    },
  },
};

type Doc = Record<string, unknown>;
let config: Doc;
let puts: Doc[];
/** While set, validate and PUT wait for it. */
let gate: Promise<void> | null;

/** Holds every validate and PUT until the returned function is called. */
function holdSaves(): () => void {
  let release: () => void = () => {};
  gate = new Promise<void>((r) => {
    release = () => {
      gate = null;
      r();
    };
  });
  return () => release();
}

beforeEach(() => {
  initLocale("en");
  resetSettingsConfigForTests();
  config = { revision: "rev-1", agent: { max_turns: 40 } };
  puts = [];
  gate = null;
  window.location.hash = "";
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      const reply = (status: number, body: unknown) => ({
        ok: status >= 200 && status < 300,
        status,
        json: async () => structuredClone(body),
      });
      if (path === "/coddy/config/schema") return reply(200, schema);
      if (path === "/coddy/config" && method === "GET") {
        return reply(200, config);
      }
      if (path === "/coddy/config/validate") {
        if (gate) await gate;
        return reply(200, { ok: true });
      }
      if (path === "/coddy/config" && method === "PUT") {
        if (gate) await gate;
        const body = JSON.parse(String(init?.body)) as Doc;
        puts.push(body);
        config = body;
        return reply(200, { ok: true });
      }
      return reply(404, {});
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  setEnv({ mode: "local" });
  resetSettingsConfigForTests();
});

const saveButton = () => screen.getByTestId("settings-save");
const status = () => screen.getByTestId("settings-save-status").textContent;
const maxTurns = async () =>
  (await screen.findByLabelText("Max turns")) as HTMLInputElement;

test("an edit makes Save stand out and says it is not saved until Save is pressed", async () => {
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = await maxTurns();
  expect(saveButton().className).not.toContain("is-dirty");
  expect(status()).toBe("");

  fireEvent.change(input, { target: { value: "41" } });
  expect(saveButton().className).toContain("is-dirty");
  expect(status()).toBe("Unsaved changes");
  expect(puts).toHaveLength(0);

  await act(async () => {
    fireEvent.click(saveButton());
  });
  await waitFor(() => expect(saveButton().className).toContain("is-saved"));
  expect(saveButton().className).not.toContain("is-dirty");
  expect(status()).toBe("");
  expect((puts[0]!.agent as Doc).max_turns).toBe(41);
});

test("an edit put back as it was leaves Save as it was", async () => {
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = await maxTurns();
  fireEvent.change(input, { target: { value: "41" } });
  fireEvent.change(input, { target: { value: "40" } });
  expect(saveButton().className).not.toContain("is-dirty");
  expect(status()).toBe("");
});

test("closing Settings with unsaved edits asks to save them or keep editing", async () => {
  const onClose = vi.fn();
  render(<Settings onClose={onClose} initialSection="agent" />);
  fireEvent.change(await maxTurns(), { target: { value: "41" } });

  fireEvent.click(screen.getByTestId("settings-drawer-close"));
  const dialog = screen.getByTestId("settings-close-dialog");
  expect(dialog).toHaveTextContent("Save before closing?");
  fireEvent.click(within(dialog).getByRole("button", { name: "Keep editing" }));
  expect(screen.queryByTestId("settings-close-dialog")).toBeNull();
  expect(onClose).not.toHaveBeenCalled();

  fireEvent.click(screen.getByTestId("settings-drawer-close"));
  await act(async () => {
    fireEvent.click(
      within(screen.getByTestId("settings-close-dialog")).getByRole("button", {
        name: "Save and close",
      }),
    );
  });
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  expect((puts[0]!.agent as Doc).max_turns).toBe(41);
});

test("Escape over unsaved edits asks first instead of closing", async () => {
  const onClose = vi.fn();
  render(
    <>
      <OpenRailScreen id="settings" onClose={onClose} />
      <Settings onClose={onClose} initialSection="agent" />
    </>,
  );
  fireEvent.change(await maxTurns(), { target: { value: "41" } });
  (document.activeElement as HTMLElement | null)?.blur();
  fireEvent.keyDown(document.body, { key: "Escape" });
  expect(screen.getByTestId("settings-close-dialog")).toBeInTheDocument();
  expect(onClose).not.toHaveBeenCalled();
});

test("closing with nothing unsaved closes at once", async () => {
  const onClose = vi.fn();
  render(<Settings onClose={onClose} initialSection="agent" />);
  await maxTurns();
  fireEvent.click(screen.getByTestId("settings-drawer-close"));
  expect(onClose).toHaveBeenCalledTimes(1);
  expect(screen.queryByTestId("settings-close-dialog")).toBeNull();
});

test("leaving the page with unsaved edits asks the browser, a switch to another server does not, and Save never writes there", async () => {
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const leave = () => {
    const e = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(e);
    return e.defaultPrevented;
  };
  const input = await maxTurns();
  expect(leave()).toBe(false);
  fireEvent.change(input, { target: { value: "41" } });
  expect(leave()).toBe(true);

  // A switch to or from Local stores the new server, then reloads the page.
  // A page kept by a stopped reload still shows this server's form.
  setEnv({ mode: "remote", baseUrl: "http://other.test", token: "" });
  expect(leave()).toBe(false);
  await act(async () => {
    fireEvent.click(saveButton());
  });
  expect(puts).toHaveLength(0);
  expect(document.querySelector(".settings-lead-pane")).toHaveTextContent(
    "another server",
  );
});

// Edge cases.

test("a value put back while Save runs stays unsaved, since the server has the other one", async () => {
  const onClose = vi.fn();
  render(<Settings onClose={onClose} initialSection="agent" />);
  const input = await maxTurns();
  fireEvent.change(input, { target: { value: "41" } });
  const release = holdSaves();
  await act(async () => {
    fireEvent.click(saveButton());
  });
  fireEvent.change(input, { target: { value: "40" } });
  await act(async () => {
    release();
  });
  await waitFor(() => expect(puts).toHaveLength(1));
  await waitFor(() => expect(status()).toBe("Unsaved changes"));
  fireEvent.click(screen.getByTestId("settings-drawer-close"));
  expect(onClose).not.toHaveBeenCalled();
  expect(screen.getByTestId("settings-close-dialog")).toBeInTheDocument();
});

test("Save and close stays open when the form changed while the save ran", async () => {
  const onClose = vi.fn();
  render(<Settings onClose={onClose} initialSection="agent" />);
  const input = await maxTurns();
  fireEvent.change(input, { target: { value: "41" } });
  fireEvent.click(screen.getByTestId("settings-drawer-close"));
  const release = holdSaves();
  await act(async () => {
    fireEvent.click(
      within(screen.getByTestId("settings-close-dialog")).getByRole("button", {
        name: "Save and close",
      }),
    );
  });
  fireEvent.change(input, { target: { value: "42" } });
  await act(async () => {
    release();
  });
  await waitFor(() => expect(puts).toHaveLength(1));
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });
  expect(onClose).not.toHaveBeenCalled();
  expect(status()).toBe("Unsaved changes");
});

test("a switch to another server while Save runs sends nothing there", async () => {
  render(<Settings onClose={() => {}} initialSection="agent" />);
  fireEvent.change(await maxTurns(), { target: { value: "41" } });
  const release = holdSaves();
  await act(async () => {
    fireEvent.click(saveButton());
  });
  setEnv({ mode: "remote", baseUrl: "http://other.test", token: "" });
  await act(async () => {
    release();
  });
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });
  expect(puts).toHaveLength(0);
  expect(saveButton().className).not.toContain("is-saved");
});

test("the close dialog does not start a second save while one runs", async () => {
  render(<Settings onClose={() => {}} initialSection="agent" />);
  fireEvent.change(await maxTurns(), { target: { value: "41" } });
  const release = holdSaves();
  await act(async () => {
    fireEvent.click(saveButton());
  });
  fireEvent.click(screen.getByTestId("settings-drawer-close"));
  expect(
    within(screen.getByTestId("settings-close-dialog")).getByRole("button", {
      name: "Save and close",
    }),
  ).toBeDisabled();
  await act(async () => {
    release();
  });
  await waitFor(() => expect(puts).toHaveLength(1));
});

test("a newer copy reaches a form whose edit was put back", async () => {
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = await maxTurns();
  fireEvent.change(input, { target: { value: "41" } });
  fireEvent.change(input, { target: { value: "40" } });
  config = { revision: "rev-9", agent: { max_turns: 70 } };
  act(() => noteSettingsConfigReloaded());
  await waitFor(() => expect(input.value).toBe("70"));
});

test("an edit right after a save makes Save stand out at once, not green", async () => {
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = await maxTurns();
  fireEvent.change(input, { target: { value: "41" } });
  await act(async () => {
    fireEvent.click(saveButton());
  });
  await waitFor(() => expect(saveButton().className).toContain("is-saved"));
  fireEvent.change(input, { target: { value: "42" } });
  expect(saveButton().className).toContain("is-dirty");
  expect(saveButton().className).not.toContain("is-saved");
  expect(screen.getByRole("status", { hidden: true })).not.toHaveTextContent(
    "Saved",
  );
});

test("closing during a save closes the drawer once the save leaves nothing unsaved", async () => {
  const onClose = vi.fn();
  render(<Settings onClose={onClose} initialSection="agent" />);
  fireEvent.change(await maxTurns(), { target: { value: "41" } });
  const release = holdSaves();
  await act(async () => {
    fireEvent.click(saveButton());
  });
  fireEvent.click(screen.getByTestId("settings-drawer-close"));
  expect(screen.getByTestId("settings-close-dialog")).toBeInTheDocument();
  await act(async () => {
    release();
  });
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  expect(screen.queryByTestId("settings-close-dialog")).toBeNull();
  expect(puts).toHaveLength(1);
});

test("a copy landing during a save does not take a value put back meanwhile", async () => {
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = await maxTurns();
  fireEvent.change(input, { target: { value: "41" } });
  const release = holdSaves();
  await act(async () => {
    fireEvent.click(saveButton());
  });
  fireEvent.change(input, { target: { value: "40" } });
  config = { revision: "rev-7", agent: { max_turns: 41 } };
  act(() => noteSettingsConfigReloaded());
  await act(async () => {
    await new Promise((r) => setTimeout(r, 20));
  });
  expect(input.value).toBe("40");
  await act(async () => {
    release();
  });
  await waitFor(() => expect(puts).toHaveLength(1));
  await waitFor(() => expect(status()).toBe("Unsaved changes"));
  expect(input.value).toBe("40");
});

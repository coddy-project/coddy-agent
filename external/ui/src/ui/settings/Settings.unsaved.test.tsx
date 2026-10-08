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
import { resetSettingsConfigForTests } from "./settingsConfigStore";
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

beforeEach(() => {
  initLocale("en");
  resetSettingsConfigForTests();
  config = { revision: "rev-1", agent: { max_turns: 40 } };
  puts = [];
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
      if (path === "/coddy/config/validate") return reply(200, { ok: true });
      if (path === "/coddy/config" && method === "PUT") {
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

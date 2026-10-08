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
import { AUTOSAVE_MS } from "./settingsDraftStore";
import { initLocale } from "../i18n/i18n";

// Issue #485: the Settings form saves on its own a moment after the last edit,
// and only the changes the schema marks as a deliberate act wait for Save.

const schema = {
  type: "object",
  "x-coddy-property-order": ["providers", "agent", "gateways"],
  properties: {
    providers: {
      type: "array",
      title: "LLM providers",
      "x-coddy-save": "confirm-removal",
      items: {
        type: "object",
        required: ["name", "type"],
        properties: {
          // The served schema carries the example config's first provider as
          // the defaults of a new row.
          name: { type: "string", title: "Provider id", default: "demo" },
          type: { type: "string", title: "Provider type", default: "openai" },
        },
      },
    },
    agent: {
      type: "object",
      title: "ReAct loop",
      properties: { max_turns: { type: "integer", title: "Max turns" } },
    },
    gateways: {
      type: "object",
      title: "Messenger gateways",
      properties: {
        telegram: {
          type: "object",
          title: "Telegram",
          properties: {
            enable: {
              type: "boolean",
              title: "Enabled",
              "x-coddy-save": "confirm",
            },
            token: { type: "string", title: "Bot token" },
          },
        },
      },
    },
  },
};

type Doc = Record<string, unknown>;

let config: Doc;
/** The documents the PUTs carried, in order. */
let puts: Doc[];
/** What the next PUTs answer; ok with a fresh revision unless a test says otherwise. */
let refuse: string | null;

function stubServer() {
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    const method = init?.method ?? "GET";
    const reply = (status: number, body: unknown) => ({
      ok: status >= 200 && status < 300,
      status,
      json: async () => structuredClone(body),
    });
    if (path === "/coddy/config/schema") return reply(200, schema);
    if (path === "/coddy/config" && method === "GET") return reply(200, config);
    if (path === "/coddy/config" && method === "PUT") {
      const body = JSON.parse(String(init?.body)) as Doc;
      puts.push(body);
      if (refuse !== null) {
        return reply(400, { ok: false, error: refuse });
      }
      const revision = `rev-${puts.length + 1}`;
      config = { ...body, revision };
      return reply(200, { ok: true, revision });
    }
    return reply(200, { ok: true, models: [] });
  });
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

beforeEach(() => {
  initLocale("en");
  resetSettingsConfigForTests();
  vi.useFakeTimers({ shouldAdvanceTime: true });
  config = {
    revision: "rev-1",
    providers: [{ name: "demo" }, { name: "spare" }],
    agent: { max_turns: 40 },
    gateways: { telegram: { token: "" } },
  };
  puts = [];
  refuse = null;
  window.location.hash = "";
  stubServer();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  resetSettingsConfigForTests();
});

async function pause(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

const status = () => screen.getByTestId("settings-save-status").textContent;
const saveButton = () => screen.getByTestId("settings-save");
const telegram = (d: Doc) => (d.gateways as Doc).telegram as Doc;

test("an ordinary change saves itself after a pause, several edits in one save", async () => {
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;
  expect(status()).toBe("Changes save automatically");

  for (const v of ["41", "42", "43"]) {
    fireEvent.change(input, { target: { value: v } });
    await pause(AUTOSAVE_MS / 3);
  }
  expect(puts).toHaveLength(0);
  expect(status()).toBe("Unsaved changes");

  await pause(AUTOSAVE_MS);
  await waitFor(() => expect(status()).toBe("All changes saved"));
  expect(puts).toHaveLength(1);
  expect((puts[0]!.agent as Doc).max_turns).toBe(43);
  expect(saveButton().className).not.toContain("has-pending");
});

test("turning on a gateway waits for Save, highlighted, while the rest of the form saves itself", async () => {
  render(<Settings onClose={() => {}} initialSection="gateways" />);
  const sw = await screen.findByRole("switch", { name: "Enabled" });
  fireEvent.click(sw);
  fireEvent.change(screen.getByLabelText("Bot token"), {
    target: { value: "123:abc" },
  });

  // The button stands out at once, with the count of what waits for it.
  expect(saveButton().className).toContain("has-pending");
  expect(within(saveButton()).getByText("1")).toBeInTheDocument();
  const panel = screen.getByTestId("settings-pending");
  expect(panel).toHaveTextContent("This change takes effect only after Save");
  expect(panel).toHaveTextContent("Gateways › Telegram › Enabled: turn on");

  // The token goes out on its own; the switch does not.
  await pause(AUTOSAVE_MS + 50);
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(telegram(puts[0]!)).toEqual({ token: "123:abc" });
  expect(status()).toBe("1 change waits for Save");
  expect(saveButton().className).toContain("has-pending");

  await act(async () => {
    fireEvent.click(saveButton());
  });
  await waitFor(() => expect(puts).toHaveLength(2));
  expect(telegram(puts[1]!)).toEqual({ token: "123:abc", enable: true });
  await waitFor(() =>
    expect(saveButton().className).not.toContain("has-pending"),
  );
  expect(screen.queryByTestId("settings-pending")).toBeNull();
});

test("removing a provider waits for Save, and Discard puts it back", async () => {
  render(<Settings onClose={() => {}} initialSection="providers" />);
  fireEvent.click(await screen.findByRole("button", { name: "Remove demo" }));
  expect(screen.queryByRole("button", { name: "Remove demo" })).toBeNull();
  expect(screen.getByTestId("settings-pending")).toHaveTextContent(
    "LLM providers: demo removed",
  );

  // Nothing else changed, so nothing goes out on its own.
  await pause(AUTOSAVE_MS + 50);
  expect(puts).toHaveLength(0);

  fireEvent.click(screen.getByTestId("settings-pending-discard"));
  expect(
    screen.getByRole("button", { name: "Remove demo" }),
  ).toBeInTheDocument();
  expect(screen.queryByTestId("settings-pending")).toBeNull();
  expect(saveButton().className).not.toContain("has-pending");
  await pause(AUTOSAVE_MS + 50);
  expect(puts).toHaveLength(0);
});

test("closing Settings with a change waiting for Save asks first", async () => {
  const onClose = vi.fn();
  render(<Settings onClose={onClose} initialSection="gateways" />);
  fireEvent.click(await screen.findByRole("switch", { name: "Enabled" }));

  fireEvent.click(screen.getByTestId("settings-drawer-close"));
  const dialog = screen.getByTestId("settings-close-dialog");
  expect(dialog).toHaveTextContent(
    "Not applied until saved: Gateways › Telegram › Enabled: turn on.",
  );
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
  expect(telegram(puts[0]!).enable).toBe(true);
});

test("a change waiting for Save is still there when the drawer opens again", async () => {
  const view = render(
    <Settings onClose={() => {}} initialSection="gateways" />,
  );
  fireEvent.click(await screen.findByRole("switch", { name: "Enabled" }));
  view.unmount();
  await pause(AUTOSAVE_MS + 50);
  expect(puts).toHaveLength(0);

  render(<Settings onClose={() => {}} initialSection="gateways" />);
  expect(screen.getByRole("switch", { name: "Enabled" })).toHaveAttribute(
    "aria-checked",
    "true",
  );
  expect(saveButton().className).toContain("has-pending");
});

test("the next save goes out under the revision the last one answered with", async () => {
  render(<Settings onClose={() => {}} initialSection="agent" />);
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;
  fireEvent.change(input, { target: { value: "42" } });
  await pause(AUTOSAVE_MS + 50);
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]!.revision).toBe("rev-1");

  fireEvent.change(input, { target: { value: "40" } });
  await pause(AUTOSAVE_MS + 50);
  await waitFor(() => expect(puts).toHaveLength(2));
  expect(puts[1]!.revision).toBe("rev-2");
  expect((puts[1]!.agent as Doc).max_turns).toBe(40);
});

test("a provider added with Add waits for its name, and the rest of the form saves without it", async () => {
  render(<Settings onClose={() => {}} initialSection="providers" />);
  fireEvent.click(await screen.findByTestId("settings-master-add"));
  const name = screen.getByLabelText("Provider id") as HTMLInputElement;
  expect(name.value).toBe("");
  await pause(AUTOSAVE_MS + 50);
  expect(puts).toHaveLength(0);
  expect(status()).toBe("Unsaved changes");
  expect(document.querySelector(".settings-lead-pane")).toBeNull();

  fireEvent.change(name, { target: { value: "codex" } });
  await pause(AUTOSAVE_MS + 50);
  await waitFor(() => expect(puts).toHaveLength(1));
  expect((puts[0]!.providers as Doc[]).map((p) => p.name)).toEqual([
    "demo",
    "spare",
    "codex",
  ]);
});

// Edge cases.

test("a refused save says why, the status says not saved, and closing asks first", async () => {
  refuse = "agent.max_turns: must be positive";
  const onClose = vi.fn();
  const { container } = render(
    <Settings onClose={onClose} initialSection="agent" />,
  );
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;
  fireEvent.change(input, { target: { value: "-1" } });
  await pause(AUTOSAVE_MS + 50);
  await waitFor(() => expect(status()).toBe("Not saved"));
  expect(
    container.querySelector(".settings-lead-pane .settings-error")?.textContent,
  ).toBe("agent.max_turns: must be positive");

  fireEvent.click(screen.getByTestId("settings-drawer-close"));
  expect(screen.getByTestId("settings-close-dialog")).toHaveTextContent(
    "The last save was refused",
  );
  expect(onClose).not.toHaveBeenCalled();
});

test("closing with a save waiting for the pause sends it at once", async () => {
  const onClose = vi.fn();
  const view = render(<Settings onClose={onClose} initialSection="agent" />);
  const input = (await screen.findByLabelText("Max turns")) as HTMLInputElement;
  fireEvent.change(input, { target: { value: "50" } });
  fireEvent.click(screen.getByTestId("settings-drawer-close"));
  expect(onClose).toHaveBeenCalledTimes(1);
  expect(screen.queryByTestId("settings-close-dialog")).toBeNull();
  // App takes the drawer away on close; the save goes out then, not after
  // the pause.
  view.unmount();
  await waitFor(() => expect(puts).toHaveLength(1));
  expect((puts[0]!.agent as Doc).max_turns).toBe(50);
});

test("Save and Discard wait while a Save is on its way", async () => {
  let release: () => void = () => {};
  const held = new Promise<void>((r) => {
    release = r;
  });
  const fetch = globalThis.fetch as ReturnType<typeof vi.fn>;
  const answer = fetch.getMockImplementation()!;
  fetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if (init?.method === "PUT") {
      await held;
    }
    return answer(path, init);
  });
  render(<Settings onClose={() => {}} initialSection="gateways" />);
  fireEvent.click(await screen.findByRole("switch", { name: "Enabled" }));
  await act(async () => {
    fireEvent.click(saveButton());
  });
  expect(saveButton()).toBeDisabled();
  expect(screen.getByTestId("settings-pending-discard")).toBeDisabled();
  fireEvent.click(saveButton());
  await act(async () => {
    release();
    await held;
  });
  await waitFor(() => expect(saveButton()).not.toBeDisabled());
  expect(puts).toHaveLength(1);
});

test("a page left with a change waiting for Save asks the browser to confirm", async () => {
  render(<Settings onClose={() => {}} initialSection="gateways" />);
  const leave = () => {
    const e = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(e);
    return e.defaultPrevented;
  };
  await screen.findByRole("switch", { name: "Enabled" });
  expect(leave()).toBe(false);
  fireEvent.click(screen.getByRole("switch", { name: "Enabled" }));
  expect(leave()).toBe(true);
});

test("a switch turned on and off again holds nothing", async () => {
  render(<Settings onClose={() => {}} initialSection="gateways" />);
  const sw = await screen.findByRole("switch", { name: "Enabled" });
  fireEvent.click(sw);
  fireEvent.click(sw);
  expect(saveButton().className).not.toContain("has-pending");
  await pause(AUTOSAVE_MS + 50);
  expect(puts).toHaveLength(0);
});

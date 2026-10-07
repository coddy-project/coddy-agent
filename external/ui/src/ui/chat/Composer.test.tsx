import React, { useState } from "react";
import { afterEach, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { describe, expect, test } from "vitest";
import { Composer } from "./Composer";
import {
  recordWorkspaceAtRecent,
  WORKSPACE_AT_RECENTS_NO_SESSION_KEY,
} from "../skills/workspaceAtRecents";

afterEach(() => cleanup());

function renderComposer(opts: { isEmpty: boolean }) {
  return render(
    <Composer
      value=""
      isEmpty={opts.isEmpty}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
}

function renderComposerWithLlm(opts: { isEmpty: boolean }) {
  return render(
    <Composer
      value=""
      isEmpty={opts.isEmpty}
      mode="agent"
      modes={["agent", "plan"]}
      llmModels={["openai/gpt-4o-mini", "openai/gpt-4o"]}
      llmModel="openai/gpt-4o-mini"
      onLlmModelChange={() => {}}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
}

test("ask mode renders its own pill class and menu entry", () => {
  render(
    <Composer
      value=""
      isEmpty={true}
      mode="ask"
      modes={["agent", "plan", "ask"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );

  const pill = screen.getByRole("button", { name: "Mode" });
  expect(pill).toHaveClass("mode-ask");
  expect(pill).toHaveTextContent("Ask");

  fireEvent.click(pill);
  const menu = screen.getByRole("menu");
  expect(menu).toHaveTextContent("Ask");
});

test("mode menu opens down on start screen", () => {
  renderComposer({ isEmpty: true });

  fireEvent.click(screen.getByRole("button", { name: "Mode" }));

  const menu = screen.getByRole("menu");
  expect(menu).toHaveClass("opens-down");
});

test("mode menu opens up in active chat composer", () => {
  renderComposer({ isEmpty: false });

  fireEvent.click(screen.getByRole("button", { name: "Mode" }));

  const menu = screen.getByRole("menu");
  expect(menu).toHaveClass("opens-up");
});

test("switching session refocuses textarea in active chat", () => {
  const { rerender } = render(
    <Composer
      value=""
      isEmpty={false}
      sessionId="sess-a"
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const ta = screen.getByRole("textbox", { name: "Message" });
  expect(ta).toHaveFocus();
  ta.blur();
  rerender(
    <Composer
      value=""
      isEmpty={false}
      sessionId="sess-b"
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  expect(ta).toHaveFocus();
});

// On a phone or a tablet a focused field opens the on-screen keyboard over
// half of the screen: opening the start screen or a chat must not do that by
// itself. The keyboard comes when the reader taps the field.
test("a touch-only device keeps the keyboard closed on the start screen and in a chat", () => {
  stubViewport({ narrow: true, touchOnly: true });
  try {
    const hero = render(
      <Composer
        value=""
        isEmpty
        sessionId=""
        mode="agent"
        modes={["agent"]}
        onModeChange={() => {}}
        onChange={() => {}}
        onSend={() => {}}
      />,
    );
    expect(screen.getByRole("textbox", { name: "Message" })).not.toHaveFocus();
    hero.unmount();

    const { rerender } = render(
      <Composer
        value=""
        isEmpty={false}
        sessionId="sess-a"
        mode="agent"
        modes={["agent"]}
        onModeChange={() => {}}
        onChange={() => {}}
        onSend={() => {}}
      />,
    );
    const ta = screen.getByRole("textbox", { name: "Message" });
    expect(ta).not.toHaveFocus();
    rerender(
      <Composer
        value=""
        isEmpty={false}
        sessionId="sess-b"
        mode="agent"
        modes={["agent"]}
        onModeChange={() => {}}
        onChange={() => {}}
        onSend={() => {}}
      />,
    );
    expect(ta).not.toHaveFocus();
  } finally {
    vi.unstubAllGlobals();
  }
});

test("a narrow desktop window still focuses the field: it has a keyboard", () => {
  stubViewport({ narrow: true, touchOnly: false });
  try {
    render(
      <Composer
        value=""
        isEmpty
        sessionId=""
        mode="agent"
        modes={["agent"]}
        onModeChange={() => {}}
        onChange={() => {}}
        onSend={() => {}}
      />,
    );
    expect(screen.getByRole("textbox", { name: "Message" })).toHaveFocus();
  } finally {
    vi.unstubAllGlobals();
  }
});

test("yaml model menu opens down on start screen when backends exist", () => {
  renderComposerWithLlm({ isEmpty: true });

  fireEvent.click(screen.getByRole("button", { name: "Model" }));

  const menu = screen.getByRole("menu");
  expect(menu).toHaveClass("opens-down");
});

test("yaml model menu opens up in active chat composer", () => {
  renderComposerWithLlm({ isEmpty: false });

  fireEvent.click(screen.getByRole("button", { name: "Model" }));

  const menu = screen.getByRole("menu");
  expect(menu).toHaveClass("opens-up");
});

test("send play disabled when input empty", () => {
  renderComposer({ isEmpty: true });
  expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
});

test("send play enabled when draft has text", () => {
  render(
    <Composer
      value="hi"
      isEmpty={true}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  expect(screen.getByRole("button", { name: "Send" })).not.toBeDisabled();
});

test("click Send button calls onSend with trimmed text", () => {
  const onSend = vi.fn();
  render(
    <Composer
      value="  hello world  "
      isEmpty={true}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={onSend}
    />,
  );
  const btn = screen.getByRole("button", { name: "Send" });
  fireEvent.click(btn);
  expect(onSend).toHaveBeenCalledTimes(1);
  expect(onSend).toHaveBeenCalledWith("hello world");
});

test("pressing Enter calls onSend with trimmed text", () => {
  const onSend = vi.fn();
  render(
    <Composer
      value="  test input  "
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={onSend}
    />,
  );
  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.keyDown(ta, { key: "Enter", code: "Enter", charCode: 13 });
  expect(onSend).toHaveBeenCalledTimes(1);
  expect(onSend).toHaveBeenCalledWith("test input");
});

test("the slash menu lists /docs where the reader can open", async () => {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: true,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
    onchange: null,
  }));
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) =>
      Promise.resolve({
        ok: true,
        json: async () =>
          String(url).includes("/coddy/commands")
            ? {
                object: "coddy.commands",
                items: [{ name: "compact", description: "Summarize" }],
              }
            : { items: [], has_more: false, page: 1 },
      }),
    ),
  );
  function Harness() {
    const [value, setValue] = useState("");
    return (
      <Composer
        value={value}
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={setValue}
        onSend={() => {}}
        onDocsCommand={() => {}}
      />
    );
  }
  render(<Harness />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "/do", selectionStart: 3, selectionEnd: 3 },
  });
  // No skill matches "do", yet the menu stays open on the command.
  await waitFor(() => {
    expect(screen.getByTestId("command-row-docs")).toBeTruthy();
  });
  expect(screen.getByTestId("command-row-docs").textContent).toContain(
    "documentation",
  );
  vi.unstubAllGlobals();
});

test("Tab key selects first slash command from picker", async () => {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: true,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
    onchange: null,
  }));
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({
      items: [{ name: "rpa-gen-rules", description: "Generate project rules" }],
      has_more: false,
      page: 1,
    }),
  });
  vi.stubGlobal("fetch", fetchMock);

  const onChange = vi.fn();
  function Harness() {
    const [value, setValue] = useState("");
    return (
      <Composer
        value={value}
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={(v) => {
          setValue(v);
          onChange(v);
        }}
        onSend={() => {}}
      />
    );
  }

  render(<Harness />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "/gen", selectionStart: 4, selectionEnd: 4 },
  });

  await waitFor(() => {
    expect(
      screen.queryByRole("listbox", { name: "Slash commands" }),
    ).toBeTruthy();
  });

  fireEvent.keyDown(ta, { key: "Tab", code: "Tab" });

  await waitFor(() => {
    expect(onChange).toHaveBeenCalledWith("/rpa-gen-rules ");
  });
  expect(screen.queryByRole("listbox", { name: "Slash commands" })).toBeNull();
  vi.unstubAllGlobals();
});

test("composer highlights only the active slash draft at caret", () => {
  const s = "asdfasf /find-skills asdfasdf";
  render(
    <Composer
      value={s}
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const ta = screen.getByRole("textbox", {
    name: "Message",
  }) as HTMLTextAreaElement;
  const caret = s.indexOf("/") + "/find-skil".length;
  ta.focus();
  ta.setSelectionRange(caret, caret);
  fireEvent.select(ta);

  const chip = screen.getByTestId("composer-skill-chip");
  expect(chip).toHaveTextContent("/find-skil");
});

test("no slash chip and no menu after API returns zero commands for prefix", async () => {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({ items: [], has_more: false, page: 1 }),
  });
  vi.stubGlobal("fetch", fetchMock);

  function Harness() {
    const [value, setValue] = useState("");
    return (
      <Composer
        value={value}
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={setValue}
        onSend={() => {}}
      />
    );
  }

  render(<Harness />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "/as", selectionStart: 3, selectionEnd: 3 },
  });

  await waitFor(() => {
    expect(fetchMock).toHaveBeenCalled();
  });
  await waitFor(() => {
    expect(screen.queryByTestId("composer-skill-chip")).toBeNull();
  });
  expect(screen.queryByRole("listbox", { name: "Slash commands" })).toBeNull();

  vi.unstubAllGlobals();
});

test("extending a no-match prefix does not reopen slash menu or refetch", async () => {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({ items: [], has_more: false, page: 1 }),
  });
  vi.stubGlobal("fetch", fetchMock);

  function Harness() {
    const [value, setValue] = useState("");
    return (
      <Composer
        value={value}
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={setValue}
        onSend={() => {}}
      />
    );
  }

  render(<Harness />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "/adf", selectionStart: 4, selectionEnd: 4 },
  });
  // The composer also fetches /coddy/commands once on mount, so count only the
  // skills endpoint to prove the no-match prefix is not refetched.
  const slashCalls = () =>
    fetchMock.mock.calls.filter((c: unknown[]) =>
      String(c[0]).includes("/coddy/slash-commands"),
    ).length;
  await waitFor(() => expect(slashCalls()).toBe(1));
  fireEvent.change(ta, {
    target: {
      value: "/adfadsfgaf",
      selectionStart: "/adfadsfgaf".length,
      selectionEnd: "/adfadsfgaf".length,
    },
  });
  await new Promise((r) => setTimeout(r, 250));
  expect(slashCalls()).toBe(1);
  expect(screen.queryByRole("listbox", { name: "Slash commands" })).toBeNull();
  expect(screen.queryByTestId("composer-skill-chip")).toBeNull();

  vi.unstubAllGlobals();
});

test("slash menu shows a Commands group from /coddy/commands", async () => {
  // Force the mobile bottom-sheet menu so the picker renders without needing
  // getBoundingClientRect (null under jsdom), matching the Tab-picker test.
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: true,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
    onchange: null,
  }));
  const fetchMock = vi.fn((url: string) => {
    if (String(url).includes("/coddy/commands")) {
      return Promise.resolve({
        ok: true,
        json: async () => ({
          object: "coddy.commands",
          items: [
            { name: "compact", description: "Summarize history" },
            { name: "plugin", description: "Manage plugins" },
          ],
        }),
      });
    }
    return Promise.resolve({
      ok: true,
      json: async () => ({
        items: [{ name: "some-skill", description: "A skill" }],
        has_more: false,
        page: 1,
      }),
    });
  });
  vi.stubGlobal("fetch", fetchMock);

  function Harness() {
    const [value, setValue] = useState("");
    return (
      <Composer
        value={value}
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={setValue}
        onSend={() => {}}
      />
    );
  }

  render(<Harness />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "/", selectionStart: 1, selectionEnd: 1 },
  });

  // The built-in commands render as their own "Commands" group beside skills.
  await waitFor(() => {
    expect(screen.getByTestId("command-row-compact")).toBeTruthy();
  });
  expect(screen.getByTestId("command-row-plugin")).toBeTruthy();
  // /docs is the browser's own: listed only where it can open the reader.
  expect(screen.queryByTestId("command-row-docs")).toBeNull();
  expect(screen.getByText("Commands")).toBeTruthy();
  expect(screen.getByTestId("slash-command-row-some-skill")).toBeTruthy();

  vi.unstubAllGlobals();
});

// /docs is the console's help command; in the web UI it opens the reader
// instead of going to the agent as a prompt.
describe("/docs", () => {
  function renderWith(
    value: string,
    extra: Partial<Parameters<typeof Composer>[0]> = {},
  ) {
    const onSend = vi.fn();
    const onDocsCommand = vi.fn();
    const onQueue = vi.fn();
    render(
      <Composer
        value={value}
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={() => {}}
        onSend={onSend}
        onQueue={onQueue}
        onDocsCommand={onDocsCommand}
        {...extra}
      />,
    );
    return { onSend, onDocsCommand, onQueue };
  }

  test("opens the reader with what follows it, and sends nothing", () => {
    const { onSend, onDocsCommand } = renderWith("  /docs telegram proxy ");
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Message" }), {
      key: "Enter",
    });
    expect(onDocsCommand).toHaveBeenCalledWith("telegram proxy");
    expect(onSend).not.toHaveBeenCalled();
  });

  test("the send button does the same", () => {
    const { onSend, onDocsCommand } = renderWith("/docs");
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    expect(onDocsCommand).toHaveBeenCalledWith("");
    expect(onSend).not.toHaveBeenCalled();
  });

  test("works while a turn runs instead of joining the queue", () => {
    const { onQueue, onDocsCommand } = renderWith(
      "/docs features/mentions#completion",
      {
        generating: true,
      },
    );
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Message" }), {
      key: "Enter",
    });
    expect(onDocsCommand).toHaveBeenCalledWith("features/mentions#completion");
    expect(onQueue).not.toHaveBeenCalled();
  });

  test("a word that only starts like it is an ordinary prompt", () => {
    const { onSend, onDocsCommand } = renderWith("/docsify the readme");
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Message" }), {
      key: "Enter",
    });
    expect(onDocsCommand).not.toHaveBeenCalled();
    expect(onSend).toHaveBeenCalledWith("/docsify the readme");
  });
});

test("/mcp opens MCP settings without sending a prompt", () => {
  const onSend = vi.fn();
  const onMCPCommand = vi.fn();
  render(
    <Composer
      value=" /mcp "
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={onSend}
      onMCPCommand={onMCPCommand}
    />,
  );
  fireEvent.keyDown(screen.getByRole("textbox", { name: "Message" }), {
    key: "Enter",
  });
  expect(onMCPCommand).toHaveBeenCalledOnce();
  expect(onSend).not.toHaveBeenCalled();
});

// The console opens its /mcp controls whatever follows the command; the web
// composer does the same rather than send "/mcp github" to the model. A word
// that only starts with the command is an ordinary prompt.
test.each([
  ["/mcp github", true],
  ["/mcp\tgithub tools", true],
  ["/mcpx", false],
  ["/mcp-servers", false],
])("%j runs the MCP command: %s", (value, opens) => {
  const onSend = vi.fn();
  const onMCPCommand = vi.fn();
  render(
    <Composer
      value={value}
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={onSend}
      onMCPCommand={onMCPCommand}
    />,
  );
  fireEvent.keyDown(screen.getByRole("textbox", { name: "Message" }), {
    key: "Enter",
  });
  expect(onMCPCommand).toHaveBeenCalledTimes(opens ? 1 : 0);
  expect(onSend).toHaveBeenCalledTimes(opens ? 0 : 1);
});

test("generating shows stop and calls onStop", () => {
  let stopped = false;
  render(
    <Composer
      value=""
      isEmpty={true}
      generating={true}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
      onStop={() => {
        stopped = true;
      }}
    />,
  );
  const b = screen.getByRole("button", { name: "Stop generation" });
  expect(b).not.toBeDisabled();
  expect(b).toHaveClass("composer-send-stop");
  expect(
    b.querySelector(".composer-send-glyph .composer-stop-square"),
  ).toBeTruthy();
  expect(b.closest(".composer-bar-actions")).toBeTruthy();
  fireEvent.click(b);
  expect(stopped).toBe(true);
});

test("context tooltip percent and Max context follow cap when model max changes", () => {
  const usage = { inputTokens: 800, outputTokens: 200, totalTokens: 1000 };
  const { rerender } = render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      tokenUsage={usage}
      contextPct={1.0}
      maxContextTokens={100000}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const tip = () => screen.getByRole("tooltip").textContent ?? "";
  expect(tip()).toMatch(/1\.0% context used/);
  expect(tip()).toMatch(/Max context 100000/);

  rerender(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      tokenUsage={usage}
      contextPct={10.0}
      maxContextTokens={10000}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  expect(tip()).toMatch(/10\.0% context used/);
  expect(tip()).toMatch(/Max context 10000/);
});

test("context tooltip hidden until pointer leaves ring after closing breakdown", () => {
  const breakdown = {
    systemPrompt: 100,
    toolDefinitions: 200,
    rules: 0,
    skills: 0,
    mcp: 0,
    subagents: 0,
    conversation: 100,
    estimatedTotal: 400,
  };
  render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      contextPct={5}
      maxContextTokens={10000}
      contextBreakdown={breakdown}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const host = screen.getByTestId("composer-context-ring-host");
  fireEvent.mouseEnter(host);
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.click(host);
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.mouseDown(document.body);
  expect(screen.queryByTestId("context-breakdown-popover")).toBeNull();
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.mouseLeave(host);
  fireEvent.mouseEnter(host);
  expect(screen.getByRole("tooltip")).toBeTruthy();
});

test("click context ring opens breakdown popover; Escape closes", () => {
  const breakdown = {
    systemPrompt: 100,
    toolDefinitions: 200,
    rules: 300,
    skills: 150,
    mcp: 50,
    subagents: 0,
    conversation: 1200,
    estimatedTotal: 2000,
  };
  render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      tokenUsage={{ inputTokens: 800, outputTokens: 200, totalTokens: 1000 }}
      contextPct={10.0}
      maxContextTokens={10000}
      contextBreakdown={breakdown}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  expect(screen.queryByTestId("context-breakdown-popover")).toBeNull();
  fireEvent.click(screen.getByTestId("composer-context-ring-host"));
  expect(screen.getByTestId("context-breakdown-popover")).toBeTruthy();
  expect(screen.getByTestId("context-breakdown-row-rules")).toBeTruthy();
  fireEvent.keyDown(document, { key: "Escape" });
  expect(screen.queryByTestId("context-breakdown-popover")).toBeNull();
});

test("context popover percent follows breakdown not cumulative tokenUsage pct", () => {
  const breakdown = {
    systemPrompt: 851,
    toolDefinitions: 1950,
    rules: 14867,
    skills: 45,
    mcp: 0,
    subagents: 0,
    conversation: 6074,
    estimatedTotal: 23787,
  };
  render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      tokenUsage={{
        inputTokens: 800000,
        outputTokens: 20000,
        totalTokens: 820000,
      }}
      contextPct={100}
      maxContextTokens={128000}
      contextBreakdown={breakdown}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  fireEvent.click(screen.getByTestId("composer-context-ring-host"));
  expect(screen.getByText(/18\.6% [Uu]sed/)).toBeTruthy();
  const fg = document.querySelector(
    ".context-ring-fg",
  ) as SVGCircleElement | null;
  expect(fg).toBeTruthy();
  const c = 2 * Math.PI * 12;
  const off = Number.parseFloat(fg!.getAttribute("stroke-dashoffset") || "0");
  expect(off).toBeCloseTo(c * (1 - 23787 / 128000), 1);
});

test("context meter fill width reflects usage percent", () => {
  const breakdown = {
    systemPrompt: 500,
    toolDefinitions: 1000,
    rules: 0,
    skills: 100,
    mcp: 0,
    subagents: 0,
    conversation: 400,
    estimatedTotal: 2000,
  };
  render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      tokenUsage={{ inputTokens: 800, outputTokens: 200, totalTokens: 1000 }}
      contextPct={10.0}
      maxContextTokens={20000}
      contextBreakdown={breakdown}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  fireEvent.click(screen.getByTestId("composer-context-ring-host"));
  const fill = screen.getByTestId("context-meter-fill");
  expect(fill.style.width).toBe("10%");
});
function stubMatchMediaMobile(isMobile: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: isMobile,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }));
}

// The card holds the field and the bar, nothing above the field: the wand
// stands in the field's top right corner, so the placeholder starts at the top.
test("the improve-prompt button stands in the field's corner and the card has no chip row", () => {
  stubMatchMediaMobile(false);
  render(
    <Composer
      value="fix memory thing"
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const button = screen.getByTestId("composer-enhance-btn");
  expect(button).toHaveAttribute("title", "Improve prompt");
  expect(button.parentElement).toHaveClass("composer-field-wrap");
  expect(button.closest(".composer-bar")).toBeNull();
  expect(document.querySelector(".composer-context-row")).toBeNull();
  // The environment is an item of the nav rail, not a chip of the composer.
  expect(screen.queryByTestId("nav-environment")).toBeNull();
  vi.unstubAllGlobals();
});

const pickCtx = {
  path: "/w/demo",
  name: "demo",
  is_git_repo: true,
  is_worktree: false,
  repo_root: "/w/demo",
  branch: "main",
  branches: ["main", "feat/x"],
};

// Before the chat starts, where it will work is still a choice: the plate over
// the card offers the folder and the branch as picks and the worktree as a
// checkbox. Git's count waits for a session.
test("before a chat starts the folder, branch and worktree are picks on the plate over the card", () => {
  stubMatchMediaMobile(false);
  const onWorkspacePickBranch = vi.fn();
  render(
    <Composer
      value=""
      isEmpty={true}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
      workspaceCtx={pickCtx}
      onWorkspacePickFolder={() => {}}
      onWorkspacePickBranch={onWorkspacePickBranch}
      onWorktreeToggle={() => {}}
    />,
  );
  const plate = screen.getByTestId("workspace-bar");
  expect(plate).toHaveClass("workspace-bar--pick");
  const card = document.querySelector(".composer-card")!;
  expect(plate.nextElementSibling).toBe(card);
  expect(card).toHaveClass("composer-card--joined");
  const folder = within(plate).getByTestId("composer-workspace-chip");
  expect(folder.tagName).toBe("BUTTON");
  expect(folder.textContent).toBe("demo");
  const branch = within(plate).getByTestId("composer-branch-chip");
  expect(branch.textContent).toBe("main");
  expect(within(plate).getByTestId("composer-worktree-checkbox")).toBeTruthy();
  expect(within(plate).queryByTestId("workspace-bar-edits")).toBeNull();
  fireEvent.click(branch);
  fireEvent.click(screen.getByTestId("workspace-branch-row-feat/x"));
  expect(onWorkspacePickBranch).toHaveBeenCalledWith("feat/x", false);
  vi.unstubAllGlobals();
});

// The worktree choice is the browser's for every folder, but a folder with no
// git index has no branch to switch: the plate offers the folder alone, even
// with the worktree checkbox switched on.
test("a folder in no repository: the plate offers the folder alone", () => {
  stubMatchMediaMobile(false);
  render(
    <Composer
      worktreePref={true}
      value=""
      isEmpty={true}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
      workspaceCtx={{
        path: "/tmp/plain",
        name: "plain",
        is_git_repo: false,
        is_worktree: false,
      }}
      onWorkspacePickFolder={() => {}}
    />,
  );
  const plate = screen.getByTestId("workspace-bar");
  expect(within(plate).getByTestId("composer-workspace-chip").textContent).toBe(
    "plain",
  );
  expect(within(plate).queryByTestId("composer-branch-chip")).toBeNull();
  expect(within(plate).queryByTestId("composer-worktree-checkbox")).toBeNull();
  expect(within(plate).queryByTestId("workspace-bar-edits")).toBeNull();
  vi.unstubAllGlobals();
});

test("enhance button posts the draft and replaces it with the result", async () => {
  stubMatchMediaMobile(false);
  const onChange = vi.fn();
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({
      object: "coddy.enhance_prompt",
      text: "Refactor the memory endpoint and add tests.",
    }),
  });
  vi.stubGlobal("fetch", fetchMock);
  render(
    <Composer
      value="fix memory thing"
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={onChange}
      onSend={() => {}}
    />,
  );

  fireEvent.click(screen.getByTestId("composer-enhance-btn"));
  await waitFor(() => {
    expect(onChange).toHaveBeenCalledWith(
      "Refactor the memory endpoint and add tests.",
    );
  });
  const call = fetchMock.mock.calls.find(
    ([url]) => url === "/coddy/enhance-prompt",
  );
  expect(call).toBeDefined();
  expect(call![0]).toBe("/coddy/enhance-prompt");
  expect(JSON.parse((call![1] as RequestInit).body as string)).toEqual({
    text: "fix memory thing",
  });
  vi.unstubAllGlobals();
});

test("enhance request carries the active session id", async () => {
  stubMatchMediaMobile(false);
  const fetchMock = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({ text: "Better draft." }),
  });
  vi.stubGlobal("fetch", fetchMock);
  render(
    <Composer
      value="fix memory thing"
      isEmpty={false}
      sessionId="sess_abc123"
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );

  fireEvent.click(screen.getByTestId("composer-enhance-btn"));
  await waitFor(() => expect(fetchMock).toHaveBeenCalled());
  const call = fetchMock.mock.calls.find(
    ([url]) => url === "/coddy/enhance-prompt",
  );
  expect(call).toBeDefined();
  const init = call![1] as RequestInit;
  expect((init.headers as Record<string, string>)["X-Coddy-Session-ID"]).toBe(
    "sess_abc123",
  );
  vi.unstubAllGlobals();
});

test("Ctrl+Z restores the draft before prompt enhancement", async () => {
  stubMatchMediaMobile(false);
  const onChange = vi.fn();
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ text: "Better draft." }),
    }),
  );
  render(
    <Composer
      value="fix memory thing"
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={(_mode) => {}}
      onChange={onChange}
      onSend={() => {}}
    />,
  );

  fireEvent.click(screen.getByTestId("composer-enhance-btn"));
  await waitFor(() => expect(onChange).toHaveBeenCalledWith("Better draft."));
  const ta = screen.getByRole("textbox", { name: "Message" });
  // A Ctrl+Z the input method is composing with is its own undo.
  fireEvent.keyDown(ta, { key: "z", ctrlKey: true, isComposing: true });
  expect(onChange).toHaveBeenLastCalledWith("Better draft.");
  fireEvent.keyDown(ta, { key: "z", ctrlKey: true });
  expect(onChange).toHaveBeenLastCalledWith("fix memory thing");
  vi.unstubAllGlobals();
});

/**
 * Answers `matchMedia` per query: `narrow` for the width breakpoint of the
 * stacked shell, `touchOnly` for the no-hover coarse-pointer query. The Enter
 * rule follows the input device, the layout follows the width, and a narrow
 * desktop window is the case where the two differ.
 */
function stubViewport(opts: { narrow: boolean; touchOnly: boolean }) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: query.includes("max-width")
      ? opts.narrow
      : query.includes("hover") || query.includes("pointer")
        ? opts.touchOnly
        : false,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }));
}

function renderEnterComposer(value = "hello") {
  const onSend = vi.fn();
  const onChange = vi.fn();
  render(
    <Composer
      value={value}
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={onChange}
      onSend={onSend}
    />,
  );
  const ta = screen.getByRole("textbox", {
    name: "Message",
  }) as HTMLTextAreaElement;
  return { onSend, onChange, ta };
}

test("a narrow desktop window: Enter sends", () => {
  stubViewport({ narrow: true, touchOnly: false });
  const { onSend, ta } = renderEnterComposer();
  expect(fireEvent.keyDown(ta, { key: "Enter" })).toBe(false);
  expect(onSend).toHaveBeenCalledTimes(1);
  expect(onSend).toHaveBeenCalledWith("hello");
  vi.unstubAllGlobals();
});

test("Ctrl+Enter inserts a newline at the caret and does not send", () => {
  stubViewport({ narrow: false, touchOnly: false });
  const { onSend, onChange, ta } = renderEnterComposer();
  ta.setSelectionRange(3, 3);
  expect(fireEvent.keyDown(ta, { key: "Enter", ctrlKey: true })).toBe(false);
  expect(onSend).not.toHaveBeenCalled();
  expect(onChange).toHaveBeenCalledWith("hel\nlo");
  vi.unstubAllGlobals();
});

test("Ctrl+Enter replaces a selection with the newline", () => {
  stubViewport({ narrow: true, touchOnly: false });
  const { onSend, onChange, ta } = renderEnterComposer();
  ta.setSelectionRange(1, 4);
  fireEvent.keyDown(ta, { key: "Enter", ctrlKey: true });
  expect(onSend).not.toHaveBeenCalled();
  expect(onChange).toHaveBeenCalledWith("h\no");
  vi.unstubAllGlobals();
});

test("Ctrl+Enter expands a fence marker into an editable monospace code block", () => {
  stubViewport({ narrow: false, touchOnly: false });
  const { onSend, onChange, ta } = renderEnterComposer("before\n```");
  ta.setSelectionRange("before\n```".length, "before\n```".length);
  fireEvent.keyDown(ta, { key: "Enter", ctrlKey: true });

  expect(onSend).not.toHaveBeenCalled();
  expect(onChange).toHaveBeenCalledWith("before\n```\n\n```");
  expect(ta.closest(".composer-stack")).toHaveClass("composer-code-editing");
  vi.unstubAllGlobals();
});

test("Shift+Enter leaves the newline to the browser and does not send", () => {
  stubViewport({ narrow: false, touchOnly: false });
  const { onSend, onChange, ta } = renderEnterComposer();
  expect(fireEvent.keyDown(ta, { key: "Enter", shiftKey: true })).toBe(true);
  expect(onSend).not.toHaveBeenCalled();
  expect(onChange).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
});

test("Enter that confirms an IME candidate does not send", () => {
  stubViewport({ narrow: false, touchOnly: false });
  const { onSend, ta } = renderEnterComposer();
  fireEvent.keyDown(ta, { key: "Enter", isComposing: true });
  fireEvent.keyDown(ta, { key: "Enter", keyCode: 229 });
  expect(onSend).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
});

test("a touch-only phone: Return inserts a newline and the Send button sends", () => {
  stubViewport({ narrow: true, touchOnly: true });
  const { onSend, ta } = renderEnterComposer();
  expect(fireEvent.keyDown(ta, { key: "Enter" })).toBe(true);
  expect(onSend).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(onSend).toHaveBeenCalledWith("hello");
  vi.unstubAllGlobals();
});

test("the keyboard's Enter key is labelled send, or enter on a touch-only phone", () => {
  stubViewport({ narrow: true, touchOnly: false });
  const first = renderEnterComposer();
  expect(first.ta).toHaveAttribute("enterkeyhint", "send");
  cleanup();
  stubViewport({ narrow: true, touchOnly: true });
  const second = renderEnterComposer();
  expect(second.ta).toHaveAttribute("enterkeyhint", "enter");
  vi.unstubAllGlobals();
});

test("attach button hidden when llmModelMultimodal is false", () => {
  stubMatchMediaMobile(false);
  render(
    <Composer
      value=""
      isEmpty={true}
      mode="agent"
      modes={["agent", "plan"]}
      llmModels={["openai/gpt-4o"]}
      llmModel="openai/gpt-4o"
      llmModelMultimodal={false}
      onLlmModelChange={() => {}}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  expect(screen.queryByTestId("composer-attach-btn")).toBeNull();
  vi.unstubAllGlobals();
});

test("attach button visible when llmModelMultimodal is true", () => {
  stubMatchMediaMobile(false);
  render(
    <Composer
      value=""
      isEmpty={true}
      mode="agent"
      modes={["agent", "plan"]}
      llmModels={["openai/gpt-4o"]}
      llmModel="openai/gpt-4o"
      llmModelMultimodal={true}
      onLlmModelChange={() => {}}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  expect(screen.getByTestId("composer-attach-btn")).toBeTruthy();
  vi.unstubAllGlobals();
});

test("selecting a file shows attachment chip", async () => {
  stubMatchMediaMobile(false);
  render(
    <Composer
      value="hello"
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModelMultimodal={true}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const fileInput = screen.getByTestId(
    "composer-file-input",
  ) as HTMLInputElement;
  const file = new File(["content"], "photo.png", { type: "image/png" });
  fireEvent.change(fileInput, { target: { files: [file] } });
  await waitFor(() => {
    expect(screen.getByText("photo.png")).toBeTruthy();
  });
  vi.unstubAllGlobals();
});

test("send with attached file passes files to onSend", async () => {
  stubMatchMediaMobile(false);
  const onSend = vi.fn();
  render(
    <Composer
      value="describe this"
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModelMultimodal={true}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={onSend}
    />,
  );
  const fileInput = screen.getByTestId(
    "composer-file-input",
  ) as HTMLInputElement;
  const file = new File(["data"], "img.png", { type: "image/png" });
  fireEvent.change(fileInput, { target: { files: [file] } });
  await waitFor(() => screen.getByText("img.png"));
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(onSend).toHaveBeenCalledWith("describe this", [file]);
  vi.unstubAllGlobals();
});

test("Tab queues the alternate mode and clears attached images", async () => {
  stubMatchMediaMobile(false);
  const onQueue = vi.fn();
  render(
    <Composer
      value="inspect this"
      isEmpty={false}
      generating={true}
      mode="agent"
      modes={["agent"]}
      llmModelMultimodal={true}
      queueMode="steer"
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
      onQueue={onQueue}
    />,
  );
  const file = new File(["image"], "img.png", { type: "image/png" });
  fireEvent.change(screen.getByTestId("composer-file-input"), {
    target: { files: [file] },
  });
  await waitFor(() => screen.getByText("img.png"));
  fireEvent.keyDown(screen.getByRole("textbox", { name: "Message" }), {
    key: "Tab",
  });
  expect(onQueue).toHaveBeenCalledWith("inspect this", "after_turn", [file]);
  expect(screen.queryByText("img.png")).toBeNull();
  vi.unstubAllGlobals();
});

/** jsdom has no real clipboard: dispatch a native paste event carrying image items. */
function pasteWithImages(el: Element, files: File[]) {
  const ev = new Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(ev, "clipboardData", {
    value: {
      items: files.map((f) => ({
        kind: "file",
        type: f.type,
        getAsFile: () => f,
      })),
    },
    configurable: true,
  });
  fireEvent(el, ev);
}

/** jsdom has no DataTransfer: dispatch a native drop event carrying files. */
function dropFiles(el: Element, files: File[]) {
  const ev = new Event("drop", { bubbles: true, cancelable: true });
  Object.defineProperty(ev, "dataTransfer", {
    value: { types: ["Files"], files },
    configurable: true,
  });
  fireEvent(el, ev);
}

test("pasting an image attaches it under a deterministic pasted-N name", () => {
  stubMatchMediaMobile(false);
  render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModelMultimodal={true}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const ta = screen.getByRole("textbox", { name: "Message" });
  pasteWithImages(ta, [new File(["img"], "image.png", { type: "image/png" })]);
  expect(screen.getByText("pasted-1.png")).toBeTruthy();
  pasteWithImages(ta, [
    new File(["img2"], "image.png", { type: "image/jpeg" }),
  ]);
  expect(screen.getByText("pasted-2.jpg")).toBeTruthy();
  vi.unstubAllGlobals();
});

test("pasting an image when the model is not multimodal shows a hint and no chip", () => {
  stubMatchMediaMobile(false);
  render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModelMultimodal={false}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const ta = screen.getByRole("textbox", { name: "Message" });
  pasteWithImages(ta, [new File(["img"], "image.png", { type: "image/png" })]);
  expect(screen.getByTestId("composer-attach-hint").textContent).toBe(
    "Selected model cannot accept attachments",
  );
  expect(screen.queryByText("pasted-1.png")).toBeNull();
  vi.unstubAllGlobals();
});

test("plain-text paste attaches nothing and shows no hint", () => {
  stubMatchMediaMobile(false);
  render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModelMultimodal={false}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const ta = screen.getByRole("textbox", { name: "Message" });
  const ev = new Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(ev, "clipboardData", { value: { items: [] } });
  fireEvent(ta, ev);
  expect(screen.queryByTestId("composer-attach-hint")).toBeNull();
  expect(screen.queryByText("pasted-1.png")).toBeNull();
  vi.unstubAllGlobals();
});

test("dropping files on the composer card attaches them", () => {
  stubMatchMediaMobile(false);
  const { container } = render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModelMultimodal={true}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const card = container.querySelector(".composer-card") as HTMLElement;
  dropFiles(card, [new File(["data"], "img.png", { type: "image/png" })]);
  expect(screen.getByText("img.png")).toBeTruthy();
  vi.unstubAllGlobals();
});

test("dropping files when the model is not multimodal shows a hint", () => {
  stubMatchMediaMobile(false);
  const { container } = render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModelMultimodal={false}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const card = container.querySelector(".composer-card") as HTMLElement;
  dropFiles(card, [new File(["data"], "img.png", { type: "image/png" })]);
  expect(screen.getByTestId("composer-attach-hint").textContent).toBe(
    "Selected model cannot accept attachments",
  );
  expect(screen.queryByText("img.png")).toBeNull();
  vi.unstubAllGlobals();
});

test("dragging files over the composer card toggles the drop-target affordance", () => {
  stubMatchMediaMobile(false);
  const { container } = render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const card = container.querySelector(".composer-card") as HTMLElement;
  const dragOver = new Event("dragover", { bubbles: true, cancelable: true });
  Object.defineProperty(dragOver, "dataTransfer", {
    value: { types: ["Files"] },
  });
  fireEvent(card, dragOver);
  expect(card).toHaveClass("composer-card--dragover");
  const dragLeave = new Event("dragleave", { bubbles: true, cancelable: true });
  fireEvent(card, dragLeave);
  expect(card).not.toHaveClass("composer-card--dragover");
  vi.unstubAllGlobals();
});

test("image attachments render a thumbnail; non-image ones keep the icon", () => {
  stubMatchMediaMobile(false);
  const createObjectURL = vi.fn(() => "blob:coddy-thumb-1");
  const revokeObjectURL = vi.fn();
  const urlCtor = URL as unknown as {
    createObjectURL?: ((f: File) => string) | undefined;
    revokeObjectURL?: ((u: string) => void) | undefined;
  };
  const origCreate = urlCtor.createObjectURL;
  const origRevoke = urlCtor.revokeObjectURL;
  urlCtor.createObjectURL = createObjectURL;
  urlCtor.revokeObjectURL = revokeObjectURL;
  try {
    render(
      <Composer
        value=""
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        llmModelMultimodal={true}
        onModeChange={() => {}}
        onChange={() => {}}
        onSend={() => {}}
      />,
    );
    const fileInput = screen.getByTestId(
      "composer-file-input",
    ) as HTMLInputElement;
    fireEvent.change(fileInput, {
      target: {
        files: [
          new File(["data"], "img.png", { type: "image/png" }),
          new File(["data"], "notes.txt", { type: "text/plain" }),
        ],
      },
    });
    const thumbs = screen.getAllByTestId("composer-attachment-thumb");
    expect(thumbs).toHaveLength(1);
    expect(thumbs[0]?.getAttribute("src")).toBe("blob:coddy-thumb-1");
    const imgChip = thumbs[0]?.closest(".composer-attachment-chip");
    expect(imgChip).toHaveClass("composer-attachment-chip--image");
    expect(screen.getByText("notes.txt")).toBeTruthy();
  } finally {
    urlCtor.createObjectURL = origCreate;
    urlCtor.revokeObjectURL = origRevoke;
    vi.unstubAllGlobals();
  }
});

/** Renders the composer with one image and one text attachment already picked. */
function renderComposerWithAttachments(onSend = () => {}) {
  stubMatchMediaMobile(false);
  const urlCtor = URL as unknown as {
    createObjectURL?: ((f: File) => string) | undefined;
    revokeObjectURL?: ((u: string) => void) | undefined;
  };
  const orig = {
    create: urlCtor.createObjectURL,
    revoke: urlCtor.revokeObjectURL,
  };
  urlCtor.createObjectURL = vi.fn(() => "blob:coddy-card-1");
  urlCtor.revokeObjectURL = vi.fn();
  const view = render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModelMultimodal={true}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={onSend}
    />,
  );
  fireEvent.change(screen.getByTestId("composer-file-input"), {
    target: {
      files: [
        new File(["data"], "img.png", { type: "image/png" }),
        new File(["data"], "notes.txt", { type: "text/plain" }),
      ],
    },
  });
  return {
    ...view,
    restore: () => {
      urlCtor.createObjectURL = orig.create;
      urlCtor.revokeObjectURL = orig.revoke;
      vi.unstubAllGlobals();
    },
  };
}

// An image is big enough to recognise before it is sent, and a click enlarges
// it in the same viewer the documentation reader uses. A file that is not an
// image keeps the icon chip it always had.
test("an image attachment is a preview card that opens the picture enlarged", () => {
  const { restore } = renderComposerWithAttachments();
  try {
    const thumb = screen.getByTestId("composer-attachment-thumb");
    const card = thumb.closest(".composer-attachment-chip");
    expect(card).toHaveClass("composer-attachment-card");
    expect(
      screen.getByText("notes.txt").closest(".composer-attachment-chip"),
    ).not.toHaveClass("composer-attachment-card");

    expect(document.querySelector(".docs-lightbox")).toBeNull();
    fireEvent.click(screen.getByLabelText("Open img.png enlarged"));
    const shown = document.querySelector(
      ".docs-lightbox-stage img",
    ) as HTMLImageElement | null;
    expect(shown?.getAttribute("src")).toBe("blob:coddy-card-1");

    fireEvent.click(screen.getByTestId("docs-lightbox-close"));
    expect(document.querySelector(".docs-lightbox")).toBeNull();
  } finally {
    restore();
  }
});

// The remove control moved onto the card, over the picture: it must still take
// the attachment away rather than enlarge what it is removing.
test("removing a preview card drops the attachment and opens nothing", () => {
  const { restore } = renderComposerWithAttachments();
  try {
    fireEvent.click(screen.getByLabelText("Remove img.png"));
    expect(screen.queryByTestId("composer-attachment-thumb")).toBeNull();
    expect(document.querySelector(".docs-lightbox")).toBeNull();
    expect(screen.getByText("notes.txt")).toBeTruthy();
  } finally {
    restore();
  }
});

test("send is enabled by an image alone and sends empty text with the files", async () => {
  stubMatchMediaMobile(false);
  const onSend = vi.fn();
  render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModelMultimodal={true}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={onSend}
    />,
  );
  const sendBtn = screen.getByRole("button", {
    name: "Send",
  }) as HTMLButtonElement;
  expect(sendBtn.disabled).toBe(true);
  const ta = screen.getByRole("textbox", { name: "Message" });
  pasteWithImages(ta, [new File(["img"], "image.png", { type: "image/png" })]);
  await waitFor(() => screen.getByText("pasted-1.png"));
  expect(sendBtn.disabled).toBe(false);
  fireEvent.click(sendBtn);
  expect(onSend).toHaveBeenCalledTimes(1);
  const [sentText, sentFiles] = onSend.mock.calls[0] as [string, File[]];
  expect(sentText).toBe("");
  expect(sentFiles).toHaveLength(1);
  expect(sentFiles[0]?.name).toBe("pasted-1.png");
  vi.unstubAllGlobals();
});

test("attached images stay visible but are not sent after switching to a non-multimodal model", async () => {
  stubMatchMediaMobile(false);
  const onSend = vi.fn();
  const common = {
    isEmpty: false,
    mode: "agent",
    modes: ["agent", "plan"],
    onModeChange: () => {},
    onChange: () => {},
    onSend,
  };
  const { rerender } = render(
    <Composer {...common} value="" llmModelMultimodal={true} />,
  );
  const fileInput = screen.getByTestId(
    "composer-file-input",
  ) as HTMLInputElement;
  fireEvent.change(fileInput, {
    target: {
      files: [new File(["img"], "photo.png", { type: "image/png" })],
    },
  });
  await waitFor(() => screen.getByText("photo.png"));

  rerender(<Composer {...common} value="" llmModelMultimodal={false} />);
  const chip = screen
    .getByText("photo.png")
    .closest(".composer-attachment-chip");
  expect(chip).toHaveClass("composer-attachment-chip--disabled");
  expect(chip).toHaveAttribute("aria-disabled", "true");
  expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();

  rerender(
    <Composer
      {...common}
      value="send only this text"
      llmModelMultimodal={false}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(onSend).toHaveBeenCalledTimes(1);
  expect(onSend).toHaveBeenCalledWith("send only this text");
  expect(screen.getByText("photo.png")).toBeTruthy();
  vi.unstubAllGlobals();
});

test("Enter sends an image-only message", async () => {
  stubMatchMediaMobile(false);
  const onSend = vi.fn();
  render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      llmModelMultimodal={true}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={onSend}
    />,
  );
  const ta = screen.getByRole("textbox", { name: "Message" });
  pasteWithImages(ta, [new File(["img"], "image.png", { type: "image/png" })]);
  await waitFor(() => screen.getByText("pasted-1.png"));
  fireEvent.keyDown(ta, { key: "Enter" });
  expect(onSend).toHaveBeenCalledTimes(1);
  const [sentText, sentFiles] = onSend.mock.calls[0] as [string, File[]];
  expect(sentText).toBe("");
  expect(sentFiles).toHaveLength(1);
  vi.unstubAllGlobals();
});

test("arrow keys move the slash highlight and Enter picks the highlighted row", async () => {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: true,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
    onchange: null,
  }));
  const fetchMock = vi.fn((url: string) => {
    if (String(url).includes("/coddy/commands")) {
      return Promise.resolve({
        ok: true,
        json: async () => ({
          object: "coddy.commands",
          items: [
            { name: "compact", description: "c" },
            { name: "plugin", description: "p" },
          ],
        }),
      });
    }
    return Promise.resolve({
      ok: true,
      json: async () => ({
        items: [{ name: "review", description: "r" }],
        has_more: false,
        page: 1,
      }),
    });
  });
  vi.stubGlobal("fetch", fetchMock);

  const onChange = vi.fn();
  function Harness() {
    const [value, setValue] = useState("");
    return (
      <Composer
        value={value}
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={(v) => {
          setValue(v);
          onChange(v);
        }}
        onSend={() => {}}
      />
    );
  }

  render(<Harness />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "/", selectionStart: 1, selectionEnd: 1 },
  });

  await waitFor(() => {
    expect(screen.getByTestId("command-row-compact")).toBeTruthy();
  });
  // The first row (a skill) is highlighted by default.
  expect(
    screen
      .getByTestId("slash-command-row-review")
      .getAttribute("aria-selected"),
  ).toBe("true");

  // ArrowDown moves the highlight to the next row (the first command).
  fireEvent.keyDown(ta, { key: "ArrowDown" });
  expect(
    screen.getByTestId("command-row-compact").getAttribute("aria-selected"),
  ).toBe("true");

  // Enter picks the highlighted row and appends it to the input.
  fireEvent.keyDown(ta, { key: "Enter" });
  await waitFor(() => expect(onChange).toHaveBeenCalledWith("/compact "));
});

// --- @path:N-M line-range picker ---

function stubShell(mobile: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: mobile,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
    onchange: null,
  }));
}

/** Serves the line-range panel's file read; anything else 404s. */
function stubWorkspaceFileFetch(lines: string[]) {
  const fetchMock = vi.fn((input: string) =>
    Promise.resolve(
      String(input).startsWith("/coddy/workspace/file?")
        ? {
            ok: true,
            json: async () => ({
              lines,
              total_lines: lines.length,
              truncated: false,
            }),
          }
        : { ok: false, status: 404, json: async () => ({}) },
    ),
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function RangeHarness(props: {
  initial: string;
  onChange: (v: string) => void;
}) {
  const [value, setValue] = useState(props.initial);
  return (
    <Composer
      value={value}
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={(v) => {
        setValue(v);
        props.onChange(v);
      }}
      onSend={() => {}}
    />
  );
}

test("a colon after a file mention opens the line-range picker", async () => {
  stubShell(true);
  stubWorkspaceFileFetch(["alpha", "beta", "gamma"]);
  render(<RangeHarness initial="" onChange={() => {}} />);

  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "@f.txt:", selectionStart: 7, selectionEnd: 7 },
  });

  await waitFor(() => {
    expect(screen.queryByTestId("at-range-picker")).toBeTruthy();
  });
  expect(screen.getByTestId("at-range-lines")).toHaveTextContent("alpha");
  // The file picker is gone: the colon handed the draft over.
  expect(screen.queryByTestId("workspace-files-menu")).toBeNull();
  vi.unstubAllGlobals();
});

test("typed digits highlight the selected lines", async () => {
  stubShell(true);
  stubWorkspaceFileFetch(["one", "two", "three", "four"]);
  render(<RangeHarness initial="" onChange={() => {}} />);

  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "@f.txt:", selectionStart: 7, selectionEnd: 7 },
  });
  await waitFor(() => {
    expect(screen.queryByTestId("at-range-picker")).toBeTruthy();
  });
  fireEvent.change(ta, {
    target: { value: "@f.txt:2-3", selectionStart: 10, selectionEnd: 10 },
  });

  await waitFor(() => {
    expect(screen.getByTestId("at-range-current")).toHaveTextContent("2-3");
  });
  const rows = screen
    .getByTestId("at-range-lines")
    .querySelectorAll(".at-range-line--sel");
  expect(Array.from(rows).map((r) => r.getAttribute("data-line"))).toEqual([
    "2",
    "3",
  ]);
  vi.unstubAllGlobals();
});

// A half-typed range still shows where it starts.
test("a start without an end highlights one line", async () => {
  stubShell(true);
  stubWorkspaceFileFetch(["one", "two", "three"]);
  render(<RangeHarness initial="" onChange={() => {}} />);

  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "@f.txt:2", selectionStart: 8, selectionEnd: 8 },
  });

  await waitFor(() => {
    expect(screen.getByTestId("at-range-current")).toHaveTextContent("2-2");
  });
  vi.unstubAllGlobals();
});

test("mobile shells render display-only rows with no mouse selection", async () => {
  stubShell(true);
  stubWorkspaceFileFetch(["one", "two"]);
  render(<RangeHarness initial="" onChange={() => {}} />);

  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "@f.txt:", selectionStart: 7, selectionEnd: 7 },
  });
  await waitFor(() => {
    expect(screen.queryByTestId("at-range-picker")).toBeTruthy();
  });

  expect(screen.queryByTestId("at-range-line-1")).toBeNull();
  expect(
    screen.getByTestId("at-range-lines").querySelectorAll("button"),
  ).toHaveLength(0);
  vi.unstubAllGlobals();
});

test("the picker closes once the mention token ends", async () => {
  stubShell(true);
  stubWorkspaceFileFetch(["one", "two"]);
  render(<RangeHarness initial="" onChange={() => {}} />);

  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "@f.txt:1-2", selectionStart: 10, selectionEnd: 10 },
  });
  await waitFor(() => {
    expect(screen.queryByTestId("at-range-picker")).toBeTruthy();
  });

  fireEvent.change(ta, {
    target: { value: "@f.txt:1-2 ", selectionStart: 11, selectionEnd: 11 },
  });
  await waitFor(() => {
    expect(screen.queryByTestId("at-range-picker")).toBeNull();
  });
  vi.unstubAllGlobals();
});

test("prose that never resolves to a file leaves the picker closed", async () => {
  stubShell(true);
  // Every read 404s, so nothing should open.
  stubWorkspaceFileFetch([]);
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve({ ok: false, status: 404, json: async () => ({}) }),
    ),
  );
  render(<RangeHarness initial="" onChange={() => {}} />);

  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "@nope.txt:1-2", selectionStart: 13, selectionEnd: 13 },
  });

  await waitFor(() => {
    expect(
      (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.length,
    ).toBeGreaterThan(0);
  });
  expect(screen.queryByTestId("at-range-picker")).toBeNull();
  vi.unstubAllGlobals();
});

/**
 * Desktop picker floats next to the field, so it needs a measurable wrapper and a
 * ResizeObserver; jsdom supplies neither. Returns a restore function.
 */
function stubDesktopLayout(): () => void {
  const realRect = Element.prototype.getBoundingClientRect;
  Element.prototype.getBoundingClientRect = function () {
    return {
      x: 0,
      y: 100,
      top: 100,
      left: 0,
      right: 400,
      bottom: 160,
      width: 400,
      height: 60,
      toJSON: () => ({}),
    } as DOMRect;
  };
  const realRO = globalThis.ResizeObserver;
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  return () => {
    Element.prototype.getBoundingClientRect = realRect;
    globalThis.ResizeObserver = realRO;
  };
}

test("clicking and dragging lines writes the range into the composer", async () => {
  stubShell(false);
  const restoreLayout = stubDesktopLayout();
  stubWorkspaceFileFetch(["one", "two", "three", "four", "five"]);
  const onChange = vi.fn();
  render(<RangeHarness initial="" onChange={onChange} />);

  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "@f.txt:", selectionStart: 7, selectionEnd: 7 },
  });
  await waitFor(() => {
    expect(screen.queryByTestId("at-range-picker")).toBeTruthy();
  });

  // Pressing a row starts the selection at that line.
  fireEvent.mouseDown(screen.getByTestId("at-range-line-3"));
  await waitFor(() => {
    expect(onChange).toHaveBeenCalledWith("@f.txt:3-3");
  });

  // Dragging over a later row extends it; the anchor stays put.
  fireEvent.mouseEnter(screen.getByTestId("at-range-line-5"));
  await waitFor(() => {
    expect(onChange).toHaveBeenCalledWith("@f.txt:3-5");
  });

  // Once the button is released, hovering no longer changes the range.
  fireEvent.mouseUp(window);
  onChange.mockClear();
  fireEvent.mouseEnter(screen.getByTestId("at-range-line-1"));
  expect(onChange).not.toHaveBeenCalled();

  restoreLayout();
  vi.unstubAllGlobals();
});

// Dragging upwards still yields a forward range.
test("a backwards drag normalizes the range", async () => {
  stubShell(false);
  const restoreLayout = stubDesktopLayout();
  stubWorkspaceFileFetch(["one", "two", "three", "four"]);
  const onChange = vi.fn();
  render(<RangeHarness initial="" onChange={onChange} />);

  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "@f.txt:", selectionStart: 7, selectionEnd: 7 },
  });
  await waitFor(() => {
    expect(screen.queryByTestId("at-range-picker")).toBeTruthy();
  });

  fireEvent.mouseDown(screen.getByTestId("at-range-line-4"));
  fireEvent.mouseEnter(screen.getByTestId("at-range-line-2"));
  await waitFor(() => {
    expect(onChange).toHaveBeenCalledWith("@f.txt:2-4");
  });

  restoreLayout();
  vi.unstubAllGlobals();
});

// The loaded preview is keyed by path only, so a session switch must discard it.
test("switching sessions closes the range picker and refetches on the next digit", async () => {
  stubShell(true);
  const fetchMock = stubWorkspaceFileFetch(["one", "two"]);
  function SessionHarness(props: { sessionId: string }) {
    const [value, setValue] = useState("");
    return (
      <Composer
        value={value}
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={setValue}
        onSend={() => {}}
        sessionId={props.sessionId}
      />
    );
  }
  const { rerender } = render(<SessionHarness sessionId="sess_a" />);

  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "@f.txt:", selectionStart: 7, selectionEnd: 7 },
  });
  await waitFor(() => {
    expect(screen.queryByTestId("at-range-picker")).toBeTruthy();
  });
  const before = fetchMock.mock.calls.length;

  rerender(<SessionHarness sessionId="sess_b" />);
  await waitFor(() => {
    expect(screen.queryByTestId("at-range-picker")).toBeNull();
  });

  fireEvent.change(ta, {
    target: { value: "@f.txt:1", selectionStart: 8, selectionEnd: 8 },
  });
  await waitFor(() => {
    expect(fetchMock.mock.calls.length).toBeGreaterThan(before);
  });
  const lastCall = fetchMock.mock.calls[fetchMock.mock.calls.length - 1];
  expect(String(lastCall?.[0]).startsWith("/coddy/workspace/file?")).toBe(true);
  await waitFor(() => {
    expect(screen.queryByTestId("at-range-picker")).toBeTruthy();
  });
  vi.unstubAllGlobals();
});

// --- "@" mention picker (GET /coddy/mentions) ---

type MentionStubRow = {
  kind: string;
  insert: string;
  label: string;
  detail?: string;
  continue?: boolean;
};

/** Answers GET /coddy/mentions from a map keyed by the query; records every URL. */
function stubMentionsFetch(
  answers: Record<string, { items: MentionStubRow[]; total?: number }>,
) {
  const urls: string[] = [];
  const fetchMock = vi.fn((input: string) => {
    urls.push(String(input));
    const u = new URL(String(input), "http://x");
    if (u.pathname === "/coddy/mentions") {
      const a = answers[u.searchParams.get("q") ?? ""] ?? { items: [] };
      return Promise.resolve({
        ok: true,
        json: async () => ({
          items: a.items,
          total: a.total ?? a.items.length,
        }),
      });
    }
    return Promise.resolve({ ok: false, status: 404, json: async () => ({}) });
  });
  vi.stubGlobal("fetch", fetchMock);
  return urls;
}

function MentionHarness(props: {
  onChange: (v: string) => void;
  generating?: boolean;
}) {
  const [value, setValue] = useState("");
  return (
    <Composer
      value={value}
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      generating={props.generating ?? false}
      onModeChange={() => {}}
      onChange={(v) => {
        setValue(v);
        props.onChange(v);
      }}
      onSend={() => {}}
    />
  );
}

function typeDraft(ta: HTMLElement, value: string) {
  fireEvent.change(ta, {
    target: { value, selectionStart: value.length, selectionEnd: value.length },
  });
}

test("the @ picker asks the server's search and names each candidate's kind", async () => {
  stubShell(true);
  const urls = stubMentionsFetch({
    app: {
      items: [
        {
          kind: "file",
          insert: "@external/cli/app.go",
          label: "external/cli/app.go",
          detail: "external/cli/",
        },
        {
          kind: "session",
          insert: "@session:sess_1",
          label: "App refactor",
          detail: "sess_1",
        },
      ],
      total: 7,
    },
  });
  render(<MentionHarness onChange={() => {}} />);
  typeDraft(screen.getByRole("textbox", { name: "Message" }), "@app");

  await waitFor(() => {
    expect(
      screen.getByTestId("mention-row-file-external_cli_app_go"),
    ).toBeTruthy();
  });
  const first = urls.find((u) => u.startsWith("/coddy/mentions?"));
  expect(first).toContain("q=app");
  // The picker just opened: the server rebuilds its workspace index.
  expect(first).toContain("refresh=1");
  expect(
    screen.getByTestId("mention-row-session-App_refactor"),
  ).toHaveTextContent("session");
  expect(screen.getByTestId("mention-more")).toHaveTextContent(
    "2 of 7, type to narrow",
  );
  vi.unstubAllGlobals();
});

test("arrow keys pick the row that enter inserts, with a space after it", async () => {
  stubShell(true);
  stubMentionsFetch({
    rea: {
      items: [
        { kind: "file", insert: "@README.md", label: "README.md" },
        {
          kind: "file",
          insert: "@internal/agent/react.go",
          label: "internal/agent/react.go",
        },
      ],
    },
  });
  const onChange = vi.fn();
  render(<MentionHarness onChange={onChange} generating={true} />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  typeDraft(ta, "see @rea");
  await waitFor(() => {
    expect(screen.getByTestId("mention-row-file-README_md")).toBeTruthy();
  });
  fireEvent.keyDown(ta, { key: "ArrowDown" });
  await waitFor(() => {
    expect(
      screen.getByTestId("mention-row-file-internal_agent_react_go"),
    ).toHaveAttribute("aria-selected", "true");
  });
  // The composer takes input while a turn runs, and so does the picker.
  fireEvent.keyDown(ta, { key: "Enter" });
  expect(onChange).toHaveBeenLastCalledWith("see @internal/agent/react.go ");
  vi.unstubAllGlobals();
});

test("the server's answer keeps the row the arrows moved to among the recent picks", async () => {
  stubShell(true);
  localStorage.clear();
  recordWorkspaceAtRecent(WORKSPACE_AT_RECENTS_NO_SESSION_KEY, {
    path_rel: "b.go",
    kind: "file",
  });
  recordWorkspaceAtRecent(WORKSPACE_AT_RECENTS_NO_SESSION_KEY, {
    path_rel: "a.go",
    kind: "file",
  });
  let answer: (v: unknown) => void = () => {};
  const pending = new Promise((resolve) => {
    answer = resolve;
  });
  vi.stubGlobal(
    "fetch",
    vi.fn((input: string) =>
      String(input).startsWith("/coddy/mentions?")
        ? pending.then(() => ({
            ok: true,
            json: async () => ({
              items: [
                {
                  kind: "scheme",
                  insert: "@session:",
                  label: "session:",
                  continue: true,
                },
              ],
              total: 1,
            }),
          }))
        : Promise.resolve({ ok: false, status: 404, json: async () => ({}) }),
    ),
  );
  render(<MentionHarness onChange={() => {}} />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  typeDraft(ta, "@");
  await waitFor(() => {
    expect(screen.getByTestId("mention-row-file-b_go")).toBeTruthy();
  });
  fireEvent.keyDown(ta, { key: "ArrowDown" });
  await waitFor(() => {
    expect(screen.getByTestId("mention-row-file-b_go")).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });
  answer(null);
  await waitFor(() => {
    expect(screen.getByTestId("mention-row-scheme-session_")).toBeTruthy();
  });
  expect(screen.getByTestId("mention-row-file-b_go")).toHaveAttribute(
    "aria-selected",
    "true",
  );
  localStorage.clear();
  vi.unstubAllGlobals();
});

test("the composer chips only the mentions the server says a send would attach", async () => {
  stubShell(true);
  const checks: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: string, init?: RequestInit) => {
      if (String(input) === "/coddy/mentions/check") {
        checks.push(String(init?.body ?? ""));
        return Promise.resolve({
          ok: true,
          json: async () => ({
            object: "coddy.mention_check",
            mentions: [
              { token: "@google/genai" },
              { token: "@README.md", typed: "@README.md", kind: "file" },
            ],
          }),
        });
      }
      return Promise.resolve({
        ok: true,
        json: async () => ({ items: [], total: 0 }),
      });
    }),
  );
  render(<MentionHarness onChange={() => {}} />);
  typeDraft(
    screen.getByRole("textbox", { name: "Message" }),
    "npm install @google/genai and read @README.md please",
  );
  // A package name is not a file: only the mention that resolves is a chip.
  await waitFor(() => {
    expect(
      screen.getAllByTestId("composer-at-chip").map((el) => el.textContent),
    ).toEqual(["@README.md"]);
  });
  expect(JSON.parse(checks[checks.length - 1] ?? "{}")).toEqual({
    text: "npm install @google/genai and read @README.md please",
  });
  vi.unstubAllGlobals();
});

test("a folder row keeps the picker open on what it holds", async () => {
  stubShell(true);
  const urls = stubMentionsFetch({
    src: {
      items: [
        { kind: "directory", insert: "@src/", label: "src/", continue: true },
      ],
    },
    "src/": {
      items: [{ kind: "file", insert: "@src/app.go", label: "src/app.go" }],
    },
  });
  const onChange = vi.fn();
  render(<MentionHarness onChange={onChange} />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  typeDraft(ta, "@src");
  await waitFor(() => {
    expect(screen.getByTestId("mention-row-directory-src_")).toBeTruthy();
  });
  fireEvent.keyDown(ta, { key: "Tab" });
  expect(onChange).toHaveBeenLastCalledWith("@src/");
  await waitFor(() => {
    expect(urls.some((u) => u.includes("q=src%2F"))).toBe(true);
  });
  vi.unstubAllGlobals();
});

test("the permission chip names the session's mode and switches it (#292)", () => {
  const picked: string[] = [];
  render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan", "ask"]}
      permissionMode="bypass"
      configuredPermissionMode="ask"
      onPermissionModeChange={(m) => picked.push(m)}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const chip = screen.getByTestId("composer-permission");
  expect(chip.textContent).toBe("Bypass");
  expect(chip.className).toContain("perm-bypass");
  expect(chip.getAttribute("title")).toContain("Ask first");
  fireEvent.click(chip);
  fireEvent.click(screen.getByRole("menuitem", { name: "Ask first" }));
  expect(picked).toEqual(["ask"]);
});

test("the selector chips run attach, mode, model, reasoning, permission", () => {
  render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan", "ask"]}
      llmModelMultimodal
      llmModels={["openai/gpt-5"]}
      llmModel="openai/gpt-5"
      onLlmModelChange={() => {}}
      llmReasoningLevels={["low", "medium", "high"]}
      llmReasoning="medium"
      onLlmReasoningChange={() => {}}
      permissionMode="ask"
      configuredPermissionMode="ask"
      onPermissionModeChange={() => {}}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const tabs = document.querySelector(".composer-tabs")!;
  const order = Array.from(tabs.querySelectorAll("button.composer-tab")).map(
    (b) => b.getAttribute("aria-label"),
  );
  expect(order).toEqual([
    "Attach file",
    "Mode",
    "Model",
    "Reasoning level",
    "Permissions",
  ]);
});

test("the permission chip is not shown without a handler", () => {
  renderComposer({ isEmpty: false });
  expect(screen.queryByTestId("composer-permission")).toBeNull();
});

test("the settings armed for the next turns are shown next to the selectors", () => {
  render(
    <Composer
      value=""
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      settingsOverrides={[
        {
          setting: "model",
          value: "nd/gpt-oss-120b",
          turnsLeft: 2,
          active: true,
        },
        { setting: "reasoning", value: "off", turnsLeft: 1 },
      ]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const chip = screen.getByTestId("composer-overrides");
  expect(chip.textContent).toBe("nd/gpt-oss-120b this turn and 2 more +1");
  expect(chip.getAttribute("title")).toContain("off, 1 turn left");
});

test("picking /plan in the / menu switches the mode instead of typing it", async () => {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: true,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
    onchange: null,
  }));
  const fetchMock = vi.fn((url: string) => {
    if (String(url).includes("/coddy/commands")) {
      return Promise.resolve({
        ok: true,
        json: async () => ({
          object: "coddy.commands",
          items: [
            {
              name: "plan",
              description: "Plan mode",
              kind: "setting",
              hint: "[--once|--count=N]",
            },
          ],
        }),
      });
    }
    return Promise.resolve({
      ok: true,
      json: async () => ({ items: [], has_more: false, page: 1 }),
    });
  });
  vi.stubGlobal("fetch", fetchMock);
  const modes: string[] = [];
  function Harness() {
    const [value, setValue] = useState("");
    return (
      <Composer
        value={value}
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan", "ask"]}
        onModeChange={(m) => modes.push(m)}
        onChange={setValue}
        onSend={() => {}}
      />
    );
  }
  render(<Harness />);
  const ta = screen.getByRole("textbox", {
    name: "Message",
  }) as HTMLTextAreaElement;
  fireEvent.change(ta, {
    target: { value: "/pl", selectionStart: 3, selectionEnd: 3 },
  });
  const row = await screen.findByTestId("command-row-plan");
  expect(row.textContent).toContain("[--once|--count=N]");
  fireEvent.mouseDown(row);
  expect(modes).toEqual(["plan"]);
  expect(ta.value).toBe("");
  vi.unstubAllGlobals();
});

// An input method confirming a candidate reports its keys too: isComposing,
// or in Safari a keyCode 229 keydown right after compositionend. They belong
// to it, so a picker takes no row, keeps its highlight and stays open, the way
// Enter then never sends.
function safariCommitKey(ta: HTMLElement, key: string) {
  fireEvent.compositionStart(ta);
  fireEvent.compositionEnd(ta);
  fireEvent.keyDown(ta, { key, keyCode: 229 });
}
test("keys an input method is composing with leave the slash picker alone", async () => {
  stubShell(true);
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        items: [
          { name: "rpa-gen-rules", description: "Generate project rules" },
          { name: "rpa-gen-docs", description: "Generate docs" },
        ],
        has_more: false,
        page: 1,
      }),
    }),
  );
  const onChange = vi.fn();
  const onSend = vi.fn();
  function Harness() {
    const [value, setValue] = useState("");
    return (
      <Composer
        value={value}
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={(v) => {
          setValue(v);
          onChange(v);
        }}
        onSend={onSend}
      />
    );
  }
  render(<Harness />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  typeDraft(ta, "/gen");
  await waitFor(() => {
    expect(
      screen.getByTestId("slash-command-row-rpa-gen-rules"),
    ).toHaveAttribute("aria-selected", "true");
  });
  onChange.mockClear();
  fireEvent.keyDown(ta, { key: "ArrowDown", isComposing: true });
  fireEvent.keyDown(ta, { key: "Enter", isComposing: true });
  safariCommitKey(ta, "Enter");
  fireEvent.keyDown(ta, { key: "Tab", isComposing: true });
  fireEvent.keyDown(ta, { key: "Escape", isComposing: true });

  expect(onChange).not.toHaveBeenCalled();
  expect(onSend).not.toHaveBeenCalled();
  expect(screen.getByTestId("slash-command-row-rpa-gen-rules")).toHaveAttribute(
    "aria-selected",
    "true",
  );
  vi.unstubAllGlobals();
});

test("keys an input method is composing with leave the @ picker alone", async () => {
  stubShell(true);
  stubMentionsFetch({
    rea: {
      items: [
        { kind: "file", insert: "@README.md", label: "README.md" },
        { kind: "file", insert: "@docs/README.md", label: "docs/README.md" },
      ],
    },
  });
  const onChange = vi.fn();
  render(<MentionHarness onChange={onChange} />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  typeDraft(ta, "@rea");
  await waitFor(() => {
    expect(screen.getByTestId("mention-row-file-README_md")).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });
  onChange.mockClear();
  safariCommitKey(ta, "ArrowDown");
  fireEvent.keyDown(ta, { key: "Enter", isComposing: true });
  safariCommitKey(ta, "Tab");
  fireEvent.keyDown(ta, { key: "Escape", isComposing: true });

  expect(onChange).not.toHaveBeenCalled();
  expect(screen.getByTestId("mention-row-file-README_md")).toHaveAttribute(
    "aria-selected",
    "true",
  );
  expect(screen.getByTestId("workspace-files-menu")).toBeTruthy();
  vi.unstubAllGlobals();
});

// Android keyboards report keyCode 229 for most keydowns with no composition
// behind them: without compositionend first, the key is the composer's.
test("a keyCode 229 with no composition behind it still takes the @ row", async () => {
  stubShell(true);
  stubMentionsFetch({
    rea: {
      items: [{ kind: "file", insert: "@README.md", label: "README.md" }],
    },
  });
  const onChange = vi.fn();
  render(<MentionHarness onChange={onChange} />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  typeDraft(ta, "@rea");
  await waitFor(() => {
    expect(screen.getByTestId("mention-row-file-README_md")).toBeTruthy();
  });
  fireEvent.keyDown(ta, { key: "Enter", keyCode: 229 });
  expect(onChange).toHaveBeenLastCalledWith("@README.md ");
  vi.unstubAllGlobals();
});

test("an Escape the input method is composing with leaves the line-range picker open", async () => {
  stubShell(true);
  stubWorkspaceFileFetch(["one", "two"]);
  render(<RangeHarness initial="" onChange={() => {}} />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "@f.txt:1-2", selectionStart: 10, selectionEnd: 10 },
  });
  await waitFor(() => {
    expect(screen.queryByTestId("at-range-picker")).toBeTruthy();
  });
  fireEvent.keyDown(ta, { key: "Escape", isComposing: true });
  safariCommitKey(ta, "Escape");
  expect(screen.queryByTestId("at-range-picker")).toBeTruthy();
  fireEvent.keyDown(ta, { key: "Escape" });
  await waitFor(() => {
    expect(screen.queryByTestId("at-range-picker")).toBeNull();
  });
  vi.unstubAllGlobals();
});

// A composition that ended with no keydown after it (a candidate tapped or
// clicked, a blur) leaves nothing behind: the next keyCode 229, which Android
// keyboards send for ordinary keys, is the composer's again.
test("a keyCode 229 long after a composition ended still takes the @ row", async () => {
  stubShell(true);
  stubMentionsFetch({
    rea: {
      items: [{ kind: "file", insert: "@README.md", label: "README.md" }],
    },
  });
  const onChange = vi.fn();
  render(<MentionHarness onChange={onChange} />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  typeDraft(ta, "@rea");
  await waitFor(() => {
    expect(screen.getByTestId("mention-row-file-README_md")).toBeTruthy();
  });
  const now = vi.spyOn(performance, "now");
  onChange.mockClear();
  // 99 ms after the end the 229 is still the key that ended it...
  now.mockReturnValue(1000);
  fireEvent.compositionStart(ta);
  fireEvent.compositionEnd(ta);
  now.mockReturnValue(1099);
  fireEvent.keyDown(ta, { key: "Enter", keyCode: 229 });
  expect(onChange).not.toHaveBeenCalled();
  // ...100 ms after, it is an ordinary key again.
  now.mockReturnValue(2000);
  fireEvent.compositionStart(ta);
  fireEvent.compositionEnd(ta);
  now.mockReturnValue(2100);
  fireEvent.keyDown(ta, { key: "Enter", keyCode: 229 });
  expect(onChange).toHaveBeenLastCalledWith("@README.md ");
  now.mockRestore();
  vi.unstubAllGlobals();
});

// The picker's FileList is live: clearing the input empties it. React runs a
// state update later whenever the app has other updates queued - as it does
// all through a running turn - so the files must be copied before the input is
// cleared, or an image picked during a turn silently goes missing.
test("files picked from the dialog survive the input being cleared before the update runs", () => {
  stubMatchMediaMobile(false);
  let updater: unknown = null;
  render(
    <Composer
      value=""
      isEmpty={true}
      mode="agent"
      modes={["agent"]}
      llmModelMultimodal={true}
      attachedFiles={[]}
      onAttachedFilesChange={(u) => {
        updater = u;
      }}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  const input = screen.getByTestId("composer-file-input") as HTMLInputElement;
  const file = new File(["image"], "shot.png", { type: "image/png" });
  const live: File[] = [file];
  Object.defineProperty(input, "files", {
    configurable: true,
    get: () => live,
  });
  Object.defineProperty(input, "value", {
    configurable: true,
    get: () => (live.length ? "C:\\fakepath\\shot.png" : ""),
    set: (v: string) => {
      if (v === "") live.length = 0;
    },
  });
  fireEvent.change(input);
  // The update runs only now, after the handler cleared the input.
  const next =
    typeof updater === "function"
      ? (updater as (p: File[]) => File[])([])
      : updater;
  expect(next).toEqual([file]);
  vi.unstubAllGlobals();
});

// A draft emptied from outside - a send, a queued prompt - fires no change
// event on the textarea, so the menu opened on that draft has to close by
// itself instead of hanging over the empty composer.
function stubCommandCatalog() {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: true,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
    onchange: null,
  }));
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) =>
      Promise.resolve({
        ok: true,
        json: async () =>
          String(url).includes("/coddy/commands")
            ? {
                object: "coddy.commands",
                items: [{ name: "compact", description: "Summarize history" }],
              }
            : { items: [], has_more: false, page: 1 },
      }),
    ),
  );
}

function SentDraftHarness(props: {
  generating?: boolean;
  onSend: (text: string) => void;
  onQueue?: (text: string) => void;
}) {
  const [value, setValue] = useState("");
  return (
    <Composer
      value={value}
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={setValue}
      generating={props.generating ?? false}
      onStop={() => {}}
      queueMode="after_turn"
      {...(props.onQueue
        ? {
            onQueue: (text: string) => {
              props.onQueue?.(text);
              setValue("");
            },
          }
        : {})}
      onSend={(text: string) => {
        props.onSend(text);
        setValue("");
      }}
    />
  );
}

async function openCommandMenu(ta: HTMLElement, draft: string) {
  fireEvent.change(ta, {
    target: {
      value: draft,
      selectionStart: draft.length,
      selectionEnd: draft.length,
    },
  });
  await waitFor(() => {
    expect(screen.getByTestId("command-row-compact")).toBeTruthy();
  });
}

test("Send clicked while the slash menu is open closes the menu", async () => {
  stubCommandCatalog();
  const onSend = vi.fn();
  render(<SentDraftHarness onSend={onSend} />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  await openCommandMenu(ta, "/compact");

  fireEvent.click(screen.getByRole("button", { name: "Send" }));

  expect(onSend).toHaveBeenCalledWith("/compact");
  expect((ta as HTMLTextAreaElement).value).toBe("");
  await waitFor(() => {
    expect(screen.queryByTestId("command-row-compact")).toBeNull();
  });
  vi.unstubAllGlobals();
});

test("a draft queued while the slash menu is open closes the menu", async () => {
  stubCommandCatalog();
  const onQueue = vi.fn();
  render(<SentDraftHarness generating onSend={() => {}} onQueue={onQueue} />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  await openCommandMenu(ta, "/compact");

  fireEvent.click(screen.getByRole("button", { name: /queue/i }));

  expect(onQueue).toHaveBeenCalledWith("/compact");
  await waitFor(() => {
    expect(screen.queryByTestId("command-row-compact")).toBeNull();
  });
  vi.unstubAllGlobals();
});

describe("cwd-scoped requests follow the chat workspace", () => {
  function slashHarness(props: { sessionId?: string; workspacePath?: string }) {
    function Harness() {
      const [value, setValue] = useState("");
      return (
        <Composer
          value={value}
          isEmpty={false}
          mode="agent"
          modes={["agent", "plan"]}
          onModeChange={() => {}}
          onChange={setValue}
          onSend={() => {}}
          {...props}
        />
      );
    }
    return Harness;
  }

  function stubFetch() {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ items: [], has_more: false, page: 1 }),
    });
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }

  const slashCall = (fetchMock: ReturnType<typeof vi.fn>) =>
    fetchMock.mock.calls.find((c: unknown[]) =>
      String(c[0]).includes("/coddy/slash-commands"),
    );

  test("a new chat lists the skills of the folder picked before the session exists", async () => {
    const fetchMock = stubFetch();
    const Harness = slashHarness({ workspacePath: "/projects/данные" });
    render(<Harness />);
    fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
      target: { value: "/rgs", selectionStart: 4, selectionEnd: 4 },
    });
    await waitFor(() => expect(slashCall(fetchMock)).toBeTruthy());
    const [url, init] = slashCall(fetchMock) as [string, RequestInit];
    expect(new URL(url, "http://x").searchParams.get("cwd")).toBe(
      "/projects/данные",
    );
    expect(new Headers(init?.headers).get("X-Coddy-Session-ID")).toBeNull();
    vi.unstubAllGlobals();
  });

  test("a session names its workspace by id, with its folder as the fallback", async () => {
    const fetchMock = stubFetch();
    const Harness = slashHarness({
      sessionId: "sess_1",
      workspacePath: "/projects/other",
    });
    render(<Harness />);
    fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
      target: { value: "/rgs", selectionStart: 4, selectionEnd: 4 },
    });
    await waitFor(() => expect(slashCall(fetchMock)).toBeTruthy());
    const [url, init] = slashCall(fetchMock) as [string, RequestInit];
    expect(new URL(url, "http://x").searchParams.get("cwd")).toBe(
      "/projects/other",
    );
    expect(new Headers(init?.headers).get("X-Coddy-Session-ID")).toBe("sess_1");
    vi.unstubAllGlobals();
  });

  test("a prefix that matched nothing in one folder is asked again in the next", async () => {
    const fetchMock = stubFetch();
    function Harness() {
      const [value, setValue] = useState("");
      const [path, setPath] = useState("/projects/other");
      return (
        <>
          <button type="button" onClick={() => setPath("/projects/data")}>
            pick data
          </button>
          <Composer
            value={value}
            isEmpty={false}
            mode="agent"
            modes={["agent", "plan"]}
            onModeChange={() => {}}
            onChange={setValue}
            onSend={() => {}}
            workspacePath={path}
          />
        </>
      );
    }
    render(<Harness />);
    const ta = screen.getByRole("textbox", { name: "Message" });
    fireEvent.change(ta, {
      target: { value: "/rgs", selectionStart: 4, selectionEnd: 4 },
    });
    const slashCwds = () =>
      fetchMock.mock.calls
        .filter((c: unknown[]) =>
          String(c[0]).includes("/coddy/slash-commands"),
        )
        .map((c: unknown[]) =>
          new URL(String(c[0]), "http://x").searchParams.get("cwd"),
        );
    await waitFor(() => expect(slashCwds()).toEqual(["/projects/other"]));
    fireEvent.click(screen.getByRole("button", { name: "pick data" }));
    fireEvent.change(ta, {
      target: { value: "/dat-", selectionStart: 5, selectionEnd: 5 },
    });
    await waitFor(() =>
      expect(slashCwds()).toEqual(["/projects/other", "/projects/data"]),
    );
    vi.unstubAllGlobals();
  });

  test("a slash answer for the folder left behind is not shown", async () => {
    // The bottom-sheet picker renders under jsdom (no layout for the anchor).
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: true,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
      onchange: null,
    }));
    let releaseOld: () => void = () => {};
    const fetchMock = vi.fn((input: string) => {
      const url = new URL(String(input), "http://x");
      if (url.pathname !== "/coddy/slash-commands") {
        return Promise.resolve({ ok: true, json: async () => ({ items: [] }) });
      }
      const body = {
        items:
          url.searchParams.get("cwd") === "/projects/data"
            ? [{ name: "dat-report", description: "data skill" }]
            : [],
        has_more: false,
        page: 1,
      };
      const answer = { ok: true, json: async () => body };
      // Both answers are held: the one for data is released while the menu
      // already waits for the folder picked after it.
      return new Promise((resolve) => {
        if (url.searchParams.get("cwd") === "/projects/data") {
          releaseOld = () => resolve(answer);
        }
      });
    });
    vi.stubGlobal("fetch", fetchMock);
    function Harness() {
      const [value, setValue] = useState("");
      const [path, setPath] = useState("/projects/data");
      return (
        <>
          <button type="button" onClick={() => setPath("/projects/other")}>
            pick other
          </button>
          <Composer
            value={value}
            isEmpty={false}
            mode="agent"
            modes={["agent", "plan"]}
            onModeChange={() => {}}
            onChange={setValue}
            onSend={() => {}}
            workspacePath={path}
          />
        </>
      );
    }
    render(<Harness />);
    fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
      target: { value: "/rgs", selectionStart: 4, selectionEnd: 4 },
    });
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some((c: unknown[]) =>
          String(c[0]).includes("cwd=%2Fprojects%2Fdata"),
        ),
      ).toBe(true),
    );
    fireEvent.click(screen.getByRole("button", { name: "pick other" }));
    releaseOld();
    await new Promise((resolve) => setTimeout(resolve, 30));
    fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
      target: { value: "/dat-", selectionStart: 5, selectionEnd: 5 },
    });
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some((c: unknown[]) =>
          String(c[0]).includes("cwd=%2Fprojects%2Fother"),
        ),
      ).toBe(true),
    );
    // The answer for data came after data was left: the menu that opens next,
    // waiting for the other folder, never lists what data holds.
    expect(screen.queryByText("/dat-report")).toBeNull();
    vi.unstubAllGlobals();
  });

  test("an @ mention of a new chat searches the picked folder", async () => {
    const fetchMock = stubFetch();
    const Harness = slashHarness({ workspacePath: "/projects/data" });
    render(<Harness />);
    fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
      target: { value: "@READ", selectionStart: 5, selectionEnd: 5 },
    });
    const mentionCall = () =>
      fetchMock.mock.calls.find((c: unknown[]) =>
        String(c[0]).startsWith("/coddy/mentions?"),
      );
    await waitFor(() => expect(mentionCall()).toBeTruthy());
    const [url] = mentionCall() as [string];
    expect(new URL(url, "http://x").searchParams.get("cwd")).toBe(
      "/projects/data",
    );
    vi.unstubAllGlobals();
  });
});

// The mirror under the masked textarea draws the draft; the caret is the
// textarea's. jsdom has no layout, so these pin the wiring that keeps the two
// together, and npm run check:caret measures the pixels in a real engine.
describe("the mirror follows the textarea", () => {
  function renderDraft() {
    const view = render(
      <Composer
        value={"first line\nsecond line"}
        isEmpty={false}
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={() => {}}
        onSend={() => {}}
      />,
    );
    const ta = view.container.querySelector("#composer") as HTMLTextAreaElement;
    const mirror = view.container.querySelector(
      ".composer-mirror",
    ) as HTMLDivElement;
    const inner = view.container.querySelector(
      ".composer-mirror-inner",
    ) as HTMLDivElement;
    return { ta, mirror, inner };
  }

  test("the mirror keeps the stylesheet's padding and takes the textarea's height", () => {
    const { ta, mirror, inner } = renderDraft();
    // The right padding is the stylesheet's, the same as the textarea's; a
    // width measured in script (the old 16px plus the scrollbar) left the
    // mirror 28px wider than the field it draws for.
    expect(inner.style.paddingRight).toBe("");
    expect(inner.style.transform).toBe("");
    // The stack under the inline textarea is a few pixels taller than it, so
    // the mirror is cut to the field's own height, fraction included.
    ta.style.height = "131.5px";
    fireEvent(window, new Event("resize"));
    expect(mirror.style.height).toBe("131.5px");
  });

  test("the mirror scrolls with the textarea", () => {
    const { ta, mirror } = renderDraft();
    ta.scrollTop = 42;
    fireEvent.scroll(ta);
    expect(mirror.scrollTop).toBe(42);
  });

  test("a page zoom or another screen density lines the mirror up again", () => {
    const listeners = new Map<string, () => void>();
    const matchMedia = vi.mocked(window.matchMedia);
    const setupImplementation = matchMedia.getMockImplementation();
    matchMedia.mockImplementation(
      (query: string) =>
        ({
          matches: true,
          media: query,
          onchange: null,
          addListener: vi.fn(),
          removeListener: vi.fn(),
          addEventListener: (_: string, fn: () => void) =>
            listeners.set(query, fn),
          removeEventListener: () => listeners.delete(query),
          dispatchEvent: vi.fn(),
        }) as unknown as MediaQueryList,
    );
    try {
      const { ta, mirror } = renderDraft();
      const density = [...listeners.keys()].find((q) =>
        q.startsWith("(resolution:"),
      );
      expect(density).toBe(`(resolution: ${window.devicePixelRatio}dppx)`);
      // The window moved to another screen: the field scrolled while
      // laying its text out again, and no scroll event says so.
      ta.scrollTop = 17;
      listeners.get(density!)!();
      expect(mirror.scrollTop).toBe(17);
      // It goes on listening on the new density.
      expect(listeners.has(density!)).toBe(true);
      // A page zoom resizes the window.
      ta.scrollTop = 23;
      fireEvent(window, new Event("resize"));
      expect(mirror.scrollTop).toBe(23);
    } finally {
      if (setupImplementation) {
        matchMedia.mockImplementation(setupImplementation);
      }
    }
  });
});

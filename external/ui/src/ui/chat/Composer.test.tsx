import React, { useState } from "react";
import { afterEach, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, test } from "vitest";
import { Composer } from "./Composer";

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
      items: [{ name: "generate-rules", description: "Generate rules" }],
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
        onChange={(v) => { setValue(v); onChange(v); }}
        onSend={() => {}}
      />
    );
  }

  render(<Harness />);
  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, { target: { value: "/gen", selectionStart: 4, selectionEnd: 4 } });

  await waitFor(() => {
    expect(screen.queryByRole("listbox", { name: "Slash commands" })).toBeTruthy();
  });

  fireEvent.keyDown(ta, { key: "Tab", code: "Tab" });

  await waitFor(() => {
    expect(onChange).toHaveBeenCalledWith("/generate-rules ");
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
  expect(screen.getByText("Commands")).toBeTruthy();
  expect(screen.getByTestId("slash-command-row-some-skill")).toBeTruthy();

  vi.unstubAllGlobals();
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
  expect(b.querySelector(".composer-send-glyph .composer-stop-square")).toBeTruthy();
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
      tokenUsage={{ inputTokens: 800000, outputTokens: 20000, totalTokens: 820000 }}
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
  const fg = document.querySelector(".context-ring-fg") as SVGCircleElement | null;
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

test("enhance button shares the composer context row with workspace controls", () => {
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
  expect(button.closest(".composer-context-row")).not.toBeNull();
  expect(button.closest(".composer-field-wrap")).toBeNull();
  expect(button.closest(".composer-bar")).toBeNull();
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
  fireEvent.keyDown(screen.getByRole("textbox", { name: "Message" }), {
    key: "z",
    ctrlKey: true,
  });
  expect(onChange).toHaveBeenLastCalledWith("fix memory thing");
  vi.unstubAllGlobals();
});

test("desktop: Ctrl+Enter calls onSend", () => {
  stubMatchMediaMobile(false);
  const onSend = vi.fn();
  render(
    <Composer
      value="hello"
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={onSend}
    />,
  );
  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.keyDown(ta, { key: "Enter", ctrlKey: true });
  expect(onSend).toHaveBeenCalledTimes(1);
  expect(onSend).toHaveBeenCalledWith("hello");
  vi.unstubAllGlobals();
});

test("desktop: Shift+Enter does not call onSend", () => {
  stubMatchMediaMobile(false);
  const onSend = vi.fn();
  render(
    <Composer
      value="hello"
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={onSend}
    />,
  );
  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.keyDown(ta, { key: "Enter", shiftKey: true });
  expect(onSend).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
});

test("mobile: Enter does not call onSend (newline only)", () => {
  stubMatchMediaMobile(true);
  const onSend = vi.fn();
  render(
    <Composer
      value="hello"
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={onSend}
    />,
  );
  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.keyDown(ta, { key: "Enter" });
  expect(onSend).not.toHaveBeenCalled();
  vi.unstubAllGlobals();
});

test("mobile: clicking Send button calls onSend", () => {
  stubMatchMediaMobile(true);
  const onSend = vi.fn();
  render(
    <Composer
      value="hello"
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={onSend}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(onSend).toHaveBeenCalledWith("hello");
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
  const fileInput = screen.getByTestId("composer-file-input") as HTMLInputElement;
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
  const fileInput = screen.getByTestId("composer-file-input") as HTMLInputElement;
  const file = new File(["data"], "img.png", { type: "image/png" });
  fireEvent.change(fileInput, { target: { files: [file] } });
  await waitFor(() => screen.getByText("img.png"));
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(onSend).toHaveBeenCalledWith("describe this", [file]);
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
  pasteWithImages(ta, [new File(["img2"], "image.png", { type: "image/jpeg" })]);
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
    createObjectURL?: (f: File) => string;
    revokeObjectURL?: (u: string) => void;
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
  const sendBtn = screen.getByRole("button", { name: "Send" }) as HTMLButtonElement;
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
  const chip = screen.getByText("photo.png").closest(
    ".composer-attachment-chip",
  );
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
    screen.getByTestId("slash-command-row-review").getAttribute("aria-selected"),
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

function RangeHarness(props: { initial: string; onChange: (v: string) => void }) {
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
    vi.fn(() => Promise.resolve({ ok: false, status: 404, json: async () => ({}) })),
  );
  render(<RangeHarness initial="" onChange={() => {}} />);

  const ta = screen.getByRole("textbox", { name: "Message" });
  fireEvent.change(ta, {
    target: { value: "@nope.txt:1-2", selectionStart: 13, selectionEnd: 13 },
  });

  await waitFor(() => {
    expect((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.length).toBeGreaterThan(0);
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

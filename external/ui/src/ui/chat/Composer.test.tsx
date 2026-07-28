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

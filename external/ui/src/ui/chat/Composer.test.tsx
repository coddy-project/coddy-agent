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
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  fireEvent.change(ta, {
    target: {
      value: "/adfadsfgaf",
      selectionStart: "/adfadsfgaf".length,
      selectionEnd: "/adfadsfgaf".length,
    },
  });
  await new Promise((r) => setTimeout(r, 250));
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("listbox", { name: "Slash commands" })).toBeNull();
  expect(screen.queryByTestId("composer-skill-chip")).toBeNull();

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
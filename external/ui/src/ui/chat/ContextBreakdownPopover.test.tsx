import React from "react";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import {
  ContextBreakdownPopover,
  formatContextTokens,
} from "./ContextBreakdownPopover";
import { OpenRailScreen } from "../nav/railEscape.fakes";

const breakdown = {
  systemPrompt: 100,
  toolDefinitions: 50,
  rules: 0,
  skills: 0,
  mcp: 0,
  subagents: 0,
  conversation: 350,
  estimatedTotal: 500,
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test("the foot names the threshold in percent and tokens, and its button compacts and holds progress", async () => {
  let finish: ((response: Response) => void) | undefined;
  const pending = new Promise<Response>((resolve) => {
    finish = resolve;
  });
  const fetchMock = vi.fn(() => pending);
  vi.stubGlobal("fetch", fetchMock);
  const onCompacted = vi.fn();
  render(
    <ContextBreakdownPopover
      open
      onClose={() => {}}
      maxContextTokens={1000}
      sessionId="sess_123"
      compactAvailable
      compactAutoEnabled
      compactThreshold={95}
      onCompacted={onCompacted}
    />,
  );

  expect(screen.getByTestId("context-breakdown-compaction")).toHaveTextContent(
    "Auto-compact at 95% · 950",
  );
  const action = screen.getByTestId("context-breakdown-compact");
  expect(action).toHaveTextContent("Compact now");
  fireEvent.click(action);
  expect(action).toBeDisabled();
  expect(action).toHaveTextContent("Compacting");
  expect(fetchMock).toHaveBeenCalledWith(
    "/coddy/sessions/sess_123/compact",
    expect.objectContaining({
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Coddy-Session-ID": "sess_123",
      },
      body: "{}",
    }),
  );
  finish!(new Response("{}", { status: 200 }));
  await waitFor(() => expect(onCompacted).toHaveBeenCalledOnce());
  expect(action).not.toBeDisabled();
});

test("the foot says automatic compaction is off, and the button still compacts", () => {
  render(
    <ContextBreakdownPopover
      open
      onClose={() => {}}
      maxContextTokens={1000}
      breakdown={breakdown}
      sessionId="sess_123"
      compactAvailable
      compactAutoEnabled={false}
    />,
  );
  expect(screen.getByTestId("context-breakdown-compaction")).toHaveTextContent(
    "Auto-compact is off",
  );
  expect(screen.getByTestId("context-breakdown-compact")).toHaveTextContent(
    "Compact now",
  );
  expect(screen.getByTestId("context-breakdown-compact")).toBeEnabled();
});

test("idle sheet keeps close and disabled compact controls visible", () => {
  render(
    <ContextBreakdownPopover
      open
      onClose={() => {}}
      useSheet
      maxContextTokens={128000}
      compactThreshold={80}
    />,
  );
  expect(screen.getByTestId("context-breakdown-close")).toBeVisible();
  const compact = screen.getByTestId("context-breakdown-compact");
  expect(compact).toHaveTextContent("Compact now");
  expect(screen.getByTestId("context-breakdown-compaction")).toHaveTextContent(
    "Auto-compact at 80%",
  );
  expect(compact).toBeDisabled();
});

test("context popover uses modal head chrome, the percent and the tokens over the bar and compaction at its foot", () => {
  render(
    <ContextBreakdownPopover
      open
      onClose={() => {}}
      maxContextTokens={1_000_000}
      breakdown={{ ...breakdown, estimatedTotal: 540_612 }}
      sessionId="sess_123"
      compactAvailable
      compactAutoEnabled
      compactThreshold={80}
    />,
  );

  expect(screen.getByTestId("context-breakdown-head")).toHaveClass(
    "sessions-head",
  );
  expect(screen.getByTestId("context-breakdown-close")).toHaveClass(
    "sessions-close",
  );
  // The row stands right over the bar: the percent, then the tokens at its end.
  const usageRow = screen.getByTestId("context-breakdown-usage-row");
  expect(usageRow.children[0]).toHaveTextContent("54% used");
  expect(usageRow.children[1]).toHaveTextContent("540.6k / 1M");
  expect(usageRow.children[1]).toHaveAttribute(
    "title",
    "~540,612 / 1,000,000 Tokens",
  );
  expect(usageRow.nextElementSibling).toBe(
    screen.getByTestId("context-breakdown-bar"),
  );
  expect(usageRow).not.toContainElement(
    screen.getByTestId("context-breakdown-compact"),
  );
  // Under the list: the threshold and what it comes to, then the button.
  const foot = screen.getByTestId("context-breakdown-compaction");
  expect(foot.children[0]).toHaveTextContent("Auto-compact at 80% · 800k");
  expect(foot.children[1]).toBe(
    screen.getByTestId("context-breakdown-compact"),
  );
  const legend = screen.getByTestId(
    "context-breakdown-row-conversation",
  ).parentElement!;
  expect(legend.nextElementSibling).toBe(foot);
});

test("the tokens over the bar shorten without ever rounding up", () => {
  expect(formatContextTokens(0)).toBe("0");
  expect(formatContextTokens(999)).toBe("999");
  expect(formatContextTokens(1000)).toBe("1k");
  expect(formatContextTokens(45_898)).toBe("45.8k");
  expect(formatContextTokens(131_072)).toBe("131k");
  expect(formatContextTokens(540_612)).toBe("540.6k");
  expect(formatContextTokens(999_999)).toBe("999.9k");
  expect(formatContextTokens(1_000_000)).toBe("1M");
  expect(formatContextTokens(1_250_000)).toBe("1.2M");
});

test("a sliver of use reads under one percent, never zero", () => {
  render(
    <ContextBreakdownPopover
      open
      onClose={() => {}}
      maxContextTokens={1_000_000}
      breakdown={{ ...breakdown, estimatedTotal: 500 }}
    />,
  );
  expect(
    screen.getByTestId("context-breakdown-usage-row").children[0],
  ).toHaveTextContent("<1% used");
});

test("context action says nothing to compact when the endpoint folds nothing", async () => {
  const fetchMock = vi.fn(async () =>
    Promise.resolve(
      new Response(
        JSON.stringify({ compacted: false, reason: "nothing_to_compact" }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      ),
    ),
  );
  vi.stubGlobal("fetch", fetchMock);
  const onCompacted = vi.fn();
  render(
    <ContextBreakdownPopover
      open
      onClose={() => {}}
      maxContextTokens={1000}
      sessionId="sess_123"
      compactAvailable
      compactAutoEnabled
      onCompacted={onCompacted}
    />,
  );

  fireEvent.click(screen.getByTestId("context-breakdown-compact"));
  await waitFor(() =>
    expect(screen.getByText(/nothing to compact/i)).toBeInTheDocument(),
  );
  expect(onCompacted).not.toHaveBeenCalled();
});

// History open beside the chat, the breakdown opened over the composer after
// it: Escape takes the breakdown down first, and History stays for the next one.
test("Escape closes the breakdown before the drawer open beside the chat", () => {
  const onClose = vi.fn();
  const closeHistory = vi.fn();
  render(
    <>
      <OpenRailScreen id="history" onClose={closeHistory} />
      <ContextBreakdownPopover
        open
        onClose={onClose}
        maxContextTokens={128000}
      />
    </>,
  );
  fireEvent.keyDown(document.body, { key: "Escape" });
  expect(onClose).toHaveBeenCalledTimes(1);
  expect(closeHistory).not.toHaveBeenCalled();
});

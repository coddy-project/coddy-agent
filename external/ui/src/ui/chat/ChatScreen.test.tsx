import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { ChatScreen } from "./ChatScreen";
import type { TranscriptItem } from "./types";

afterEach(() => cleanup());

test("empty hero shows headline with accent span", () => {
  const { getByTestId, getByRole } = render(
    <ChatScreen
      title=""
      sessionId=""
      heroAccentVerb="know"
      heroComposerFocusEpoch={0}
      onTitleSave={() => {}}
      items={[]}
      draft=""
      tokenUsage={null}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onDraftChange={() => {}}
      onSend={() => {}}
    />,
  );

  expect(getByRole("heading", { level: 1 })).toHaveTextContent(
    "What do you want to know?",
  );
  expect(getByTestId("hero-title-accent")).toHaveTextContent("know");
  expect(getByRole("textbox")).toHaveFocus();
});

test("active chat wraps title in chat-title-column aligned with composer column", () => {
  const { container } = render(
    <ChatScreen
      title="Hi"
      sessionId="s1"
      heroAccentVerb="know"
      heroComposerFocusEpoch={0}
      onTitleSave={() => {}}
      items={[{ type: "user_message", id: "1", content: "x" }]}
      draft=""
      tokenUsage={null}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onDraftChange={() => {}}
      onSend={() => {}}
    />,
  );

  const col = container.querySelector(".chat-title-column");
  expect(col).toBeTruthy();
  expect(col?.querySelector(".chat-header")).toBeTruthy();
});

test("disabled attachments survive the empty-to-active composer transition", async () => {
  const common = {
    title: "",
    sessionId: "",
    heroAccentVerb: "know" as const,
    heroComposerFocusEpoch: 0,
    onTitleSave: () => {},
    draft: "",
    tokenUsage: null,
    mode: "agent",
    modes: ["agent", "plan"],
    llmModels: ["openai/vision", "openai/text"],
    llmModel: "openai/vision",
    onLlmModelChange: () => {},
    onModeChange: () => {},
    onDraftChange: () => {},
    onSend: () => {},
  };
  const { rerender } = render(
    <ChatScreen {...common} items={[]} llmModelMultimodal={true} />,
  );
  fireEvent.change(screen.getByTestId("composer-file-input"), {
    target: {
      files: [new File(["img"], "photo.png", { type: "image/png" })],
    },
  });
  await waitFor(() => screen.getByText("photo.png"));

  rerender(
    <ChatScreen
      {...common}
      sessionId="s1"
      items={[{ type: "user_message", id: "1", content: "hello" }]}
      llmModel="openai/text"
      llmModelMultimodal={false}
    />,
  );

  expect(
    screen.getByText("photo.png").closest(".composer-attachment-chip"),
  ).toHaveClass("composer-attachment-chip--disabled");
});

const childTranscript = {
  parentSessionId: "s_parent",
  name: "explore",
  taskId: "bg_3",
};

test("a subagent transcript replaces the docked composer with a read-only notice", () => {
  const onOpenSession = vi.fn();
  const { container } = render(
    <ChatScreen
      title="agent explore"
      sessionId="sess_0a1b2c"
      heroAccentVerb="know"
      heroComposerFocusEpoch={0}
      onTitleSave={() => {}}
      items={[{ type: "user_message", id: "1", content: "survey the repo" }]}
      draft=""
      tokenUsage={null}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onDraftChange={() => {}}
      onSend={() => {}}
      subagentTranscript={childTranscript}
      onOpenSession={onOpenSession}
    />,
  );

  expect(container.querySelector(".composer-card")).toBeNull();
  expect(screen.getByTestId("subagent-readonly-notice")).toHaveTextContent(
    "Read-only transcript of subagent explore",
  );
  fireEvent.click(screen.getByTestId("subagent-readonly-parent-link"));
  expect(onOpenSession).toHaveBeenCalledWith("s_parent");
});

test("the notice also takes the hero composer's slot on an empty child transcript", () => {
  const { container } = render(
    <ChatScreen
      title=""
      sessionId="sess_0a1b2c"
      heroAccentVerb="know"
      heroComposerFocusEpoch={0}
      onTitleSave={() => {}}
      items={[]}
      draft=""
      tokenUsage={null}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onDraftChange={() => {}}
      onSend={() => {}}
      subagentTranscript={childTranscript}
    />,
  );

  expect(container.querySelector(".composer-card")).toBeNull();
  expect(screen.getByTestId("subagent-readonly-notice")).toBeInTheDocument();
});

const scrollBase = {
  title: "Long chat",
  sessionId: "s1",
  heroAccentVerb: "know" as const,
  heroComposerFocusEpoch: 0,
  onTitleSave: () => {},
  draft: "",
  tokenUsage: null,
  mode: "agent",
  modes: ["agent", "plan"],
  onModeChange: () => {},
  onDraftChange: () => {},
  onSend: () => {},
};

const firstTurn: TranscriptItem[] = [
  { type: "user_message", id: "u1", content: "explain the layout" },
  { type: "assistant_message", id: "a1", content: "a very long answer" },
];

/** jsdom lays nothing out, so the scroll viewport is given real metrics by hand. */
function transcriptViewport(
  container: HTMLElement,
  metrics: { scrollHeight: number; clientHeight: number },
): HTMLElement {
  const el = container.querySelector("#messages") as HTMLElement;
  expect(el).toBeTruthy();
  Object.defineProperty(el, "scrollHeight", {
    value: metrics.scrollHeight,
    configurable: true,
  });
  Object.defineProperty(el, "clientHeight", {
    value: metrics.clientHeight,
    configurable: true,
  });
  return el;
}

/** The button never unmounts, so "shown" is a state on it, not its presence. */
function scrollButtonShown(): boolean {
  return (
    screen.getByTestId("chat-scroll-bottom").getAttribute("data-visible") ===
    "true"
  );
}

test("scrolling up reveals the scroll-to-bottom button, which returns the transcript to the newest message", async () => {
  const { container } = render(
    <ChatScreen {...scrollBase} items={firstTurn} />,
  );
  const viewport = transcriptViewport(container, {
    scrollHeight: 1200,
    clientHeight: 400,
  });

  // Parked at the newest message: nothing to jump back to.
  viewport.scrollTop = 800;
  fireEvent.scroll(viewport);
  expect(scrollButtonShown()).toBe(false);

  viewport.scrollTop = 200;
  fireEvent.scroll(viewport);
  await waitFor(() => expect(scrollButtonShown()).toBe(true));

  const button = screen.getByTestId("chat-scroll-bottom");
  // It rides with the composer column, above the input area.
  expect(button.closest(".chat-bottom-inner")).toBeTruthy();
  expect(button).toHaveAccessibleName("Scroll to the latest message");

  const frames = fakeFrameClock();
  try {
    fireEvent.click(button);
    // 600px of travel is the shortest budget, 220ms. `1200 - 400` of viewport
    // is the last reachable scrollTop.
    frames.advance(220);
    expect(viewport.scrollTop).toBe(800);
    expect(scrollButtonShown()).toBe(false);
  } finally {
    frames.restore();
  }
});

/**
 * jsdom hands out animation frames in a late burst, so the jump gets a clock of
 * its own here: frames run when the test says so, on the same reading of
 * `performance.now()` the animation started from.
 */
function fakeFrameClock() {
  let now = 0;
  let nextHandle = 0;
  const pending = new Map<number, FrameRequestCallback>();
  const raf = vi
    .spyOn(window, "requestAnimationFrame")
    .mockImplementation((cb) => {
      nextHandle += 1;
      pending.set(nextHandle, cb);
      return nextHandle;
    });
  const caf = vi
    .spyOn(window, "cancelAnimationFrame")
    .mockImplementation((handle) => {
      pending.delete(handle);
    });
  const clock = vi.spyOn(performance, "now").mockImplementation(() => now);
  return {
    advance(ms: number) {
      now += ms;
      const due = [...pending.values()];
      pending.clear();
      act(() => {
        for (const cb of due) cb(now);
      });
    },
    pendingFrames: () => pending.size,
    restore() {
      raf.mockRestore();
      caf.mockRestore();
      clock.mockRestore();
    },
  };
}

test("the jump travels over several frames and settles on the newest message", async () => {
  const { container } = render(
    <ChatScreen {...scrollBase} items={firstTurn} />,
  );
  const viewport = transcriptViewport(container, {
    scrollHeight: 4000,
    clientHeight: 400,
  });
  viewport.scrollTop = 0;
  fireEvent.scroll(viewport);
  await waitFor(() => expect(scrollButtonShown()).toBe(true));

  const frames = fakeFrameClock();
  try {
    fireEvent.click(screen.getByTestId("chat-scroll-bottom"));
    // 3600px of travel takes the full 460ms budget. Halfway through the time
    // the ease-out has already covered seven eighths of the distance, and the
    // rest of it is the settle.
    frames.advance(230);
    expect(viewport.scrollTop).toBe(3150);
    expect(scrollButtonShown()).toBe(false);

    frames.advance(230);
    expect(viewport.scrollTop).toBe(3600);
    expect(frames.pendingFrames()).toBe(0);
    expect(scrollButtonShown()).toBe(false);
  } finally {
    frames.restore();
  }
});

test("the reader reaching for the wheel cancels a jump still in the air", async () => {
  const { container } = render(
    <ChatScreen {...scrollBase} items={firstTurn} />,
  );
  const viewport = transcriptViewport(container, {
    scrollHeight: 8000,
    clientHeight: 400,
  });
  viewport.scrollTop = 0;
  fireEvent.scroll(viewport);
  await waitFor(() => expect(scrollButtonShown()).toBe(true));

  const frames = fakeFrameClock();
  try {
    fireEvent.click(screen.getByTestId("chat-scroll-bottom"));
    frames.advance(115);
    const stoppedAt = viewport.scrollTop;
    expect(stoppedAt).toBeGreaterThan(0);
    expect(stoppedAt).toBeLessThan(7600);

    fireEvent.wheel(window);
    expect(frames.pendingFrames()).toBe(0);
    // The travel is over where the reader stopped it, and the button is back
    // because the transcript is parked short of the end.
    expect(viewport.scrollTop).toBe(stoppedAt);
    expect(scrollButtonShown()).toBe(true);

    frames.advance(460);
    expect(viewport.scrollTop).toBe(stoppedAt);
  } finally {
    frames.restore();
  }
});

test("output arriving while the reader is scrolled up keeps the button and the reading position", async () => {
  const { container, rerender } = render(
    <ChatScreen {...scrollBase} items={firstTurn} />,
  );
  const viewport = transcriptViewport(container, {
    scrollHeight: 1200,
    clientHeight: 400,
  });
  viewport.scrollTop = 200;
  fireEvent.scroll(viewport);
  await waitFor(() => expect(scrollButtonShown()).toBe(true));

  Object.defineProperty(viewport, "scrollHeight", {
    value: 2000,
    configurable: true,
  });
  rerender(
    <ChatScreen
      {...scrollBase}
      items={[
        ...firstTurn,
        { type: "assistant_message", id: "a2", content: "more output" },
      ]}
    />,
  );

  expect(viewport.scrollTop).toBe(200);
  expect(scrollButtonShown()).toBe(true);
});

test("a transcript still following the newest output shows no button", async () => {
  const { container, rerender } = render(
    <ChatScreen {...scrollBase} items={firstTurn} />,
  );
  const viewport = transcriptViewport(container, {
    scrollHeight: 1200,
    clientHeight: 400,
  });
  viewport.scrollTop = 800;
  fireEvent.scroll(viewport);

  Object.defineProperty(viewport, "scrollHeight", {
    value: 2000,
    configurable: true,
  });
  rerender(
    <ChatScreen
      {...scrollBase}
      items={[
        ...firstTurn,
        { type: "assistant_message", id: "a2", content: "more output" },
      ]}
    />,
  );

  expect(viewport.scrollTop).toBe(1600);
  await waitFor(() => expect(scrollButtonShown()).toBe(false));
});

test("the empty hero has no scroll-to-bottom button", () => {
  render(<ChatScreen {...scrollBase} sessionId="" title="" items={[]} />);
  expect(screen.queryByTestId("chat-scroll-bottom")).toBeNull();
});

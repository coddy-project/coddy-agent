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
import type { BackgroundTask } from "../tasks/types";
import type { TranscriptItem } from "./types";

afterEach(() => cleanup());

test("new background permission prompts follow the reader at the bottom, but polling does not", () => {
  const common = {
    title: "Audit",
    sessionId: "sess_parent",
    heroAccentVerb: "know" as const,
    heroComposerFocusEpoch: 0,
    onTitleSave: () => {},
    items: [{ type: "user_message" as const, id: "u1", content: "audit" }],
    draft: "",
    tokenUsage: null,
    mode: "agent",
    modes: ["agent"],
    onModeChange: () => {},
    onDraftChange: () => {},
    onSend: () => {},
  };
  const task: BackgroundTask = {
    id: "bg_1",
    session_id: "sess_parent",
    kind: "agent",
    label: "writer",
    status: "running",
    started_at: "2026-09-14T10:00:00Z",
    timeout_seconds: 900,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 1,
    overdue: false,
    running: true,
    agent: { name: "writer", session_id: "sess_child" },
    pending_permission: {
      sessionId: "sess_child",
      toolCall: { toolCallId: "call_1", title: "Run: run_command" },
      options: [],
    },
  };
  const { container, rerender } = render(
    <ChatScreen {...common} backgroundTasks={[]} />,
  );
  const scroller = container.querySelector("#messages") as HTMLElement;
  Object.defineProperties(scroller, {
    scrollHeight: { configurable: true, value: 2000 },
    clientHeight: { configurable: true, value: 500 },
  });
  scroller.scrollTop = 1500;
  fireEvent.scroll(scroller);
  Object.defineProperty(scroller, "scrollHeight", {
    configurable: true,
    value: 2300,
  });
  rerender(<ChatScreen {...common} backgroundTasks={[task]} />);
  // The last reachable position: scrollHeight less the viewport.
  expect(scroller.scrollTop).toBe(1800);
  // A freshly fetched row with the same prompt must not move the viewport.
  scroller.scrollTop = 1000;
  rerender(
    <ChatScreen
      {...common}
      backgroundTasks={[{ ...task, elapsed_seconds: 2 }]}
    />,
  );
  expect(scroller.scrollTop).toBe(1000);
  // A reader inspecting older messages keeps their position on a new prompt.
  scroller.scrollTop = 300;
  fireEvent.scroll(scroller);
  rerender(
    <ChatScreen
      {...common}
      backgroundTasks={[
        {
          ...task,
          pending_permission: {
            ...task.pending_permission!,
            toolCall: { toolCallId: "call_2" },
          },
        },
      ]}
    />,
  );
  expect(scroller.scrollTop).toBe(300);
});

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

// A background subagent asks after its parent turn ended. The chat of that parent
// session is where the person reads the conversation, so the prompt waits at the
// end of it, inside the transcript column - not in a panel that is closed by
// default - and answering it re-reads the task rows.
test("a background subagent's prompt waits at the end of its parent chat", async () => {
  vi.stubGlobal(
    "fetch",
    vi
      .fn()
      .mockImplementation(() => Promise.resolve({ ok: true, status: 204 })),
  );
  const refreshed = vi.fn();
  const { container } = render(
    <ChatScreen
      title="Audit"
      sessionId="sess_parent"
      heroAccentVerb="know"
      heroComposerFocusEpoch={0}
      onTitleSave={() => {}}
      items={[{ type: "user_message", id: "1", content: "audit it" }]}
      draft=""
      tokenUsage={null}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onDraftChange={() => {}}
      onSend={() => {}}
      backgroundTasks={[
        {
          id: "bg_1",
          session_id: "sess_parent",
          kind: "agent",
          label: "agent writer: audit",
          status: "running",
          started_at: "2026-09-14T10:00:00Z",
          timeout_seconds: 900,
          output_bytes: 0,
          output_truncated: false,
          elapsed_seconds: 5,
          overdue: false,
          running: true,
          agent: { name: "writer", session_id: "sess_child" },
          pending_permission: {
            sessionId: "sess_child",
            toolCall: {
              toolCallId: "call_7",
              title: "[subagent writer] Run: run_command",
            },
            options: [
              { optionId: "allow", name: "Allow once", kind: "allow_once" },
              { optionId: "reject", name: "Reject", kind: "reject_once" },
            ],
            agent_name: "writer",
          },
        },
      ]}
      onOpenBackgroundTasks={() => {}}
      onBackgroundTasksChanged={refreshed}
    />,
  );

  const card = screen.getByTestId("subagent-permission-bg_1");
  expect(container.querySelector(".messages-inner")?.contains(card)).toBe(true);
  fireEvent.click(screen.getByTestId("subagent-permission-reject-bg_1"));
  await waitFor(() => expect(refreshed).toHaveBeenCalledTimes(1));
  vi.unstubAllGlobals();
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

/**
 * The block over the transcript (the usage banner, the composer) is measured by
 * a ResizeObserver: a stand-in the test can fire, and a height it can set.
 */
function composerBlock(container: HTMLElement) {
  const host = container.querySelector(".chat-bottom-inner") as HTMLElement;
  expect(host).toBeTruthy();
  return {
    grow(height: number) {
      host.getBoundingClientRect = () =>
        ({ height, width: 0, top: 0, left: 0, right: 0, bottom: height, x: 0, y: 0 }) as DOMRect;
      act(() => resizeCallbacks.forEach((cb) => cb()));
    },
  };
}

const resizeCallbacks: Array<() => void> = [];
class ResizeObserverStandIn {
  private readonly fire: () => void;
  constructor(cb: ResizeObserverCallback) {
    this.fire = () => cb([], this as unknown as ResizeObserver);
  }
  observe() {
    resizeCallbacks.push(this.fire);
  }
  unobserve() {}
  disconnect() {
    const at = resizeCallbacks.indexOf(this.fire);
    if (at >= 0) resizeCallbacks.splice(at, 1);
  }
}

// The usage read answers after the transcript is on screen, so the banner rises
// over a transcript already parked at its newest message. The reserve under the
// transcript grows with it; a transcript that stays where it was then has its
// last lines under the banner, which is opaque.
test("a banner rising over the composer keeps a transcript at the newest message there", () => {
  vi.stubGlobal("ResizeObserver", ResizeObserverStandIn);
  try {
    const { container } = render(<ChatScreen {...scrollBase} items={firstTurn} />);
    const viewport = transcriptViewport(container, {
      scrollHeight: 1200,
      clientHeight: 400,
    });
    viewport.scrollTop = 800;
    fireEvent.scroll(viewport);

    Object.defineProperty(viewport, "scrollHeight", { value: 1260, configurable: true });
    composerBlock(container).grow(250);

    expect(viewport.scrollTop).toBe(860);
  } finally {
    resizeCallbacks.length = 0;
    vi.unstubAllGlobals();
  }
});

test("a banner rising over the composer leaves a reader who scrolled up where they are", () => {
  vi.stubGlobal("ResizeObserver", ResizeObserverStandIn);
  try {
    const { container } = render(<ChatScreen {...scrollBase} items={firstTurn} />);
    const viewport = transcriptViewport(container, {
      scrollHeight: 1200,
      clientHeight: 400,
    });
    viewport.scrollTop = 200;
    fireEvent.scroll(viewport);

    Object.defineProperty(viewport, "scrollHeight", { value: 1260, configurable: true });
    composerBlock(container).grow(250);

    expect(viewport.scrollTop).toBe(200);
  } finally {
    resizeCallbacks.length = 0;
    vi.unstubAllGlobals();
  }
});

// The button takes the reader to the newest message, which is below them. A
// measurement that puts "the bottom" above where the page already is (an
// on-screen keyboard that lets the page scroll past the old end) must not send
// them back up the transcript.
test("the scroll-to-bottom button never moves the transcript up", async () => {
  const { container } = render(<ChatScreen {...scrollBase} items={firstTurn} />);
  const viewport = transcriptViewport(container, { scrollHeight: 1200, clientHeight: 400 });
  viewport.scrollTop = 200;
  fireEvent.scroll(viewport);
  await waitFor(() => expect(scrollButtonShown()).toBe(true));

  // The page went on past the end the transcript measures (800).
  viewport.scrollTop = 900;
  fireEvent.click(screen.getByTestId("chat-scroll-bottom"));
  expect(viewport.scrollTop).toBe(900);
  await waitFor(() => expect(scrollButtonShown()).toBe(false));
});

// iOS Safari keeps innerHeight when its keyboard opens and shrinks only the
// visual viewport, so the composer block, fixed to the layout viewport's
// bottom, rode under the keyboard with the scroll-to-bottom button on it. The
// stacked shell lifts it by what the keyboard covers.
test("on the stacked shell the composer block rises above an overlaying keyboard", () => {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: query.includes("max-width: 1199px"),
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }));
  const vv = Object.assign(new EventTarget(), { height: window.innerHeight, offsetTop: 0, scale: 1 });
  Object.defineProperty(window, "visualViewport", { value: vv, configurable: true });
  const root = document.documentElement;
  try {
    const { unmount } = render(<ChatScreen {...scrollBase} items={firstTurn} />);
    expect(root.style.getPropertyValue("--coddy-keyboard-inset")).toBe("0px");
    vv.height = window.innerHeight - 320;
    act(() => {
      vv.dispatchEvent(new Event("resize"));
    });
    expect(root.style.getPropertyValue("--coddy-keyboard-inset")).toBe("320px");
    unmount();
    expect(root.style.getPropertyValue("--coddy-keyboard-inset")).toBe("");
  } finally {
    Object.defineProperty(window, "visualViewport", { value: undefined, configurable: true });
    vi.unstubAllGlobals();
  }
});

test("the empty hero has no scroll-to-bottom button", () => {
  render(<ChatScreen {...scrollBase} sessionId="" title="" items={[]} />);
  expect(screen.queryByTestId("chat-scroll-bottom")).toBeNull();
});

// The live line and the chip share one count of running tasks (taskStatus.ts).
function turnLineScreen(
  over: Partial<React.ComponentProps<typeof ChatScreen>>,
): React.ReactElement {
  const finished: BackgroundTask = {
    id: "bg_1",
    session_id: "sess_turn",
    kind: "command",
    label: "go build ./...",
    command: "go build ./...",
    status: "succeeded",
    started_at: "2026-09-18T10:00:00Z",
    timeout_seconds: 900,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 5,
    overdue: false,
    running: false,
  };
  return (
    <ChatScreen
      title="Build"
      sessionId="sess_turn"
      heroAccentVerb="know"
      heroComposerFocusEpoch={0}
      onTitleSave={() => {}}
      items={[{ type: "user_message", id: "u1", content: "build it" }]}
      draft=""
      tokenUsage={null}
      mode="agent"
      modes={["agent"]}
      onModeChange={() => {}}
      onDraftChange={() => {}}
      onSend={() => {}}
      generating={true}
      onStop={() => {}}
      backgroundTasks={[finished]}
      onOpenBackgroundTasks={() => {}}
      {...over}
    />
  );
}

test("the live line of a running turn carries the server's clock and token count", () => {
  render(
    turnLineScreen({
      turnProgress: {
        startedAtMs: Date.now() - 125_000,
        outputTokens: 1_200,
        estimated: true,
      },
    }),
  );
  expect(screen.getByTestId("typing-dots-turn-elapsed").textContent).toBe(
    "2m 05s",
  );
  expect(screen.getByTestId("typing-dots-turn-tokens").textContent).toBe(
    "1.2k tokens",
  );
});

test("until the server reports progress the turn clock counts from the user's message", () => {
  render(
    turnLineScreen({
      items: [
        {
          type: "user_message",
          id: "u1",
          content: "build it",
          createdAtUtc: new Date(Date.now() - 57_000).toISOString(),
        },
      ],
    }),
  );
  expect(screen.getByTestId("typing-dots-turn-elapsed").textContent).toBe(
    "57s",
  );
  expect(screen.queryByTestId("typing-dots-turn-tokens")).toBeNull();
});

test("the live line of a running turn names the running tasks and opens the Tasks panel", () => {
  const onOpen = vi.fn();
  const running: BackgroundTask = {
    id: "bg_2",
    session_id: "sess_turn",
    kind: "command",
    label: "make test",
    command: "make test",
    status: "running",
    started_at: "2026-09-18T10:00:00Z",
    timeout_seconds: 900,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 5,
    overdue: false,
    running: true,
  };
  const memory: BackgroundTask = {
    ...running,
    id: "bg_3",
    kind: "agent",
    agent: { name: "memory", system: true },
  };
  render(
    turnLineScreen({
      backgroundTasks: [running, memory],
      onOpenBackgroundTasks: onOpen,
    }),
  );
  // The memory run of the turn is a system task and is not counted.
  expect(screen.getByTestId("typing-dots-turn-tasks").textContent).toBe(
    "1 running task",
  );
  fireEvent.click(screen.getByTestId("typing-dots-turn-tasks"));
  expect(onOpen).toHaveBeenCalledTimes(1);
});

test("the transcript ends with the conversation: the way to the tasks is the header control", () => {
  render(turnLineScreen({ generating: false }));
  expect(screen.queryByTestId("bgtask-chip")).toBeNull();
  expect(screen.getByTestId("chat-header-tasks")).toBeInTheDocument();
});

test("the header control opens the Tasks panel and puts it away again", () => {
  const onOpen = vi.fn();
  const onClose = vi.fn();
  const { rerender } = render(
    turnLineScreen({
      generating: false,
      onOpenBackgroundTasks: onOpen,
      onCloseBackgroundTasks: onClose,
    }),
  );
  fireEvent.click(screen.getByTestId("chat-header-tasks"));
  expect(onOpen).toHaveBeenCalledTimes(1);
  rerender(
    turnLineScreen({
      generating: false,
      onOpenBackgroundTasks: onOpen,
      onCloseBackgroundTasks: onClose,
      backgroundTasksOpen: true,
    }),
  );
  fireEvent.click(screen.getByTestId("chat-header-tasks"));
  expect(onClose).toHaveBeenCalledTimes(1);
  expect(onOpen).toHaveBeenCalledTimes(1);
});

test("the turn has ended and its tasks have not: the tail keeps the dots and the count", () => {
  const onOpen = vi.fn();
  const running: BackgroundTask = {
    id: "bg_4",
    session_id: "sess_turn",
    kind: "command",
    label: "make test",
    command: "make test",
    status: "running",
    started_at: "2026-09-18T10:00:00Z",
    timeout_seconds: 900,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 5,
    overdue: false,
    running: true,
  };
  render(
    turnLineScreen({
      generating: false,
      backgroundTasks: [running],
      onOpenBackgroundTasks: onOpen,
    }),
  );
  expect(screen.getByTestId("typing-dots")).toBeInTheDocument();
  expect(screen.getByTestId("typing-dots-turn-tasks").textContent).toBe(
    "1 running task",
  );
  // No turn to time: the clock and the tokens are not on this line.
  expect(screen.queryByTestId("typing-dots-turn-elapsed")).toBeNull();
  expect(screen.queryByTestId("typing-dots-turn-tokens")).toBeNull();
  fireEvent.click(screen.getByTestId("typing-dots-turn-tasks"));
  expect(onOpen).toHaveBeenCalledTimes(1);
});

test("a finished turn whose tasks have finished too leaves the tail quiet", () => {
  render(turnLineScreen({ generating: false }));
  expect(screen.queryByTestId("typing-dots")).toBeNull();
});

// Issue #338. jsdom lays nothing out, so the transcript window stays off here
// and every row renders; what is left to hold is the control at the top.
test("the top of a transcript with history above offers it, says it is loading, and retries", () => {
  const onLoadOlder = vi.fn();
  const { rerender } = render(
    <ChatScreen
      {...scrollBase}
      items={firstTurn}
      transcriptHasOlder={true}
      olderTranscriptLoad="idle"
      onLoadOlderTranscript={onLoadOlder}
    />,
  );
  const control = screen.getByTestId("transcript-earlier");
  // It stands above the first row, inside the transcript's column.
  expect(control.parentElement?.classList.contains("messages-inner")).toBe(true);
  expect(control.nextElementSibling?.getAttribute("data-row-id")).toBe("u1");
  fireEvent.click(screen.getByRole("button", { name: "Show earlier messages" }));
  expect(onLoadOlder).toHaveBeenCalledTimes(1);

  rerender(
    <ChatScreen
      {...scrollBase}
      items={firstTurn}
      transcriptHasOlder={true}
      olderTranscriptLoad="loading"
      onLoadOlderTranscript={onLoadOlder}
    />,
  );
  expect(screen.getByRole("status")).toHaveTextContent("Loading earlier messages…");

  rerender(
    <ChatScreen
      {...scrollBase}
      items={firstTurn}
      transcriptHasOlder={true}
      olderTranscriptLoad="error"
      onLoadOlderTranscript={onLoadOlder}
    />,
  );
  expect(screen.getByRole("alert")).toHaveTextContent("Earlier messages did not load.");
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  expect(onLoadOlder).toHaveBeenCalledTimes(2);
});

test("a transcript holding its whole history has nothing above it", () => {
  render(<ChatScreen {...scrollBase} items={firstTurn} />);
  expect(screen.queryByTestId("transcript-earlier")).toBeNull();
});

test("the reader reaching the newest message is reported, and leaving it too", () => {
  const onAtTail = vi.fn();
  const { container } = render(
    <ChatScreen {...scrollBase} items={firstTurn} onReaderAtTailChange={onAtTail} />,
  );
  const viewport = transcriptViewport(container, {
    scrollHeight: 1200,
    clientHeight: 400,
  });
  viewport.scrollTop = 200;
  fireEvent.scroll(viewport);
  expect(onAtTail).toHaveBeenLastCalledWith(false);
  viewport.scrollTop = 800;
  fireEvent.scroll(viewport);
  expect(onAtTail).toHaveBeenLastCalledWith(true);
});

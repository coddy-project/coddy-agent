import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { AttentionTracker, TURN_END_SETTLE_MS } from "./attentionTracker";
import type { AttentionNotice } from "./notifications";
import { initLocale } from "../i18n/i18n";

let viewed = "";
let shown: AttentionNotice[] = [];
let timers: { fn: () => void; ms: number }[] = [];

function tracker(): AttentionTracker {
  return new AttentionTracker({
    viewedSessionId: () => viewed,
    sessionTitle: (sid) => (sid === "sess_a" ? "Fix the build" : ""),
    notify: (notice) => {
      shown.push(notice);
      return true;
    },
    setTimer: (fn, ms) => timers.push({ fn, ms }),
  });
}

function settle() {
  const due = timers;
  timers = [];
  for (const timer of due) {
    expect(timer.ms).toBe(TURN_END_SETTLE_MS);
    timer.fn();
  }
}

beforeEach(() => {
  initLocale("en");
  viewed = "";
  shown = [];
  timers = [];
});

afterEach(() => initLocale("en"));

describe("the end of a turn", () => {
  test("of the chat on screen is announced once it settled, tagged by its time", () => {
    viewed = "sess_a";
    const t = tracker();
    t.turnEnded("sess_a", "2026-10-10T10:00:00Z");
    expect(shown).toHaveLength(0);
    settle();
    expect(shown).toEqual([
      {
        kind: "turn_finished",
        sessionId: "sess_a",
        key: "2026-10-10T10:00:00Z",
        title: "Fix the build",
        body: "The agent finished its turn.",
      },
    ]);
  });

  test("of a chat this tab wrote to is announced while another one is on screen", () => {
    viewed = "sess_b";
    const t = tracker();
    t.noteSent("sess_a");
    t.turnEnded("sess_a", "t1");
    settle();
    expect(shown.map((n) => n.sessionId)).toEqual(["sess_a"]);
  });

  test("of a chat this tab has nothing to do with is not announced", () => {
    // A Telegram conversation, a scheduler run, a subagent's own session.
    viewed = "sess_a";
    const t = tracker();
    t.turnEnded("sess_telegram", "t1");
    t.turnEnded("sess_child", "t2");
    settle();
    expect(shown).toHaveLength(0);
  });

  test("says the turn failed when its stream said so, even a moment after the event", () => {
    viewed = "sess_a";
    const t = tracker();
    t.turnEnded("sess_a", "t1");
    t.noteStreamError("sess_a", "provider returned 429");
    settle();
    expect(shown[0]).toMatchObject({
      kind: "turn_failed",
      body: "The turn ended with an error: provider returned 429",
    });

    // The next turn starts clean.
    t.turnStarted("sess_a");
    t.turnEnded("sess_a", "t2");
    settle();
    expect(shown[1]).toMatchObject({ kind: "turn_finished", key: "t2" });
  });

  test("names a chat with no title yet by the product", () => {
    viewed = "sess_new";
    const t = tracker();
    t.turnEnded("sess_new", "t1");
    settle();
    expect(shown[0]!.title).toBe("Coddy");
  });
});

describe("what waits for an answer", () => {
  test("a permission request names the tool and is tagged by its call", () => {
    const t = tracker();
    t.permission({
      sessionId: "sess_a",
      toolCall: { toolCallId: "call_1", title: "run_command: make test" },
      options: [],
    });
    expect(shown).toEqual([
      {
        kind: "permission",
        sessionId: "sess_a",
        key: "call_1",
        title: "Fix the build",
        body: "Permission needed: run_command: make test",
      },
    ]);
  });

  test("a prompt the stream replays after a reconnect is announced once", () => {
    const t = tracker();
    const payload = {
      sessionId: "sess_a",
      toolCall: { toolCallId: "call_1", title: "run_command: make test" },
      options: [],
    };
    t.permission(payload);
    t.permission(payload);
    t.permission({ ...payload, toolCall: { toolCallId: "call_2" } });
    expect(shown.map((n) => n.key)).toEqual(["call_1", "call_2"]);
  });

  test("a question quotes its first line", () => {
    const t = tracker();
    t.question({
      sessionId: "sess_a",
      requestId: "q_1",
      questions: [{ question: "Which branch?", options: [] }],
    });
    expect(shown[0]).toMatchObject({
      kind: "question",
      key: "q_1",
      body: "The agent asks: Which branch?",
    });
  });

  test("a background subagent's prompt is announced live, once, and never from a replay", () => {
    viewed = "sess_a";
    const t = tracker();
    const prompt = {
      phase: "asked" as const,
      childSessionId: "sess_child",
      toolCallId: "call_9",
      agentName: "explore",
      toolTitle: "write README.md",
    };
    // The stream replays what was already waiting when it connects.
    t.subagentPermission("sess_a", prompt);
    expect(shown).toHaveLength(0);

    t.setLive(true);
    t.subagentPermission("sess_a", { ...prompt, toolCallId: "call_10" });
    t.subagentPermission("sess_a", { ...prompt, toolCallId: "call_10" });
    expect(shown).toEqual([
      {
        kind: "subagent_permission",
        sessionId: "sess_a",
        key: "sess_child:call_10",
        title: "Fix the build",
        body: "explore needs permission: write README.md",
      },
    ]);

    // A subagent of a chat this tab does not follow.
    t.subagentPermission("sess_other", { ...prompt, toolCallId: "call_11" });
    expect(shown).toHaveLength(1);
  });

  test("speaks the page's language", () => {
    initLocale("ru");
    viewed = "sess_a";
    const t = tracker();
    t.turnEnded("sess_a", "t1");
    settle();
    expect(shown[0]!.body).toBe("Агент закончил ход.");
  });
});

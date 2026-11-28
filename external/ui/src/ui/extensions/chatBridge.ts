import {
  deriveLiveStatus,
  withPreparingPhase,
  type LiveStatusKind,
} from "../chat/liveStatus";
import type { TranscriptItem } from "../chat/types";
import type { TurnProgress } from "../chat/turnProgress";

/**
 * The chat bridge: what the chat is doing right now, reported by the app
 * while it renders and handed to extensions as `coddy.chat`. An alternative
 * status line or a badge draws from this instead of parsing the stream.
 */
export type ChatProgress = Readonly<{
  startedAtMs: number;
  outputTokens: number;
  estimated: boolean;
  tokensPerSecond: number | null;
}>;
export type ChatActivity = "idle" | LiveStatusKind;

export type ChatStatus = Readonly<{
  sessionId: string;
  turnActive: boolean;
  activity: ChatActivity;
  progress: ChatProgress | null;
  tokenUsage: Readonly<{
    inputTokens: number;
    outputTokens: number;
    totalTokens: number;
  }> | null;
  permissionPending: boolean;
  questionPending: boolean;
}>;

/** The parsed status line of the chat, derived from the same transcript and
 * progress the host's own line reads. No extension has to scan stream rows. */
export function snapshotChatStatus(input: {
  sessionId: string;
  turnActive: boolean;
  items: readonly TranscriptItem[];
  progress: TurnProgress | null;
  tokenUsage: ChatStatus["tokenUsage"];
  permissionPending: boolean;
  questionPending: boolean;
}): ChatStatus {
  const progress = input.turnActive ? input.progress : null;
  return {
    sessionId: input.sessionId,
    turnActive: input.turnActive,
    activity: input.turnActive
      ? withPreparingPhase(deriveLiveStatus(input.items), progress).kind
      : "idle",
    progress: progress
      ? {
          startedAtMs: progress.startedAtMs,
          outputTokens: progress.outputTokens,
          estimated: progress.estimated,
          tokensPerSecond: progress.tokensPerSecond ?? null,
        }
      : null,
    tokenUsage: input.tokenUsage,
    permissionPending: input.permissionPending,
    questionPending: input.questionPending,
  };
}

type StatusListener = (status: ChatStatus) => void;

export class ChatBridge {
  private status: ChatStatus | null = null;
  private listeners = new Map<StatusListener, AbortSignal>();

  /** The app reports its status every render; listeners hear real changes. */
  report(status: ChatStatus | null) {
    const before = JSON.stringify(this.status ?? null);
    this.status = status;
    if (JSON.stringify(status ?? null) === before) return;
    if (!status) return;
    for (const [fn, signal] of [...this.listeners]) {
      if (signal.aborted) {
        this.listeners.delete(fn);
        continue;
      }
      try {
        fn(status);
      } catch {
        this.listeners.delete(fn);
      }
    }
  }

  current(): ChatStatus | null {
    return this.status;
  }

  onStatusChange(
    signal: AbortSignal,
    listener: StatusListener,
  ): { remove: () => void } {
    if (signal.aborted) throw new Error("Extension disposed");
    if (typeof listener !== "function")
      throw new Error("A listener is required");
    const remove = () => {
      if (!this.listeners.delete(listener)) return;
      signal.removeEventListener("abort", remove);
    };
    this.listeners.set(listener, signal);
    signal.addEventListener("abort", remove, { once: true });
    return { remove };
  }
}

export const chatBridge = new ChatBridge();

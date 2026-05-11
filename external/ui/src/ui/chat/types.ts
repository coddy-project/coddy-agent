export type TokenUsage = {
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
};

export type TranscriptItem =
  | {
      id: string;
      type: "user_message";
      content: string;
      /** RFC3339 UTC from server created_at or client clock when sending. */
      createdAtUtc?: string;
    }
  | {
      id: string;
      type: "thinking";
      status: "in_progress" | "completed";
      content: string;
      durationMs?: number;
      startedAtMs?: number;
    }
  | {
      id: string;
      type: "assistant_message";
      content: string;
      streaming?: boolean;
      /** RFC3339 UTC when the assistant reply was finalized (persisted). */
      createdAtUtc?: string;
    }
  | {
      id: string;
      type: "tool_call";
      toolCallId: string;
      title?: string;
      kind?: string;
      status: "pending" | "in_progress" | "completed" | "failed" | "cancelled";
      argsText?: string;
      /** Truncated preview from SSE or list endpoint (never replace with full body). */
      resultText?: string;
      /** Full saved tool output after user chose Load more (GET …/tool-calls/{id}). */
      fullResultText?: string;
      /** True when SSE or list preview omitted lines (_meta or resultPreviewTruncated). */
      resultWasTruncated?: boolean;
      startedAtMs?: number;
      finishedAtMs?: number;
      durationMs?: number;
    }
  | {
      id: string;
      type: "system_notice";
      level: "error";
      message: string;
    }
  | {
      id: string;
      type: "memory_copilot";
      memoryRowId: string;
      userTurnIndex: number;
      /** Single before-main-agent memory pass (preferred). Legacy rows may omit this. */
      memoryStatus?: "idle" | "in_progress" | "completed";
      memoryText?: string;
      recallStatus: "idle" | "in_progress" | "completed";
      persistStatus: "idle" | "in_progress" | "completed";
      recallText: string;
      recallReasoning: string;
      persistText: string;
      persistReasoning: string;
      recallDurationMs?: number;
      persistDurationMs?: number;
      /** Wall clock from memory phase start until completed (live until then). */
      memoryWallStartedAtMs?: number;
      /** When main-model thinking rows appear while recall/persist SSE is still marked busy, cap the live wall-clock label at this elapsed ms so it does not keep climbing beside thinking (see freezeMemoryWallWhenThinkingAfterRecall). */
      memoryWallLiveCapMs?: number;
      memoryWallDurationMs?: number;
      persistSaved?: boolean;
      persistRelativePath?: string;
      persistTitle?: string;
      /** Markdown body written when PersistSaved (from server, may be truncated). */
      persistSavedBody?: string;
      /** scope:relative paths read via coddy_memory_read during recall. */
      recallReadPaths?: string[];
    };

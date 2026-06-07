import type { CoddyPermissionPayload, PermissionResolvedState } from "./permissionTypes";
import type { CoddyQuestionPayload, QuestionResolvedState } from "./questionTypes";

export type TokenUsage = {
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
};

export type TranscriptItem =
  | {
      id: string;
      type: "permission_prompt";
      payload: CoddyPermissionPayload;
      resolved?: PermissionResolvedState;
    }
  | {
      id: string;
      type: "question_prompt";
      payload: CoddyQuestionPayload;
      resolved?: QuestionResolvedState;
    }
  | {
      id: string;
      type: "plan_document";
      slug: string;
      name: string;
      overview: string;
      content: string;
      /** Markdown body only (no YAML frontmatter). */
      body?: string;
      /** Absolute path to plans/<slug>.plan.md in the session bundle. */
      path?: string;
      expanded: boolean;
      /** User discarded the plan in UI; card stays visible but inactive. */
      discarded?: boolean;
      updatedAtUtc?: string;
    }
  | {
      id: string;
      type: "user_message";
      content: string;
      /** RFC3339 UTC from server created_at or client clock when sending. */
      createdAtUtc?: string;
      /** Inline file attachments sent with this message. */
      files?: { name: string; mimeType: string; sizeBytes?: number }[];
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
      createdAtUtc?: string;
    }
  | {
      id: string;
      type: "branch_nav";
      /** 0-based index of the user message that this nav is attached to. */
      userMessageIndex: number;
      /** 0-based index of the branch currently being viewed. */
      currentIndex: number;
      total: number;
      sessions: Array<{ sessionId: string; preview?: string }>;
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

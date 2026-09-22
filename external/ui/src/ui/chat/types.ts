import type {
  CoddyPermissionPayload,
  PermissionResolvedState,
} from "./permissionTypes";
import type {
  CoddyQuestionPayload,
  QuestionResolvedState,
} from "./questionTypes";
import type { TodoPlanEntry } from "./todoToolPreview";
import type { BackgroundWakeTask } from "./backgroundWake";

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
      files?: {
        name: string;
        mimeType: string;
        sizeBytes?: number;
        /** Blob URL while optimistic; session asset URL after backend persistence. */
        previewUrl?: string;
        /**
         * The full-size asset the preview card opens enlarged. Server-only:
         * absent while the row is optimistic, on a message sent before the
         * route existed, and once the asset has left the session bundle.
         */
        url?: string;
      }[];
    }
  | {
      /**
       * The first message of a turn nobody typed: background tasks the model
       * started with notify_on_finish ended and the server woke the agent. It
       * opens a turn like a user message and renders nothing - the turn reads
       * as the agent carrying on - built from the `background_wake` frame live
       * and from the message's `background_wake` field after a reload.
       */
      id: string;
      type: "background_wake";
      tasks: BackgroundWakeTask[];
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
      /** A compaction summary row (server compaction_summary=true) rendered as a
          foldout showing what is now in the context. */
      id: string;
      type: "compaction";
      summary: string;
      createdAtUtc?: string;
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
      /** Full saved tool output after the first More… click (GET …/tool-calls/{id}). */
      fullResultText?: string;
      /** True when SSE or list preview omitted lines (_meta or resultPreviewTruncated). */
      resultWasTruncated?: boolean;
      /** Final todo state saved with this call, so historical cards stay stable. */
      todoPlan?: TodoPlanEntry[];
      startedAtMs?: number;
      finishedAtMs?: number;
      durationMs?: number;
    }
  | {
      id: string;
      type: "system_notice";
      /** error rows carry the retry control; notice rows are informational. */
      level: "error" | "notice";
      message: string;
      createdAtUtc?: string;
    }
  | {
      id: string;
      /**
       * The memory subagent run of the turn, from the `memory_run` events:
       * nothing renders it, the live status line reads it while it runs. The
       * run's record is the Tasks drawer and the child transcript it names.
       */
      type: "memory_run";
      status: "started" | "finished" | "skipped";
      taskId?: string;
      childSessionId?: string;
      startedAtMs?: number;
      taskStatus?: string;
      durationMs?: number;
      delivered?: boolean;
      reason?: string;
    };

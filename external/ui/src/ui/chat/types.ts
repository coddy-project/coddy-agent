export type TokenUsage = {
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
};

export type TranscriptItem =
  | {
      id: string;
      type: 'user_message';
      content: string;
    }
  | {
      id: string;
      type: 'thinking';
      status: 'in_progress' | 'completed';
      content: string;
      durationMs?: number;
      startedAtMs?: number;
    }
  | {
      id: string;
      type: 'assistant_message';
      content: string;
      streaming?: boolean;
    }
  | {
      id: string;
      type: 'tool_call';
      toolCallId: string;
      title?: string;
      kind?: string;
      status: 'pending' | 'in_progress' | 'completed' | 'failed' | 'cancelled';
      argsText?: string;
      resultText?: string;
      detailsLoaded?: boolean;
      startedAtMs?: number;
      finishedAtMs?: number;
      durationMs?: number;
    };


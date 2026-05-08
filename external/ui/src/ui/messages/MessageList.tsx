import type { TranscriptItem } from '../chat/types';
import { AssistantMessage } from './AssistantMessage';
import { ThinkingMessage } from './ThinkingMessage';
import { ToolCallMessage } from './ToolCallMessage';
import { UserMessage } from './UserMessage';

export function MessageList(props: { items: TranscriptItem[]; onLoadToolCallDetails?: (toolCallId: string) => void }) {
  return (
    <>
      {props.items.map((it) => {
        if (it.type === 'user_message') {
          return <UserMessage key={it.id} content={it.content} />;
        }
        if (it.type === 'thinking') {
          return (
            <ThinkingMessage
              key={it.id}
              status={it.status}
              content={it.content}
              durationMs={it.durationMs}
              startedAtMs={it.startedAtMs}
            />
          );
        }
        if (it.type === 'assistant_message') {
          return <AssistantMessage key={it.id} content={it.content} />;
        }
        return (
          <ToolCallMessage
            key={it.id}
            toolCallId={it.toolCallId}
            status={it.status}
            {...(it.title !== undefined ? { title: it.title } : {})}
            {...(it.kind !== undefined ? { kind: it.kind } : {})}
            {...(it.argsText !== undefined ? { argsText: it.argsText } : {})}
            {...(it.resultText !== undefined ? { resultText: it.resultText } : {})}
            {...(it.detailsLoaded !== undefined ? { detailsLoaded: it.detailsLoaded } : {})}
            {...(it.durationMs !== undefined ? { durationMs: it.durationMs } : {})}
            {...(props.onLoadToolCallDetails ? { onLoadDetails: props.onLoadToolCallDetails } : {})}
          />
        );
      })}
    </>
  );
}


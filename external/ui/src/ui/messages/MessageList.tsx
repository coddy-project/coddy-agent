import type { TranscriptItem } from '../chat/types';
import { AssistantMessage } from './AssistantMessage';
import { ToolCallMessage } from './ToolCallMessage';
import { UserMessage } from './UserMessage';

export function MessageList(props: { items: TranscriptItem[] }) {
  return (
    <>
      {props.items.map((it) => {
        if (it.type === 'user_message') {
          return <UserMessage key={it.id} content={it.content} />;
        }
        if (it.type === 'assistant_message') {
          return <AssistantMessage key={it.id} content={it.content} />;
        }
        return (
          <ToolCallMessage
            key={it.id}
            title={it.title}
            kind={it.kind}
            status={it.status}
            argsText={it.argsText}
            resultText={it.resultText}
          />
        );
      })}
    </>
  );
}


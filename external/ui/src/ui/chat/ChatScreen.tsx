import { useEffect, useRef } from 'react';
import type { TokenUsage, TranscriptItem } from './types';
import { ChatHeader } from './ChatHeader';
import { Composer } from './Composer';
import { TokenBar } from './TokenBar';
import { MessageList } from '../messages/MessageList';

export function ChatScreen(props: {
  title: string;
  items: TranscriptItem[];
  draft: string;
  tokenUsage: TokenUsage | null;
  onDraftChange: (v: string) => void;
  onSend: (text: string) => void;
}) {
  const messagesRef = useRef<HTMLDivElement | null>(null);
  const isEmpty = props.items.length === 0;

  useEffect(() => {
    if (messagesRef.current) {
      messagesRef.current.scrollTop = messagesRef.current.scrollHeight;
    }
  }, [props.items]);

  return (
    <main className={`main ${isEmpty ? 'is-empty' : ''}`}>
      <ChatHeader title={props.title} />

      <div className="hero" id="hero" hidden={!isEmpty}>
        <h1 className="hero-title">What do you want to know?</h1>
      </div>

      <div id="messages" className="messages" aria-live="polite" ref={messagesRef}>
        <MessageList items={props.items} />
      </div>

      <TokenBar usage={props.tokenUsage} />

      <Composer
        value={props.draft}
        isEmpty={isEmpty}
        onChange={props.onDraftChange}
        onSend={props.onSend}
      />
    </main>
  );
}


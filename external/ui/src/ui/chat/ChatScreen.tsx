import type { CSSProperties } from 'react';
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { TokenUsage, TranscriptItem } from './types';
import { ChatHeader } from './ChatHeader';
import { Composer } from './Composer';
import { MessageList } from '../messages/MessageList';

export function ChatScreen(props: {
  title: string;
  sessionId: string;
  onTitleSave: (title: string) => void;
  items: TranscriptItem[];
  draft: string;
  tokenUsage: TokenUsage | null;
  contextPct?: number;
  maxContextTokens?: number;
  mode: string;
  modes: string[];
  llmModels?: string[];
  llmModel?: string;
  onLlmModelChange?: (modelId: string) => void;
  onModeChange: (mode: string) => void;
  onDraftChange: (v: string) => void;
  onSend: (text: string) => void;
  generating?: boolean;
  onStop?: () => void;
  /** Fetch persisted full tool output; UI keeps preview in resultText. */
  onFetchToolCallFull?: (toolCallId: string) => Promise<void>;
}) {
  const messagesRef = useRef<HTMLDivElement | null>(null);
  const composerHostRef = useRef<HTMLDivElement | null>(null);
  const isEmpty = props.items.length === 0;
  const stickToBottomRef = useRef(true);
  const [composerReserve, setComposerReserve] = useState(200);

  useLayoutEffect(() => {
    if (isEmpty) return;
    const host = composerHostRef.current;
    if (!host) return;
    const extra = 10;
    const apply = () => {
      const h = host.getBoundingClientRect().height;
      setComposerReserve(Math.max(140, Math.ceil(h) + extra));
    };
    apply();
    const ro = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(apply) : null;
    ro?.observe(host);
    return () => ro?.disconnect();
  }, [isEmpty, props.tokenUsage]);

  useEffect(() => {
    const el = messagesRef.current;
    if (!el) return;
    if (!stickToBottomRef.current) return;
    el.scrollTop = el.scrollHeight;
  }, [props.items]);

  return (
    <main className={`main ${isEmpty ? 'is-empty' : ''}`}>
      {isEmpty ? null : <ChatHeader title={props.title} editable={true} onTitleSave={props.onTitleSave} />}

      {isEmpty ? (
        <div className="hero" id="hero">
          <h1 className="hero-title">What do you want to know?</h1>
          <div className="hero-composer">
            <Composer
              value={props.draft}
              isEmpty={true}
              sessionId={props.sessionId}
              contextIdle={!props.sessionId}
              mode={props.mode}
              modes={props.modes}
              tokenUsage={props.tokenUsage}
              {...(props.contextPct !== undefined ? { contextPct: props.contextPct } : {})}
              {...(props.maxContextTokens !== undefined ? { maxContextTokens: props.maxContextTokens } : {})}
              {...(props.llmModels !== undefined &&
              props.llmModels.length > 0 &&
              props.onLlmModelChange !== undefined
                ? { llmModels: props.llmModels, llmModel: props.llmModel, onLlmModelChange: props.onLlmModelChange }
                : {})}
              onModeChange={props.onModeChange}
              onChange={props.onDraftChange}
              onSend={props.onSend}
              {...(props.generating === true && props.onStop !== undefined ? { generating: true, onStop: props.onStop } : {})}
            />
          </div>
        </div>
      ) : (
        <div
          className="chat-stack"
          style={
            {
              '--chat-composer-reserve': `${composerReserve}px`,
            } as CSSProperties
          }
        >
          <div
            id="messages"
            className="messages"
            aria-live="polite"
            ref={messagesRef}
            onScroll={() => {
              const el = messagesRef.current;
              if (!el) return;
              const dist = el.scrollHeight - el.scrollTop - el.clientHeight;
              stickToBottomRef.current = dist < 80;
            }}
          >
            <div className="messages-inner">
              <MessageList
                items={props.items}
                {...(props.onFetchToolCallFull ? { onFetchToolCallFull: props.onFetchToolCallFull } : {})}
              />
            </div>
            <div className="chat-scroll-tail" aria-hidden />
          </div>

          <div className="chat-bottom">
            <div className="chat-bottom-inner" ref={composerHostRef}>
              <Composer
                value={props.draft}
                isEmpty={false}
                sessionId={props.sessionId}
                contextIdle={false}
                mode={props.mode}
                modes={props.modes}
                tokenUsage={props.tokenUsage}
                {...(props.contextPct !== undefined ? { contextPct: props.contextPct } : {})}
                {...(props.maxContextTokens !== undefined ? { maxContextTokens: props.maxContextTokens } : {})}
                {...(props.llmModels !== undefined &&
                props.llmModels.length > 0 &&
                props.onLlmModelChange !== undefined
                  ? { llmModels: props.llmModels, llmModel: props.llmModel, onLlmModelChange: props.onLlmModelChange }
                  : {})}
                onModeChange={props.onModeChange}
                onChange={props.onDraftChange}
                onSend={props.onSend}
                {...(props.generating === true && props.onStop !== undefined ? { generating: true, onStop: props.onStop } : {})}
              />
            </div>
          </div>
        </div>
      )}
    </main>
  );
}

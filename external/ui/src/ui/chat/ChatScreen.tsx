import type { CSSProperties } from "react";
import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type { HeroAccentVerb } from "./heroTitleWords";
import type { QuestionResolvedState } from "./questionTypes";
import type { TokenUsage, TranscriptItem } from "./types";
import { ChatHeader } from "./ChatHeader";
import { Composer } from "./Composer";
import { MessageList } from "../messages/MessageList";
import { shellStackMaxWidthMediaQuery } from "../shellBreakpoint";

const DOC_SCROLL_SHELL_STACK_MQ = shellStackMaxWidthMediaQuery;

function subscribeMobileDocScroll(cb: () => void) {
  if (typeof window === "undefined") return () => {};
  const mq = window.matchMedia(DOC_SCROLL_SHELL_STACK_MQ);
  mq.addEventListener("change", cb);
  return () => mq.removeEventListener("change", cb);
}

function snapshotMobileDocScroll() {
  return (
    typeof window !== "undefined" &&
    window.matchMedia(DOC_SCROLL_SHELL_STACK_MQ).matches
  );
}

function serverSnapshotMobileDocScroll() {
  return false;
}

export function ChatScreen(props: {
  title: string;
  sessionId: string;
  /** Accent verb for "What do you want to …?" on the empty hero (session-stable or home rotation). */
  heroAccentVerb: HeroAccentVerb;
  /** Bumps when the user starts a fresh home chat so the composer can refocus. */
  heroComposerFocusEpoch: number;
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
  onQuestionPromptResolved?: (
    sessionId: string,
    itemId: string,
    resolved: QuestionResolvedState,
  ) => void;
}) {
  const messagesRef = useRef<HTMLDivElement | null>(null);
  const composerHostRef = useRef<HTMLDivElement | null>(null);
  const isEmpty = props.items.length === 0;
  const stickToBottomRef = useRef(true);
  const [composerReserve, setComposerReserve] = useState(200);
  const mobileDocScroll = useSyncExternalStore(
    subscribeMobileDocScroll,
    snapshotMobileDocScroll,
    serverSnapshotMobileDocScroll,
  );

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
    const ro =
      typeof ResizeObserver !== "undefined" ? new ResizeObserver(apply) : null;
    ro?.observe(host);
    return () => ro?.disconnect();
  }, [isEmpty, props.tokenUsage]);

  useEffect(() => {
    if (isEmpty) return;
    if (!stickToBottomRef.current) return;
    if (mobileDocScroll) {
      const run = () => {
        const top = Math.max(
          document.body.scrollHeight,
          document.documentElement.scrollHeight,
        );
        window.scrollTo({ top, left: 0, behavior: "auto" });
      };
      requestAnimationFrame(() => requestAnimationFrame(run));
      return;
    }
    const el = messagesRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [props.items, isEmpty, mobileDocScroll]);

  useEffect(() => {
    if (isEmpty) return;
    const onScroll = () => {
      if (mobileDocScroll) {
        const doc = document.documentElement;
        const dist = doc.scrollHeight - window.scrollY - window.innerHeight;
        stickToBottomRef.current = dist < 80;
      } else {
        const el = messagesRef.current;
        if (!el) return;
        const dist = el.scrollHeight - el.scrollTop - el.clientHeight;
        stickToBottomRef.current = dist < 80;
      }
    };
    if (mobileDocScroll) {
      window.addEventListener("scroll", onScroll, { passive: true });
      return () => window.removeEventListener("scroll", onScroll);
    }
    const el = messagesRef.current;
    el?.addEventListener("scroll", onScroll, { passive: true });
    return () => el?.removeEventListener("scroll", onScroll);
  }, [isEmpty, mobileDocScroll]);

  return (
    <main className={`main ${isEmpty ? "is-empty" : ""}`}>
      {isEmpty ? (
        <div className="hero" id="hero">
          <h1 className="hero-title">
            <span className="hero-title-muted">
              What do you want to{" "}
              <span
                className="hero-title-accent"
                data-testid="hero-title-accent"
              >
                {props.heroAccentVerb}
              </span>
              ?
            </span>
          </h1>
          <div className="hero-composer">
            <Composer
              value={props.draft}
              isEmpty={true}
              focusEpoch={props.heroComposerFocusEpoch}
              sessionId={props.sessionId}
              contextIdle={!props.sessionId}
              mode={props.mode}
              modes={props.modes}
              tokenUsage={props.tokenUsage}
              {...(props.contextPct !== undefined
                ? { contextPct: props.contextPct }
                : {})}
              {...(props.maxContextTokens !== undefined
                ? { maxContextTokens: props.maxContextTokens }
                : {})}
              {...(props.llmModels !== undefined &&
              props.llmModels.length > 0 &&
              props.onLlmModelChange !== undefined
                ? {
                    llmModels: props.llmModels,
                    llmModel: props.llmModel,
                    onLlmModelChange: props.onLlmModelChange,
                  }
                : {})}
              onModeChange={props.onModeChange}
              onChange={props.onDraftChange}
              onSend={props.onSend}
              {...(props.generating === true && props.onStop !== undefined
                ? { generating: true, onStop: props.onStop }
                : {})}
            />
          </div>
        </div>
      ) : (
        <div
          className="chat-stack"
          style={
            {
              "--chat-composer-reserve": `${composerReserve}px`,
            } as CSSProperties
          }
        >
          <div
            id="messages"
            className="chat-scroll"
            aria-live="polite"
            ref={messagesRef}
          >
            <div className="chat-scroll-sticky-head">
              <div className="chat-title-column">
                <ChatHeader
                  title={props.title}
                  editable={true}
                  onTitleSave={props.onTitleSave}
                />
              </div>
            </div>
            <div className="messages-inner">
              <MessageList
                items={props.items}
                {...(props.onFetchToolCallFull
                  ? { onFetchToolCallFull: props.onFetchToolCallFull }
                  : {})}
                {...(props.onQuestionPromptResolved
                  ? { onQuestionPromptResolved: props.onQuestionPromptResolved }
                  : {})}
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
                {...(props.contextPct !== undefined
                  ? { contextPct: props.contextPct }
                  : {})}
                {...(props.maxContextTokens !== undefined
                  ? { maxContextTokens: props.maxContextTokens }
                  : {})}
                {...(props.llmModels !== undefined &&
                props.llmModels.length > 0 &&
                props.onLlmModelChange !== undefined
                  ? {
                      llmModels: props.llmModels,
                      llmModel: props.llmModel,
                      onLlmModelChange: props.onLlmModelChange,
                    }
                  : {})}
                onModeChange={props.onModeChange}
                onChange={props.onDraftChange}
                onSend={props.onSend}
                {...(props.generating === true && props.onStop !== undefined
                  ? { generating: true, onStop: props.onStop }
                  : {})}
              />
            </div>
          </div>
        </div>
      )}
    </main>
  );
}

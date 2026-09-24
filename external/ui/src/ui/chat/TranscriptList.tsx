import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type MutableRefObject,
  type ReactNode,
  type RefObject,
} from "react";

import { MessageList, type MessageListProps } from "../messages/MessageList";
import { TranscriptEarlierControl } from "./TranscriptEarlierControl";
import type { TranscriptItem } from "./types";
import {
  transcriptWindowSupported,
  useTranscriptWindow,
} from "./useTranscriptWindow";

/** What the chat screen asks of the transcript window. */
export type TranscriptListHandle = {
  /** The window reaches the newest row. */
  attached: boolean;
  /** Puts the window back on the newest rows; `then` runs once it is. */
  attachToTail: (then?: () => void) => void;
};

/**
 * The transcript's column (`.messages-inner`): the rows of the transcript
 * window, the control standing for what is above them, and what follows the
 * newest row (issue #338).
 *
 * The window's state lives here rather than in the chat screen, so a frame
 * that renders a few more rows while the reader scrolls re-renders this list
 * and nothing around it - not the composer.
 */
export function TranscriptList(props: {
  items: TranscriptItem[];
  sessionId: string;
  scrollerRef: RefObject<HTMLElement | null>;
  docScroll: boolean;
  stickToBottomRef: RefObject<boolean>;
  hasOlder: boolean;
  olderLoad: "idle" | "loading" | "error";
  onLoadOlder: () => void;
  handleRef: MutableRefObject<TranscriptListHandle | null>;
  /** Called after the window reached or left the newest row. */
  onAttachedChange: (attached: boolean) => void;
  messageList: Omit<MessageListProps, "items" | "renderStart" | "renderEnd">;
  /** Shown after the newest row, and only while it is rendered. */
  tail?: ReactNode;
}) {
  const listRef = useRef<HTMLDivElement | null>(null);
  const [enabled] = useState(transcriptWindowSupported);
  const onLoadOlderRef = useRef(props.onLoadOlder);
  onLoadOlderRef.current = props.onLoadOlder;
  const win = useTranscriptWindow({
    items: props.items,
    resetKey: props.sessionId,
    enabled,
    listRef,
    scrollerRef: props.scrollerRef,
    docScroll: props.docScroll,
    hasOlder: props.hasOlder,
    olderLoading: props.olderLoad === "loading",
    olderFailed: props.olderLoad === "error",
    onLoadOlder: () => onLoadOlderRef.current(),
    stickToBottomRef: props.stickToBottomRef,
  });

  useLayoutEffect(() => {
    props.handleRef.current = {
      attached: win.attached,
      attachToTail: win.attachToTail,
    };
  });
  const lastAttachedRef = useRef(win.attached);
  const onAttachedChangeRef = useRef(props.onAttachedChange);
  onAttachedChangeRef.current = props.onAttachedChange;
  useEffect(() => {
    if (lastAttachedRef.current === win.attached) return;
    lastAttachedRef.current = win.attached;
    onAttachedChangeRef.current(win.attached);
  }, [win.attached]);

  return (
    <div className="messages-inner" ref={listRef}>
      {win.start > 0 || props.hasOlder ? (
        <TranscriptEarlierControl
          state={win.start > 0 ? "idle" : props.olderLoad}
          onShow={win.showEarlier}
          sentinelRef={win.topSentinelRef}
        />
      ) : null}
      <MessageList
        {...props.messageList}
        items={props.items}
        renderStart={win.start}
        renderEnd={win.end}
      />
      {win.attached ? (
        props.tail
      ) : (
        <div
          ref={win.bottomSentinelRef}
          className="transcript-window-edge"
          aria-hidden
        />
      )}
    </div>
  );
}

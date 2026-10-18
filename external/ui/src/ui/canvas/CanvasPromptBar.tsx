import { useLayoutEffect, useRef } from "react";
import { useT } from "../i18n/I18nProvider";
import { composerEnterAction, insertNewline } from "../chat/composerEnter";
import { pickLabel, type CanvasPick } from "./pickPrompt";

interface Props {
  text: string;
  onText: (text: string) => void;
  picks: CanvasPick[];
  onRemovePick: (id: string) => void;
  /** The chat's turn is running: Send queues, Stop stops it. */
  busy: boolean;
  onSend: () => void;
  onStop: () => void;
  onChat: () => void;
  touchOnly: boolean;
}

/**
 * The foot of a canvas board: the elements picked so far as chips, a field
 * that grows with its text, Send (which goes the way the composer's Send goes:
 * the chat's mode and model, the queue while a turn runs) and Stop.
 */
export function CanvasPromptBar(props: Props) {
  const { t } = useT();
  const fieldRef = useRef<HTMLTextAreaElement | null>(null);
  const canSend = props.text.trim() !== "" || props.picks.length > 0;

  useLayoutEffect(() => {
    const el = fieldRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 160)}px`;
  }, [props.text]);

  return (
    <div className="canvas-bar" data-testid="canvas-bar">
      {props.picks.length > 0 ? (
        <div className="canvas-picks" aria-label={t("canvas.picks")}>
          {props.picks.map((p) => (
            <span
              key={p.id}
              className="canvas-pick"
              data-testid="canvas-pick"
              title={p.element.selector}
            >
              <span className="canvas-pick-name">{pickLabel(p)}</span>
              {p.note.trim() ? (
                <span className="canvas-pick-note">{p.note.trim()}</span>
              ) : null}
              <button
                type="button"
                className="canvas-pick-remove"
                aria-label={t("canvas.pickRemove")}
                onClick={() => props.onRemovePick(p.id)}
              >
                ×
              </button>
            </span>
          ))}
        </div>
      ) : null}
      <div className="canvas-bar-row">
        <button
          type="button"
          className="canvas-bar-chat"
          data-testid="canvas-chat"
          onClick={props.onChat}
          title={t("canvas.toChat")}
        >
          {t("canvas.chat")}
        </button>
        <textarea
          ref={fieldRef}
          className="canvas-bar-field"
          data-testid="canvas-bar-field"
          rows={1}
          value={props.text}
          placeholder={
            props.busy ? t("canvas.placeholderBusy") : t("canvas.placeholder")
          }
          aria-label={t("canvas.placeholder")}
          enterKeyHint={props.touchOnly ? "enter" : "send"}
          onChange={(e) => props.onText(e.target.value)}
          onKeyDown={(e) => {
            const action = composerEnterAction(
              {
                key: e.key,
                shiftKey: e.shiftKey,
                ctrlKey: e.ctrlKey,
                altKey: e.altKey,
                metaKey: e.metaKey,
                isComposing: e.nativeEvent.isComposing,
                keyCode: e.keyCode,
                repeat: e.repeat,
              },
              props.touchOnly,
            );
            if (action === "send") {
              e.preventDefault();
              if (canSend) props.onSend();
            } else if (action === "newline-insert") {
              e.preventDefault();
              const el = e.currentTarget;
              const { text, caret } = insertNewline(
                el.value,
                el.selectionStart,
                el.selectionEnd,
              );
              props.onText(text);
              requestAnimationFrame(() => el.setSelectionRange(caret, caret));
            }
          }}
        />
        {/* One button, as the composer's: Stop while a turn runs and the
            field is empty, Queue once it is not, Send when idle. */}
        {props.busy && !canSend ? (
          <button
            type="button"
            className="canvas-bar-send composer-run-icon composer-run-icon--stop composer-send-stop"
            data-testid="canvas-stop"
            aria-label={t("canvas.stop")}
            title={t("canvas.stop")}
            onClick={props.onStop}
          >
            <span className="composer-send-glyph" aria-hidden="true">
              <span className="composer-stop-square" />
            </span>
          </button>
        ) : (
          <button
            type="button"
            className={
              "canvas-bar-send" +
              (props.busy ? " composer-run-icon--queue" : "")
            }
            data-testid="canvas-send"
            disabled={!canSend}
            aria-label={props.busy ? t("canvas.queue") : t("canvas.send")}
            title={props.busy ? t("canvas.queue") : t("canvas.send")}
            onClick={props.onSend}
          >
            <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
              <path d="M5 3.5v9l7-4.5z" fill="currentColor" />
            </svg>
          </button>
        )}
      </div>
    </div>
  );
}

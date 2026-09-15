import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";
import {
  addTag,
  MAX_SESSION_TAGS,
  normalizeTag,
  removeTag,
  suggestTags,
  tagsAreFull,
} from "./tagEditing";

/**
 * The labels of one conversation, edited by hand. One editor serves both places
 * that show tags - the row menu in History and the session table - because they
 * are the same three gestures: drop one, type one, take one the history already
 * uses.
 *
 * Every change is reported at once rather than behind a Save: there is nothing
 * here to review, and a dialog that has to be confirmed is a dialog that gets
 * left open. It is rendered into the document for the same reason the row menu
 * is: the drawer clips what overflows it.
 */
export function SessionTagEditor(props: {
  open: boolean;
  /** The control the editor hangs under. */
  anchor: DOMRect | null;
  tags: string[];
  /** Labels this history already uses, most used first. */
  vocabulary: string[];
  onChange: (next: string[]) => void;
  /** What the last write failed with, shown under the box; null while all is well. */
  error?: string | null;
  onClose: () => void;
  ariaLabel: string;
}) {
  const { t } = useT();
  const { open, tags, onChange, onClose } = props;
  const [draft, setDraft] = useState("");
  const [active, setActive] = useState(0);
  // Whether the arrow keys have taken over: until they do, Enter commits what
  // is in the box, which is what the box looks like it will do.
  const [picked, setPicked] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) {
      return;
    }
    setDraft("");
    setActive(0);
    setPicked(false);
    inputRef.current?.focus();
  }, [open]);

  const suggestions = useMemo(
    () => (open ? suggestTags(props.vocabulary, draft, tags) : []),
    [open, props.vocabulary, draft, tags],
  );

  useEffect(() => {
    setActive((prev) => (prev < suggestions.length ? prev : 0));
  }, [suggestions.length]);

  if (!open) {
    return null;
  }

  const full = tagsAreFull(tags);
  const typed = normalizeTag(draft);
  const commit = (raw: string) => {
    const next = addTag(tags, raw);
    setDraft("");
    setActive(0);
    setPicked(false);
    // Identity means nothing moved: a duplicate, a full set, or a label that
    // folded to nothing. No request, and no flash of a chip that is not there.
    if (next !== tags) {
      onChange(next);
    }
  };

  const onKeyDown = (ev: React.KeyboardEvent<HTMLInputElement>) => {
    if (ev.key === "Escape") {
      ev.stopPropagation();
      onClose();
      return;
    }
    if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
      if (suggestions.length === 0) {
        return;
      }
      ev.preventDefault();
      const step = ev.key === "ArrowDown" ? 1 : suggestions.length - 1;
      setPicked(true);
      setActive((prev) => (picked ? (prev + step) % suggestions.length : 0));
      return;
    }
    if (ev.key === "Enter") {
      ev.preventDefault();
      // What the arrow keys are pointing at, or else what was typed: a label
      // this history has never seen is as valid as one it has.
      const highlighted = picked ? (suggestions[active] ?? "") : "";
      commit(highlighted || draft);
      return;
    }
    // A backspace on an empty box takes the last chip, the way every other
    // chip field behaves.
    if (ev.key === "Backspace" && draft === "" && tags.length > 0) {
      ev.preventDefault();
      onChange(removeTag(tags, tags[tags.length - 1] as string));
    }
  };

  const anchor = props.anchor;
  const opensUp =
    !!anchor &&
    typeof window !== "undefined" &&
    anchor.bottom + 260 > window.innerHeight;

  const editor = (
    <>
      <button
        type="button"
        className="sessions-filter-backdrop"
        aria-hidden="true"
        tabIndex={-1}
        onMouseDown={(ev) => {
          ev.preventDefault();
          onClose();
        }}
      />
      <div
        className="session-tag-editor"
        role="dialog"
        aria-label={t("sessions.tags.editorAria", { title: props.ariaLabel })}
        data-testid="session-tag-editor"
        onClick={(ev) => ev.stopPropagation()}
        style={
          anchor
            ? {
                right: Math.max(8, window.innerWidth - anchor.right),
                ...(opensUp
                  ? { bottom: window.innerHeight - anchor.top + 6 }
                  : { top: anchor.bottom + 6 }),
              }
            : undefined
        }
      >
        {tags.length > 0 ? (
          <div className="session-tag-editor-chips">
            {tags.map((tag) => (
              <span className="session-tag-chip" key={tag}>
                {tag}
                <button
                  type="button"
                  className="session-tag-chip-remove"
                  aria-label={t("sessions.tags.remove", { tag })}
                  title={t("sessions.tags.remove", { tag })}
                  data-testid={`session-tag-remove-${tag}`}
                  onClick={() => onChange(removeTag(tags, tag))}
                >
                  ×
                </button>
              </span>
            ))}
          </div>
        ) : null}
        <input
          ref={inputRef}
          type="text"
          className="session-tag-input"
          value={draft}
          disabled={full}
          placeholder={
            full
              ? t("sessions.tags.full", { max: MAX_SESSION_TAGS })
              : t("sessions.tags.placeholder")
          }
          aria-label={t("sessions.tags.placeholder")}
          data-testid="session-tag-input"
          onChange={(ev) => {
            setDraft(ev.target.value);
            setPicked(false);
            setActive(0);
          }}
          onKeyDown={onKeyDown}
        />
        {!full && suggestions.length > 0 ? (
          <div className="session-tag-suggest" role="listbox">
            {suggestions.map((tag, i) => (
              <button
                key={tag}
                type="button"
                role="option"
                aria-selected={picked && i === active}
                className={`session-tag-suggest-item${picked && i === active ? " is-active" : ""}`}
                data-testid={`session-tag-suggest-${tag}`}
                onMouseEnter={() => {
                  setPicked(true);
                  setActive(i);
                }}
                onClick={() => commit(tag)}
              >
                {tag}
              </button>
            ))}
          </div>
        ) : null}
        {!full && typed && typed !== draft.trim() ? (
          <p className="session-tag-hint" data-testid="session-tag-preview">
            {t("sessions.tags.addHint", { tag: typed })}
          </p>
        ) : null}
        {props.error ? (
          <p className="session-tag-error" data-testid="session-tag-error">
            {props.error}
          </p>
        ) : null}
      </div>
    </>
  );

  return typeof document === "undefined"
    ? editor
    : createPortal(editor, document.body);
}

import { useEffect, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";

export function ChatHeader(props: {
  title: string;
  editable?: boolean;
  onTitleSave?: (title: string) => void;
}) {
  const { t } = useT();
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(props.title || "");
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (!editing) {
      setValue(props.title || "");
    }
  }, [props.title, editing]);

  useEffect(() => {
    if (editing) {
      inputRef.current?.focus();
      inputRef.current?.select();
    }
  }, [editing]);

  const canEdit = !!props.editable && !!props.onTitleSave;

  return (
    <header className="chat-header">
      <div className="chat-title" id="chat-title">
        {canEdit && editing ? (
          <input
            ref={inputRef}
            className="chat-title-input"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onBlur={() => {
              setEditing(false);
              const t = value.trim();
              if (t && t !== (props.title || "").trim()) {
                props.onTitleSave?.(t);
              }
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                (e.target as HTMLInputElement).blur();
              }
              if (e.key === "Escape") {
                setValue(props.title || "");
                setEditing(false);
              }
            }}
          />
        ) : (
          <button
            type="button"
            className={`chat-title-btn ${canEdit ? "is-editable" : ""}`}
            onClick={() => {
              if (canEdit) {
                setEditing(true);
              }
            }}
            aria-label={t("chat.chatTitleAriaLabel")}
          >
            {props.title || t("chat.newChat")}
          </button>
        )}
      </div>
    </header>
  );
}

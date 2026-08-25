import { useT } from "../i18n/I18nProvider";

export function TypingDotsMessage() {
  const { t } = useT();
  return (
    <div className="msg-assistant-stack" data-testid="typing-dots">
      <div className="typing-dots" aria-label={t("messages.preparingResponse")} aria-live="polite">
        <span className="typing-dots-dot" aria-hidden="true" />
        <span className="typing-dots-dot" aria-hidden="true" />
        <span className="typing-dots-dot" aria-hidden="true" />
      </div>
    </div>
  );
}

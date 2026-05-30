export function TypingDotsMessage() {
  return (
    <div className="msg-assistant-stack" data-testid="typing-dots">
      <div className="typing-dots" aria-label="Preparing response" aria-live="polite">
        <span className="typing-dots-dot" aria-hidden="true" />
        <span className="typing-dots-dot" aria-hidden="true" />
        <span className="typing-dots-dot" aria-hidden="true" />
      </div>
    </div>
  );
}

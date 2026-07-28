import { Markdown } from "../markdown/Markdown";

// CompactionMessage renders a compaction summary row as a foldout ("what is now
// in the context") styled like the thinking / tool disclosure, so it reads as a
// system action rather than a user message. Backed by transcript items of type
// "compaction" (server messages with compaction_summary=true).
export function CompactionMessage(props: { summary: string }) {
  const text = (props.summary || "").trim();
  return (
    <div className="thinking-row">
      <details className="thinking-details">
        <summary
          className="thinking-summary"
          aria-label="Context compacted summary"
        >
          <span className="thinking-left">
            <span className="thinking-chevron" aria-hidden="true" />
            <span className="thinking-label">context compacted</span>
          </span>
        </summary>
        {text ? (
          <div className="thinking-body" aria-label="Compacted context summary">
            <Markdown text={text} />
          </div>
        ) : null}
      </details>
    </div>
  );
}

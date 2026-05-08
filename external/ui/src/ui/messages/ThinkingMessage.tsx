import { useMemo } from 'react';
import { Markdown } from '../markdown/Markdown';

function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '';
  if (ms >= 60_000) {
    const mins = ms / 60_000;
    const fixed = mins < 10 ? mins.toFixed(1) : mins.toFixed(0);
    return `${fixed}m`;
  }
  return `${Math.round(ms)}ms`;
}

export function ThinkingMessage(props: { status: 'in_progress' | 'completed'; content: string; durationMs?: number }) {
  const label = props.status === 'completed' ? 'thinking' : 'thinking...';
  const dur = useMemo(() => (typeof props.durationMs === 'number' ? formatDuration(props.durationMs) : ''), [props.durationMs]);
  const text = (props.content || '').trim();
  const openByDefault = props.status !== 'completed';

  return (
    <div className="thinking-row">
      <details className="thinking-details" open={openByDefault ? true : undefined}>
        <summary className="thinking-summary" aria-label="Thinking summary">
          <span className="thinking-left">
            <span className="thinking-chevron" aria-hidden="true" />
            <span className="thinking-label">{label}</span>
          </span>
          {props.status === 'in_progress' ? <span className="thinking-spinner" aria-hidden="true" /> : null}
          {dur ? (
            <span className="thinking-dur" aria-hidden="true">
              {dur}
            </span>
          ) : null}
        </summary>
        {text ? (
          <div className="thinking-body" aria-label="Thinking content">
            <Markdown text={text} />
          </div>
        ) : null}
      </details>
    </div>
  );
}

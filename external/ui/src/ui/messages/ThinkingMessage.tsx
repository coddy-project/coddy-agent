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
  const label = props.status === 'completed' ? 'decision' : 'thinking...';
  const dur = useMemo(() => (typeof props.durationMs === 'number' ? formatDuration(props.durationMs) : ''), [props.durationMs]);
  const text = (props.content || '').trim();
  const dotStatus = props.status === 'completed' ? 'completed' : 'in_progress';

  return (
    <div className="msg msg-tools msg-thinking" data-status={dotStatus}>
      <details className="tool-details" open={props.status !== 'completed' ? true : undefined}>
        <summary className="tool-summary" aria-label="Thinking summary" title="Click to view details">
          <span className={`tool-dot tool-dot-${dotStatus}`} aria-hidden="true" />
          {props.status !== 'completed' ? <span className="tool-spinner" aria-hidden="true" /> : null}
          <span className="tool-name">{label}</span>
          {dur ? (
            <span className="tool-dur" aria-hidden="true">
              {dur}
            </span>
          ) : null}
        </summary>
        {text ? (
          <div className="tool-block tool-result" aria-label="Thinking content">
            <Markdown text={text} />
          </div>
        ) : null}
      </details>
    </div>
  );
}


import { useMemo } from 'react';
import { Markdown } from '../markdown/Markdown';

function safePrettyJSON(text: string): string {
  try {
    const v = JSON.parse(text);
    return JSON.stringify(v, null, 2);
  } catch {
    return text;
  }
}

function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '';
  if (ms >= 60_000) {
    const mins = ms / 60_000;
    const fixed = mins < 10 ? mins.toFixed(1) : mins.toFixed(0);
    return `${fixed}m`;
  }
  return `${Math.round(ms)}ms`;
}

export function ToolCallMessage(props: {
  toolCallId: string;
  title?: string | undefined;
  kind?: string | undefined;
  status: string;
  argsText?: string | undefined;
  resultText?: string | undefined;
  detailsLoaded?: boolean;
  durationMs?: number;
  onLoadDetails?: (toolCallId: string) => void;
}) {
  const args = useMemo(() => (props.argsText ? safePrettyJSON(props.argsText) : ''), [props.argsText]);
  const result = useMemo(() => (props.resultText ? props.resultText : ''), [props.resultText]);
  const name = (props.title || props.kind || 'tool').trim();
  const status = (props.status || '').toLowerCase();
  const showSpinner = status === 'pending' || status === 'in_progress';
  const canLoad = !!props.onLoadDetails && !props.detailsLoaded;
  const dur =
    typeof props.durationMs === 'number' && Number.isFinite(props.durationMs) && props.durationMs >= 0
      ? formatDuration(props.durationMs)
      : '';

  return (
    <div className="msg msg-tools msg-compact" data-kind={props.kind || ''} data-status={props.status}>
      <details
        className="tool-details"
        onToggle={(e) => {
          const el = e.currentTarget;
          if (!el.open) return;
          if (!canLoad) return;
          props.onLoadDetails?.(props.toolCallId);
        }}
      >
        <summary className="tool-summary" aria-label="Tool summary" title="Click to view details">
          <span className="tool-left">
            <span className={`tool-dot tool-dot-${status || 'unknown'}`} aria-hidden="true" />
            {showSpinner ? <span className="tool-spinner" aria-hidden="true" /> : null}
            <span className="tool-name">{name}</span>
          </span>
          {dur ? (
            <span className="tool-dur" aria-hidden="true">
              {dur}
            </span>
          ) : null}
        </summary>
        {args ? (
          <pre className="tool-block" aria-label="Tool arguments">
            {args}
          </pre>
        ) : null}
        {result ? (
          <div className="tool-block tool-result" aria-label="Tool result">
            <Markdown text={result} />
          </div>
        ) : null}
      </details>
    </div>
  );
}

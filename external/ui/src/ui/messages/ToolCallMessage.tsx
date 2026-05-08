import { useMemo } from 'react';

function safePrettyJSON(text: string): string {
  try {
    const v = JSON.parse(text);
    return JSON.stringify(v, null, 2);
  } catch {
    return text;
  }
}

export function ToolCallMessage(props: {
  title?: string | undefined;
  kind?: string | undefined;
  status: string;
  argsText?: string | undefined;
  resultText?: string | undefined;
}) {
  const args = useMemo(() => (props.argsText ? safePrettyJSON(props.argsText) : ''), [props.argsText]);
  const result = useMemo(() => (props.resultText ? props.resultText : ''), [props.resultText]);
  const name = (props.title || props.kind || 'tool').trim();

  return (
    <div className="msg msg-tools" data-kind={props.kind || ''} data-status={props.status}>
      <div className="tool-head">
        <span className="tool-name">{name}</span>
        <span className="tool-status">{props.status}</span>
      </div>
      {args ? (
        <pre className="tool-block" aria-label="Tool arguments">
          {args}
        </pre>
      ) : null}
      {result ? (
        <pre className="tool-block" aria-label="Tool result">
          {result}
        </pre>
      ) : null}
    </div>
  );
}

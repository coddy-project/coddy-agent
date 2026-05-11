import { useMemo, useRef } from "react";

export function MarkdownLineEditor(props: {
  value: string;
  onChange: (v: string) => void;
  disabled?: boolean;
  "aria-label"?: string;
  placeholder?: string;
}) {
  const taRef = useRef<HTMLTextAreaElement>(null);
  const gutterRef = useRef<HTMLPreElement>(null);

  const gutterText = useMemo(() => {
    const n = props.value === "" ? 1 : props.value.split("\n").length;
    return Array.from({ length: n }, (_, i) => String(i + 1)).join("\n");
  }, [props.value]);

  const onScroll = () => {
    const ta = taRef.current;
    const g = gutterRef.current;
    if (ta && g) {
      g.scrollTop = ta.scrollTop;
    }
  };

  return (
    <div className="md-line-editor">
      <pre
        ref={gutterRef}
        className="md-line-editor-gutter"
        aria-hidden
      >
        {gutterText}
      </pre>
      <textarea
        ref={taRef}
        className="md-line-editor-textarea"
        value={props.value}
        disabled={props.disabled}
        spellCheck={false}
        placeholder={props.placeholder}
        aria-label={props["aria-label"]}
        onChange={(ev) => props.onChange(ev.target.value)}
        onScroll={onScroll}
      />
    </div>
  );
}

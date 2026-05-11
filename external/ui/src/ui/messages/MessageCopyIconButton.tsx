import { useCallback } from "react";

export function MessageCopyIconButton(props: {
  textToCopy: string;
  tooltip: string;
  ariaLabel: string;
  dataTestId: string;
}) {
  const onCopy = useCallback(async () => {
    const text = props.textToCopy;
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      try {
        const ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.focus();
        ta.select();
        document.execCommand("copy");
        document.body.removeChild(ta);
      } catch {
        /* ignore */
      }
    }
  }, [props.textToCopy]);

  const hasText = props.textToCopy.trim().length > 0;

  return (
    <button
      type="button"
      className="msg-copy-icon-btn"
      aria-label={props.ariaLabel}
      data-testid={props.dataTestId}
      disabled={!hasText}
      title={hasText ? props.tooltip : undefined}
      onClick={() => void onCopy()}
    >
      <svg
        className="msg-copy-icon-btn__glyph"
        width="16"
        height="16"
        viewBox="0 0 16 16"
        fill="none"
        xmlns="http://www.w3.org/2000/svg"
        aria-hidden
      >
        <path
          fill="currentColor"
          d="M4 2.5A1.5 1.5 0 015.5 1h6A1.5 1.5 0 0113 2.5v8a1.5 1.5 0 01-1.5 1.5H10v.5A1.5 1.5 0 018.5 14h-6A1.5 1.5 0 011 12.5v-8A1.5 1.5 0 012.5 3H4v-.5zm1 0V3h4A1.5 1.5 0 0110.5 4.5v7H11v-8h-6v-.5zM2.5 4a.5.5 0 00-.5.5v8a.5.5 0 00.5.5h6a.5.5 0 00.5-.5v-8a.5.5 0 00-.5-.5h-6z"
        />
      </svg>
    </button>
  );
}

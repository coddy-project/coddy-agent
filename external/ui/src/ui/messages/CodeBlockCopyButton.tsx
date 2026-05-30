import { useCallback, useState } from "react";

async function copyTextToClipboard(text: string): Promise<void> {
  await navigator.clipboard.writeText(text);
}

/** Pill "Copy" control for fenced code blocks (matches Markdown `md-copy`). */
export function CodeBlockCopyButton(props: {
  textToCopy: string;
  dataTestId?: string;
}) {
  const [copied, setCopied] = useState(false);

  const onCopy = useCallback(async () => {
    const text = props.textToCopy.trim();
    if (!text) {
      return;
    }
    try {
      await copyTextToClipboard(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 900);
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
        setCopied(true);
        window.setTimeout(() => setCopied(false), 900);
      } catch {
        setCopied(false);
      }
    }
  }, [props.textToCopy]);

  const disabled = props.textToCopy.trim().length === 0;

  return (
    <button
      type="button"
      className="md-copy"
      data-testid={props.dataTestId}
      disabled={disabled}
      onClick={() => void onCopy()}
      aria-label="Copy code"
    >
      {copied ? "Copied" : "Copy"}
    </button>
  );
}

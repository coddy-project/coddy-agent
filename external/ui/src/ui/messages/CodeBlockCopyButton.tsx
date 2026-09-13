import { useCallback, useState } from "react";
import { useT } from "../i18n/I18nProvider";

async function copyTextToClipboard(text: string): Promise<void> {
  await navigator.clipboard.writeText(text);
}

/** Clipboard glyph for the copy controls that used to spell the word out. */
export function CopyGlyph() {
  return (
    <svg
      className="md-copy__glyph"
      width="14"
      height="14"
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
  );
}

/** Icon copy control for code blocks and tool previews (shares `md-copy` chrome).
 *  The word it used to show survives as the hover tooltip and the accessible name. */
export function CodeBlockCopyButton(props: {
  textToCopy: string;
  dataTestId?: string;
}) {
  const { t } = useT();
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
      title={copied ? t("messages.copied") : t("messages.copy")}
      aria-label={t("messages.copyCode")}
    >
      <CopyGlyph />
    </button>
  );
}

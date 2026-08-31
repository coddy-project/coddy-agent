import { useCallback, useRef, useState, type RefObject } from "react";
import { useT } from "../i18n/I18nProvider";

/**
 * The prompt-improvement request: in-flight flag, error line, and the saved
 * pre-enhance draft that a single Ctrl+Z restores.
 */
export function useEnhancePrompt({
  value,
  onChange,
  sessionId,
  generating,
  taRef,
}: {
  value: string;
  onChange: (v: string) => void;
  sessionId: string;
  generating: boolean;
  taRef: RefObject<HTMLTextAreaElement | null>;
}): {
  enhancing: boolean;
  enhanceErr: string | null;
  /** Typing invalidates the undo snapshot and clears the error line. */
  clearEnhanceState: () => void;
  enhancePrompt: () => Promise<void>;
  /** Returns the pre-enhance draft once (null when there is nothing to undo). */
  restorePreEnhanceDraft: () => string | null;
} {
  const { t } = useT();
  /** True while the prompt-improvement request is in flight. */
  const [enhancing, setEnhancing] = useState(false);
  /** Request failure shown without changing the user's draft. */
  const [enhanceErr, setEnhanceErr] = useState<string | null>(null);
  /** Draft saved just before improving it so Ctrl+Z can restore it once. */
  const preEnhanceRef = useRef<string | null>(null);

  const clearEnhanceState = useCallback(() => {
    preEnhanceRef.current = null;
    setEnhanceErr(null);
  }, []);

  const restorePreEnhanceDraft = useCallback((): string | null => {
    const restored = preEnhanceRef.current;
    preEnhanceRef.current = null;
    return restored;
  }, []);

  const enhancePrompt = useCallback(async () => {
    if (enhancing || generating) {
      return;
    }
    const draft = value.trim();
    if (!draft) {
      return;
    }
    preEnhanceRef.current = value;
    setEnhancing(true);
    setEnhanceErr(null);
    try {
      const headers: Record<string, string> = {
        "Content-Type": "application/json",
      };
      const sessionID = sessionId.trim();
      if (sessionID) {
        headers["X-Coddy-Session-ID"] = sessionID;
      }
      const response = await fetch("/coddy/enhance-prompt", {
        method: "POST",
        headers,
        body: JSON.stringify({ text: draft }),
      });
      if (!response.ok) {
        setEnhanceErr(
          response.status === 503
            ? t("composer.enhanceNoModel")
            : t("composer.enhanceFailed"),
        );
        preEnhanceRef.current = null;
        return;
      }
      const body = (await response.json()) as { text?: string };
      const enhanced = (body.text || "").trim();
      if (enhanced) {
        onChange(enhanced);
      } else {
        preEnhanceRef.current = null;
      }
    } catch {
      preEnhanceRef.current = null;
      setEnhanceErr(t("composer.enhanceFailed"));
    } finally {
      setEnhancing(false);
      requestAnimationFrame(() => taRef.current?.focus());
    }
  }, [enhancing, generating, value, sessionId, onChange, t, taRef]);

  return {
    enhancing,
    enhanceErr,
    clearEnhanceState,
    enhancePrompt,
    restorePreEnhanceDraft,
  };
}

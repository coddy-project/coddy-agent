import React, {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
} from "react";
import { ConfirmDialog, type ConfirmDialogVariant } from "./ConfirmDialog";

export type ConfirmOptions = {
  /** Short question shown as the dialog heading, e.g. "Delete chat?". */
  title: string;
  /** Longer explanatory body. Optional. */
  message?: string;
  /** Label on the affirmative action button. */
  confirmLabel: string;
  /** Label on the dismissal button. Defaults to "Cancel". */
  cancelLabel?: string;
  /** danger = red destructive action; primary = accent-coloured action. */
  variant?: ConfirmDialogVariant;
};

export type ConfirmFn = (options: ConfirmOptions) => Promise<boolean>;

const ConfirmContext = createContext<ConfirmFn | null>(null);

type PendingState = {
  options: ConfirmOptions;
  resolve: (ok: boolean) => void;
};

// useConfirm returns a promise-based confirm() so call sites can replace a
// synchronous `window.confirm(...)` with an async `await confirm({...})` while
// keeping their control flow intact. Mount <ConfirmProvider> once near the root
// (see main.tsx); every component below it shares the single dialog instance.
export function useConfirm(): ConfirmFn {
  const ctx = useContext(ConfirmContext);
  if (!ctx) {
    throw new Error(
      "useConfirm must be used within a <ConfirmProvider> tree.",
    );
  }
  return ctx;
}

export function ConfirmProvider({ children }: { children: React.ReactNode }) {
  const [pending, setPending] = useState<PendingState | null>(null);

  // Keep the latest resolver around so cancel/confirm handlers always close the
  // correct promise, even if state batching delays the setPending(null).
  const resolveRef = useRef<((ok: boolean) => void) | null>(null);

  const confirm = useCallback<ConfirmFn>((options) => {
    return new Promise<boolean>((resolve) => {
      resolveRef.current = resolve;
      setPending({ options, resolve });
    });
  }, []);

  const settle = useCallback(
    (ok: boolean) => {
      const r = resolveRef.current;
      resolveRef.current = null;
      setPending(null);
      if (r) {
        r(ok);
      }
    },
    [],
  );

  const onConfirm = useCallback(() => settle(true), [settle]);
  const onCancel = useCallback(() => settle(false), [settle]);

  const value = useMemo<ConfirmFn>(() => confirm, [confirm]);

  return (
    <ConfirmContext.Provider value={value}>
      {children}
      <ConfirmDialog
        open={!!pending}
        title={pending?.options.title ?? ""}
        message={pending?.options.message}
        confirmLabel={pending?.options.confirmLabel ?? "Confirm"}
        cancelLabel={pending?.options.cancelLabel}
        variant={pending?.options.variant}
        ariaLabel={pending?.options.title ?? "Confirm action"}
        onConfirm={onConfirm}
        onCancel={onCancel}
        dataTestId="app-confirm-dialog"
      />
    </ConfirmContext.Provider>
  );
}

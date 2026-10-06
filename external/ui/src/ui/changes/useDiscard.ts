import { useCallback, useRef, useState } from "react";
import { useConfirm } from "../components/useConfirm";
import { useT } from "../i18n/I18nProvider";
import { discardSessionChanges, type DiscardSelection } from "./api";
import { baseName } from "./sessionChangesText";
import type { ChangeStatus } from "./types";
import { refreshWorkingCopy } from "./workingCopy";

/**
 * Discarding uncommitted changes from the Edits views: a file, or all of them.
 * Each asks first - git puts tracked files back and deletes new ones, and
 * nothing brings them back - and the working copy is read again afterwards,
 * whatever the answer, since a failure halfway may have moved some files.
 */
export function useDiscard(sessionId: string): {
  busy: boolean;
  error: string;
  discardFile: (path: string, status: ChangeStatus) => Promise<void>;
  discardAll: () => Promise<void>;
} {
  const { t } = useT();
  const confirm = useConfirm();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // One question at a time: a second press while one is open asks nothing.
  const asking = useRef(false);

  const ask = useCallback(
    async (options: Parameters<typeof confirm>[0]) => {
      if (asking.current) {
        return false;
      }
      asking.current = true;
      try {
        return await confirm(options);
      } finally {
        asking.current = false;
      }
    },
    [confirm],
  );

  const run = useCallback(
    async (selection: DiscardSelection) => {
      const sid = sessionId.trim();
      if (!sid) {
        return;
      }
      setBusy(true);
      setError("");
      const res = await discardSessionChanges(sid, selection);
      setBusy(false);
      if (!res.ok) {
        setError(t("changes.discardFailed", { message: res.message }));
      }
      refreshWorkingCopy(sid);
    },
    [sessionId, t],
  );

  const discardFile = useCallback(
    async (path: string, status: ChangeStatus) => {
      const ok = await ask({
        title: t("changes.discardFileConfirm", { name: baseName(path) }),
        // A file HEAD does not hold is deleted; any other goes back to HEAD.
        message: t(
          status === "added"
            ? "changes.discardDeleteMessage"
            : "changes.discardRestoreMessage",
        ),
        confirmLabel: t("changes.discardYes"),
        variant: "danger",
      });
      if (ok) {
        await run({ paths: [path] });
      }
    },
    [ask, run, t],
  );

  const discardAll = useCallback(async () => {
    const ok = await ask({
      title: t("changes.discardAllConfirm"),
      message: t("changes.discardAllMessage"),
      confirmLabel: t("changes.discardYes"),
      variant: "danger",
    });
    if (ok) {
      await run({ all: true });
    }
  }, [ask, run, t]);

  return { busy, error, discardFile, discardAll };
}

import { useEffect, useState } from "react";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { useT } from "../i18n/I18nProvider";
import { fetchSessionWorktree, type SessionWorktree } from "./worktreeApi";

/** What the chat answers the dialog with: back and keep, or back and remove. */
export type LeaveWorktreeChoice = "keep" | "remove" | "discard";

/** The command each choice sends; the server runs it without a model turn. */
export function leaveWorktreeCommand(choice: LeaveWorktreeChoice): string {
  switch (choice) {
    case "remove":
      return "/worktree exit --remove";
    case "discard":
      return "/worktree exit --remove --force";
    default:
      return "/worktree exit";
  }
}

type Reading =
  | { state: "loading" }
  | { state: "failed"; message: string }
  | { state: "ready"; worktree: SessionWorktree };

/**
 * Leaving a worktree from the plate over the composer: the question Claude
 * Code asks when a worktree session ends, keep the worktree or remove it,
 * asked with what removing would touch in view - uncommitted changes (removed
 * only by discarding them, which keeps them as a snapshot), commits not pushed
 * (the branch stays unless merged), and whatever stops a removal now (another
 * chat in the worktree, a running task, a lock, loose commits, submodules, a
 * worktree Coddy did not open). Keeping is the default answer and holds the
 * focus; GET /coddy/sessions/{id}/worktree is read each time the dialog opens.
 */
export function LeaveWorktreeDialog(props: {
  open: boolean;
  sessionId: string;
  /** The main checkout's name, as the plate shows it. */
  project: string;
  onChoose: (choice: LeaveWorktreeChoice) => void;
  onClose: () => void;
}) {
  const { t, tp } = useT();
  const [reading, setReading] = useState<Reading>({ state: "loading" });
  const { open, sessionId } = props;

  useEffect(() => {
    if (!open || !sessionId) return;
    let live = true;
    setReading({ state: "loading" });
    void fetchSessionWorktree(sessionId).then((res) => {
      if (!live) return;
      setReading(
        res.ok
          ? { state: "ready", worktree: res.data }
          : { state: "failed", message: res.message },
      );
    });
    return () => {
      live = false;
    };
  }, [open, sessionId]);

  const wt = reading.state === "ready" ? reading.worktree : null;
  const facts: { key: string; text: string; tone?: "warn" | "stop" }[] = [];
  if (reading.state === "loading") {
    facts.push({ key: "loading", text: t("leaveWorktree.checking") });
  } else if (reading.state === "failed") {
    facts.push({
      key: "failed",
      text: t("leaveWorktree.readFailed", { message: reading.message }),
      tone: "stop",
    });
  } else if (wt) {
    facts.push({
      key: "branch",
      text: wt.branch
        ? t("leaveWorktree.branch", { branch: wt.branch })
        : t("leaveWorktree.detached"),
    });
    facts.push(
      wt.uncommitted > 0
        ? {
            key: "uncommitted",
            text: tp("leaveWorktree.uncommitted", wt.uncommitted),
            tone: "warn",
          }
        : { key: "clean", text: t("leaveWorktree.clean") },
    );
    if (wt.unpushed > 0) {
      facts.push({
        key: "unpushed",
        text: tp("leaveWorktree.unpushed", wt.unpushed),
      });
    }
    const others = wt.other_sessions ?? [];
    if (others.length > 0) {
      facts.push({
        key: "others",
        text: t("leaveWorktree.otherChats", {
          names: others.map((o) => o.title || o.id).join(", "),
        }),
        tone: "stop",
      });
    }
    if (wt.running_tasks > 0) {
      facts.push({
        key: "tasks",
        text: tp("leaveWorktree.tasks", wt.running_tasks),
        tone: "stop",
      });
    }
    if (wt.locked) {
      facts.push({
        key: "locked",
        text: t("leaveWorktree.locked"),
        tone: "stop",
      });
    }
    if (wt.loose_commit) {
      facts.push({
        key: "loose",
        text: t("leaveWorktree.loose"),
        tone: "stop",
      });
    }
    if (wt.submodules) {
      facts.push({
        key: "submodules",
        text: t("leaveWorktree.submodules"),
        tone: "stop",
      });
    }
    if (!wt.managed) {
      facts.push({
        key: "unmanaged",
        text: t("leaveWorktree.unmanaged"),
        tone: "stop",
      });
    }
    if (!wt.removable && !facts.some((f) => f.tone === "stop")) {
      facts.push({
        key: "refused",
        text: t("leaveWorktree.cannotRemove"),
        tone: "stop",
      });
    }
  }
  const removable = !!wt?.removable;
  const discard = removable && !!wt?.needs_discard;

  return (
    <ConfirmDialog
      open={props.open}
      title={t("leaveWorktree.title")}
      message={t("leaveWorktree.message", { name: props.project })}
      ariaLabel={t("leaveWorktree.title")}
      confirmLabel={t("leaveWorktree.keep")}
      variant="primary"
      initialFocus="confirm"
      onConfirm={() => props.onChoose("keep")}
      onCancel={props.onClose}
      dataTestId="leave-worktree-dialog"
      wide
      secondary={{
        label: discard
          ? t("leaveWorktree.discardAndRemove")
          : t("leaveWorktree.remove"),
        variant: "danger",
        disabled: !removable,
        dataTestId: "leave-worktree-remove",
        onClick: () => props.onChoose(discard ? "discard" : "remove"),
      }}
    >
      <ul className="leave-worktree-facts" data-testid="leave-worktree-facts">
        {facts.map((f) => (
          <li
            key={f.key}
            className={
              f.tone
                ? `leave-worktree-fact is-${f.tone}`
                : "leave-worktree-fact"
            }
            data-fact={f.key}
          >
            {f.text}
          </li>
        ))}
      </ul>
      {discard ? (
        <p
          className="leave-worktree-note"
          data-testid="leave-worktree-snapshot"
        >
          {t("leaveWorktree.snapshotNote")}
        </p>
      ) : null}
    </ConfirmDialog>
  );
}

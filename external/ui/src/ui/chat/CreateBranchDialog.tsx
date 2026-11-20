import React, { useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { GitDialog } from "./GitDialog";

export function CreateBranchDialog(props: {
  onCreate: (name: string) => Promise<void>;
  onClose: () => void;
}) {
  const { t } = useT();
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (!name.trim() || busy) return;
    setBusy(true);
    setError("");
    try {
      await props.onCreate(name.trim());
      props.onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("git.createFailed"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <GitDialog title={t("git.createTitle")} busy={busy} onClose={props.onClose}>
      <form
        onSubmit={(event) => void submit(event)}
        className="git-create-form"
      >
        <p className="git-dialog-description">{t("git.createDescription")}</p>
        <label htmlFor="git-branch-name">{t("git.branchName")}</label>
        <input
          id="git-branch-name"
          value={name}
          disabled={busy}
          placeholder={t("git.branchPlaceholder")}
          autoComplete="off"
          spellCheck={false}
          aria-describedby="git-branch-hint"
          aria-invalid={!!error}
          onChange={(event) => {
            setName(event.target.value);
            setError("");
          }}
        />
        <p id="git-branch-hint" className="git-dialog-description">
          {t("git.headOnly")}
        </p>
        {error ? (
          <p className="git-dialog-error" role="alert">
            {error}
          </p>
        ) : null}
        <footer className="git-dialog-actions">
          <button
            type="button"
            className="workspace-modal-btn"
            disabled={busy}
            onClick={props.onClose}
          >
            {t("common.cancel")}
          </button>
          <button
            type="submit"
            className="workspace-modal-btn workspace-modal-btn--primary"
            disabled={busy || !name.trim()}
          >
            {t(busy ? "git.creating" : "git.createAction")}
          </button>
        </footer>
      </form>
    </GitDialog>
  );
}

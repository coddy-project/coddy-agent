import { useRef, useState, useSyncExternalStore } from "react";
import { useT } from "../i18n/I18nProvider";
import { useConfirm } from "../components/useConfirm";
import { extensionRequest, useExtensions } from "./ExtensionsProvider";
import type { ExtensionEntry } from "./runtime";
import { IconTrash } from "../settings/SchemaForm";

export function ExtensionsSection() {
  const context = useExtensions();
  return context ? <ExtensionList context={context} /> : null;
}

function ExtensionList({
  context,
}: {
  context: NonNullable<ReturnType<typeof useExtensions>>;
}) {
  const { t } = useT();
  const confirm = useConfirm();
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const snapshot = useSyncExternalStore(
    context.runtime.subscribe,
    context.runtime.snapshot,
  );
  const perform = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError("");
    try {
      await fn();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };
  const toggle = async (entry: ExtensionEntry) => {
    if (
      !(await confirm({
        title: t(
          entry.enabled ? "extensions.disableTitle" : "extensions.enableTitle",
          { name: entry.name },
        ),
        message: t(entry.enabled ? "extensions.reloadHint" : "extensions.risk"),
        confirmLabel: t(
          entry.enabled ? "extensions.disable" : "extensions.accept",
        ),
        variant: "danger",
      }))
    )
      return;
    await perform(async () => {
      await extensionRequest(`/${entry.id}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          sha256: entry.sha256,
          enabled: !entry.enabled,
          accept_risk: !entry.enabled,
        }),
      });
      window.location.reload();
    });
  };
  const remove = async (entry: ExtensionEntry) => {
    if (
      !(await confirm({
        title: t("extensions.removeTitle", { name: entry.name }),
        message: t("extensions.reloadHint"),
        confirmLabel: t("extensions.remove"),
        variant: "danger",
      }))
    )
      return;
    await perform(async () => {
      await extensionRequest(`/${entry.id}`, { method: "DELETE" });
      window.location.reload();
    });
  };
  return (
    <div className="extensions-section">
      <p className="extensions-risk">{t("extensions.risk")}</p>
      <div className="extensions-actions">
        <button
          type="button"
          className="settings-btn"
          disabled={busy}
          onClick={() => input.current?.click()}
        >
          {t("extensions.install")}
        </button>
        <button
          type="button"
          className="settings-btn"
          disabled={busy}
          onClick={() => void context.refresh(true)}
        >
          {t("extensions.refresh")}
        </button>
        <input
          ref={input}
          type="file"
          hidden
          accept=".zip,application/zip"
          aria-label={t("extensions.install")}
          onChange={(e) => {
            const file = e.target.files?.[0];
            e.target.value = "";
            if (!file) return;
            void perform(async () => {
              if (file.size > 8 * 1024 * 1024)
                throw new Error(t("extensions.tooLarge"));
              await extensionRequest("", {
                method: "POST",
                headers: { "Content-Type": "application/zip" },
                body: file,
              });
              await context.refresh();
            });
          }}
        />
      </div>
      {error || context.error ? (
        <p role="alert">{error || context.error}</p>
      ) : null}
      {!context.error && context.entries.length === 0 ? (
        <p className="settings-muted">{t("extensions.empty")}</p>
      ) : null}
      {context.entries.map((entry) => (
        <article
          key={entry.id}
          className={`extension-card${entry.enabled ? "" : " is-disabled"}`}
        >
          <button
            type="button"
            role="switch"
            aria-checked={entry.enabled}
            className="skill-switch"
            disabled={busy}
            onClick={() => void toggle(entry)}
            title={t(
              entry.enabled
                ? "extensions.switch.enabledTitle"
                : "extensions.switch.disabledTitle",
            )}
            aria-label={t(
              entry.enabled
                ? "extensions.switch.disableAria"
                : "extensions.switch.enableAria",
              { name: entry.name },
            )}
            data-testid={`extensions-toggle-${entry.id}`}
          >
            <span className="skill-switch-thumb" />
          </button>
          <div className="extension-card-text">
            <div className="extension-card-name">
              {entry.name}
              <span className="extension-card-version">v{entry.version}</span>
            </div>
            {entry.description ? (
              <div className="extension-card-desc">{entry.description}</div>
            ) : null}
            {snapshot.errors[entry.id] ? (
              <p role="alert" className="extension-card-error">
                {snapshot.errors[entry.id]}
              </p>
            ) : null}
          </div>
          <button
            type="button"
            className="settings-btn settings-btn-icon settings-btn-danger"
            disabled={busy}
            onClick={() => void remove(entry)}
            title={t("extensions.remove")}
            aria-label={t("extensions.removeAria", { name: entry.name })}
            data-testid={`extensions-remove-${entry.id}`}
          >
            <IconTrash />
          </button>
        </article>
      ))}
    </div>
  );
}

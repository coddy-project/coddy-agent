import { useT } from "../i18n/I18nProvider";
import type { MCPScope } from "./mcpServerJson";

/** Editor state: null = closed; name empty = adding a new server. */
export type MCPEditorState = {
  name: string;
  text: string;
  isNew: boolean;
  scope: MCPScope;
};

/** Cursor-style JSON editor card for one .coddy/mcp.json entry. */
export function MCPEditorCard(props: {
  editor: MCPEditorState;
  error: string | null;
  busy: boolean;
  onChange: (next: MCPEditorState) => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  const { editor, error, busy, onChange, onSave, onCancel } = props;
  const { t } = useT();
  return (
    <div className="mcp-editor" data-testid="mcp-editor">
      {editor.isNew ? (
        <input
          className="settings-input"
          type="text"
          placeholder={t("mcp.editor.namePlaceholder")}
          value={editor.name}
          onChange={(e) => onChange({ ...editor, name: e.target.value })}
          aria-label={t("mcp.editor.nameAria")}
          data-testid="mcp-editor-name"
        />
      ) : null}
      {editor.isNew ? (
        <div
          className="mcp-editor-scope"
          role="radiogroup"
          aria-label={t("mcp.editor.scopeAria")}
        >
          <label className="mcp-editor-scope-option">
            <input
              type="radio"
              name="mcp-editor-scope"
              checked={editor.scope === "local"}
              onChange={() => onChange({ ...editor, scope: "local" })}
              data-testid="mcp-editor-scope-local"
            />
            <span>{t("mcp.editor.scopeLocal")}</span>
          </label>
          <label className="mcp-editor-scope-option">
            <input
              type="radio"
              name="mcp-editor-scope"
              checked={editor.scope === "global"}
              onChange={() => onChange({ ...editor, scope: "global" })}
              data-testid="mcp-editor-scope-global"
            />
            <span>{t("mcp.editor.scopeGlobal")}</span>
          </label>
        </div>
      ) : null}
      <textarea
        className="settings-input mcp-editor-json"
        rows={8}
        spellCheck={false}
        value={editor.text}
        onChange={(e) => onChange({ ...editor, text: e.target.value })}
        aria-label={t("mcp.editor.jsonAria")}
        data-testid="mcp-editor-json"
      />
      <p className="settings-field-desc">
        {t("mcp.editor.formatDescription", {
          path:
            editor.scope === "global"
              ? "~/.coddy/mcp.json"
              : "./.coddy/mcp.json",
        })}
      </p>
      {error ? <p className="settings-error">{error}</p> : null}
      <div className="mcp-editor-actions">
        <button
          type="button"
          className="settings-btn settings-btn-primary"
          disabled={busy}
          onClick={onSave}
          data-testid="mcp-editor-save"
        >
          {t("mcp.editor.save")}
        </button>
        <button
          type="button"
          className="settings-btn"
          disabled={busy}
          onClick={onCancel}
        >
          {t("mcp.editor.cancel")}
        </button>
      </div>
    </div>
  );
}

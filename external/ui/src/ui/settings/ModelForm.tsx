import { useState, useEffect, useCallback } from "react";
import type React from "react";
import {
  listModels,
  createModel,
  updateModel,
  deleteModel,
  listProviders,
  type AdminModel,
  type AdminProvider,
} from "./api";

export interface ModelFormProps {
  onRefresh?: () => void;
}

const listContainerStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "8px",
};

const rowStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  justifyContent: "space-between",
  padding: "8px 12px",
  background: "var(--bubble-user)",
  borderRadius: "8px",
};

const rowTextStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "2px",
  minWidth: 0,
};

const rowActionsStyle: React.CSSProperties = {
  display: "flex",
  gap: "6px",
  flexShrink: 0,
  marginLeft: "12px",
};

const actionButtonStyle: React.CSSProperties = {
  background: "var(--nav)",
  color: "var(--text)",
  border: "none",
  borderRadius: "6px",
  padding: "4px 10px",
  fontSize: "13px",
  cursor: "pointer",
};

const formStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  gap: "10px",
  marginTop: "16px",
};

const inputStyle: React.CSSProperties = {
  background: "var(--bg)",
  color: "var(--text)",
  border: "1px solid var(--nav)",
  borderRadius: "6px",
  padding: "8px 10px",
  fontSize: "14px",
  width: "100%",
  boxSizing: "border-box",
};

const submitButtonStyle: React.CSSProperties = {
  background: "var(--accent)",
  color: "#fff",
  border: "none",
  borderRadius: "8px",
  padding: "8px 16px",
  fontSize: "14px",
  cursor: "pointer",
};

const cancelButtonStyle: React.CSSProperties = {
  background: "var(--nav)",
  color: "var(--text)",
  border: "none",
  borderRadius: "8px",
  padding: "8px 16px",
  fontSize: "14px",
  cursor: "pointer",
};

const errorStyle: React.CSSProperties = {
  color: "#ef4444",
  fontSize: "13px",
  marginTop: "4px",
};

export function ModelForm({ onRefresh }: ModelFormProps): React.ReactNode {
  const [models, setModels] = useState<AdminModel[]>([]);
  const [providers, setProviders] = useState<AdminProvider[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);

  const [providerName, setProviderName] = useState("");
  const [modelId, setModelId] = useState("");
  const [maxTokens, setMaxTokens] = useState<number>(8192);
  const [temperature, setTemperature] = useState<number>(0.2);
  const [maxContextTokens, setMaxContextTokens] = useState<number>(128000);
  const [editingId, setEditingId] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);

  const loadData = useCallback(async () => {
    setError(null);
    const [modelsResult, providersResult] = await Promise.all([
      listModels(),
      listProviders(),
    ]);
    if (modelsResult.ok) {
      setModels(modelsResult.data);
    } else {
      setError(modelsResult.message);
    }
    if (providersResult.ok) {
      setProviders(providersResult.data);
    } else {
      setError((prev) => (prev ? `${prev}; ${providersResult.message}` : providersResult.message));
    }
  }, []);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const clearForm = () => {
    setProviderName("");
    setModelId("");
    setMaxTokens(8192);
    setTemperature(0.2);
    setMaxContextTokens(128000);
    setEditingId("");
    setFormError(null);
  };

  const handleEdit = (model: AdminModel) => {
    const slashIdx = model.model.indexOf("/");
    if (slashIdx >= 0) {
      setProviderName(model.model.slice(0, slashIdx));
      setModelId(model.model.slice(slashIdx + 1));
    } else {
      setProviderName("");
      setModelId(model.model);
    }
    setMaxTokens(model.max_tokens);
    setTemperature(model.temperature);
    setMaxContextTokens(model.max_context_tokens);
    setEditingId(model.model);
    setFormError(null);
  };

  const handleDelete = async (id: string) => {
    if (!window.confirm(`Delete model "${id}"?`)) {
      return;
    }
    setIsSubmitting(true);
    setFormError(null);
    const result = await deleteModel(id);
    if (result.ok) {
      if (id === editingId) {
        clearForm();
      }
      await loadData();
      onRefresh?.();
    } else {
      setFormError(result.message);
    }
    setIsSubmitting(false);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!providerName.trim() || !modelId.trim()) {
      setFormError("Provider and Model ID are required");
      return;
    }
    setIsSubmitting(true);
    setFormError(null);

    const model: AdminModel = {
      model: `${providerName}/${modelId}`,
      max_tokens: maxTokens,
      temperature: temperature,
      max_context_tokens: maxContextTokens,
    };

    if (editingId) {
      const result = await updateModel(editingId, model);
      if (result.ok) {
        clearForm();
        await loadData();
        onRefresh?.();
      } else {
        setFormError(result.message);
      }
    } else {
      const result = await createModel(model);
      if (result.ok) {
        clearForm();
        await loadData();
        onRefresh?.();
      } else {
        setFormError(result.message);
      }
    }
    setIsSubmitting(false);
  };

  return (
    <div>
      {error && <div style={errorStyle}>{error}</div>}

      <div style={listContainerStyle}>
        {models.map((model) => (
          <div key={model.model} style={rowStyle}>
            <div style={rowTextStyle}>
              <span style={{ fontSize: "14px", fontWeight: 500 }}>
                {model.model}
              </span>
              <span style={{ fontSize: "12px", color: "var(--muted)" }}>
                max_tokens: {model.max_tokens} — temperature: {model.temperature} — max_context_tokens: {model.max_context_tokens}
              </span>
            </div>
            <div style={rowActionsStyle}>
              <button
                type="button"
                style={actionButtonStyle}
                onClick={() => handleEdit(model)}
                disabled={isSubmitting || providers.length === 0}
              >
                Edit
              </button>
              <button
                type="button"
                style={actionButtonStyle}
                onClick={() => handleDelete(model.model)}
                disabled={isSubmitting || providers.length === 0}
              >
                Delete
              </button>
            </div>
          </div>
        ))}
      </div>

      <form style={formStyle} onSubmit={handleSubmit}>
        <label htmlFor="model-provider" style={{ fontSize: "14px" }}>
          Provider
        </label>
        {providers.length === 0 ? (
          <div style={{ fontSize: "13px", color: "var(--muted)" }}>
            No providers available. Add a provider first.
          </div>
        ) : (
          <select
            id="model-provider"
            style={inputStyle}
            value={providerName}
            onChange={(e) => setProviderName(e.target.value)}
            required
          >
            <option value="">Select provider</option>
            {providers.map((p) => (
              <option key={p.name} value={p.name}>
                {p.name}
              </option>
            ))}
          </select>
        )}

        <label htmlFor="model-id" style={{ fontSize: "14px" }}>
          Model ID
        </label>
        <input
          id="model-id"
          type="text"
          placeholder="Model ID"
          style={inputStyle}
          value={modelId}
          onChange={(e) => setModelId(e.target.value)}
          required
        />

        <label htmlFor="model-max-tokens" style={{ fontSize: "14px" }}>
          Max Tokens
        </label>
        <input
          id="model-max-tokens"
          type="number"
          step="1"
          placeholder="Max Tokens"
          style={inputStyle}
          value={maxTokens}
          onChange={(e) => {
            const v = e.target.value;
            const n = v === "" ? 0 : Number(v);
            setMaxTokens(isNaN(n) ? 0 : n);
          }}
          required
        />

        <label htmlFor="model-temperature" style={{ fontSize: "14px" }}>
          Temperature
        </label>
        <input
          id="model-temperature"
          type="number"
          step="0.1"
          placeholder="Temperature"
          style={inputStyle}
          value={temperature}
          onChange={(e) => {
            const v = e.target.value;
            const n = v === "" ? 0 : Number(v);
            setTemperature(isNaN(n) ? 0 : n);
          }}
          required
        />

        <label htmlFor="model-max-context-tokens" style={{ fontSize: "14px" }}>
          Max Context Tokens
        </label>
        <input
          id="model-max-context-tokens"
          type="number"
          step="1"
          placeholder="Max Context Tokens"
          style={inputStyle}
          value={maxContextTokens}
          onChange={(e) => {
            const v = e.target.value;
            const n = v === "" ? 0 : Number(v);
            setMaxContextTokens(isNaN(n) ? 0 : n);
          }}
          required
        />

        {formError && <div style={errorStyle}>{formError}</div>}

        <div style={{ display: "flex", gap: "8px" }}>
          <button
            type="submit"
            style={submitButtonStyle}
            disabled={isSubmitting || providers.length === 0}
          >
            {editingId ? "Save model" : "Add model"}
          </button>
          {editingId && (
            <button
              type="button"
              style={cancelButtonStyle}
              onClick={clearForm}
            >
              Cancel
            </button>
          )}
        </div>
      </form>
    </div>
  );
}

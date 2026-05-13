import { useState, useEffect, useCallback } from "react";
import type React from "react";
import {
  listProviders,
  createProvider,
  updateProvider,
  deleteProvider,
  type AdminProvider,
} from "./api";

export interface ProviderFormProps {
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

export function ProviderForm({ onRefresh }: ProviderFormProps): React.ReactNode {
  const [providers, setProviders] = useState<AdminProvider[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);

  const [name, setName] = useState("");
  const [type, setType] = useState<"openai" | "anthropic">("openai");
  const [apiBase, setApiBase] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [editingName, setEditingName] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);

  const loadProviders = useCallback(async () => {
    setError(null);
    const result = await listProviders();
    if (result.ok) {
      setProviders(result.data);
    } else {
      setError(result.message);
    }
  }, []);

  useEffect(() => {
    loadProviders();
  }, [loadProviders]);

  const clearForm = () => {
    setName("");
    setType("openai");
    setApiBase("");
    setApiKey("");
    setEditingName("");
    setFormError(null);
  };

  const handleEdit = (provider: AdminProvider) => {
    setName(provider.name);
    setType(provider.type);
    setApiBase(provider.api_base);
    setApiKey(provider.api_key ?? "");
    setEditingName(provider.name);
    setFormError(null);
  };

  const handleDelete = async (providerName: string) => {
    if (!window.confirm(`Delete provider "${providerName}"?`)) {
      return;
    }
    setIsSubmitting(true);
    setFormError(null);
    const result = await deleteProvider(providerName);
    if (result.ok) {
      if (providerName === editingName) {
        clearForm();
      }
      await loadProviders();
      onRefresh?.();
    } else {
      setFormError(result.message);
    }
    setIsSubmitting(false);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setIsSubmitting(true);
    setFormError(null);

    const provider: AdminProvider = {
      name,
      type,
      api_base: apiBase,
      ...(apiKey ? { api_key: apiKey } : {}),
    };

    if (editingName) {
      const result = await updateProvider(editingName, provider);
      if (result.ok) {
        clearForm();
        await loadProviders();
        onRefresh?.();
      } else {
        setFormError(result.message);
      }
    } else {
      const result = await createProvider(provider);
      if (result.ok) {
        clearForm();
        await loadProviders();
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
        {providers.map((provider) => (
          <div key={provider.name} style={rowStyle}>
            <div style={rowTextStyle}>
              <span style={{ fontSize: "14px", fontWeight: 500 }}>
                {provider.name}
              </span>
              <span style={{ fontSize: "12px", color: "var(--muted)" }}>
                {provider.type} — {provider.api_base}
              </span>
            </div>
            <div style={rowActionsStyle}>
              <button
                type="button"
                style={actionButtonStyle}
                onClick={() => handleEdit(provider)}
                disabled={isSubmitting}
              >
                Edit
              </button>
              <button
                type="button"
                style={actionButtonStyle}
                onClick={() => handleDelete(provider.name)}
                disabled={isSubmitting}
              >
                Delete
              </button>
            </div>
          </div>
        ))}
      </div>

      <form style={formStyle} onSubmit={handleSubmit}>
        <label htmlFor="provider-name" style={{ fontSize: "14px" }}>
          Name
        </label>
        <input
          id="provider-name"
          type="text"
          placeholder="Name"
          style={inputStyle}
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
        />
        <label htmlFor="provider-type" style={{ fontSize: "14px" }}>
          Type
        </label>
        <select
          id="provider-type"
          style={inputStyle}
          value={type}
          onChange={(e) =>
            setType(e.target.value as "openai" | "anthropic")
          }
        >
          <option value="openai">openai</option>
          <option value="anthropic">anthropic</option>
        </select>
        <label htmlFor="provider-api-base" style={{ fontSize: "14px" }}>
          API Base
        </label>
        <input
          id="provider-api-base"
          type="text"
          placeholder="API Base"
          style={inputStyle}
          value={apiBase}
          onChange={(e) => setApiBase(e.target.value)}
          required
        />
        <label htmlFor="provider-api-key" style={{ fontSize: "14px" }}>
          API Key
        </label>
        <input
          id="provider-api-key"
          type="password"
          placeholder="API Key"
          style={inputStyle}
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
        />

        {formError && <div style={errorStyle}>{formError}</div>}

        <div style={{ display: "flex", gap: "8px" }}>
          <button
            type="submit"
            style={submitButtonStyle}
            disabled={isSubmitting}
          >
            {editingName ? "Save provider" : "Add provider"}
          </button>
          {editingName && (
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

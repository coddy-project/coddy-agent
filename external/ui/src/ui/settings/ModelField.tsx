import { useState } from "react";
import { Combobox } from "./Combobox";
import { useProviderModels } from "./useProviderModels";

function providerOf(value: string): string {
  const i = value.indexOf("/");
  return i > 0 ? value.slice(0, i) : "";
}

/**
 * ModelField edits a logical model id (provider/api-model-id). The provider is an
 * editable combobox over the configured providers; "Fetch models" pulls the
 * provider's advertised models (Kilo-style) into the model combobox, which is also
 * editable so the id can be typed manually when no list is available.
 */
export function ModelField(props: {
  value: string;
  onChange: (v: string) => void;
  providers: string[];
  label?: string | undefined;
}) {
  const { value, onChange, providers } = props;
  const label = props.label ?? "Model id";

  const inferred = providerOf(value);
  const [provider, setProvider] = useState<string>(
    inferred && providers.includes(inferred) ? inferred : providers[0] ?? "",
  );
  const { loading, models, error, fetched, fetchModels, reset } = useProviderModels();

  const modelOptions = models.map((m) => ({
    value: `${provider}/${m.id}`,
    label: m.name ? `${m.name} — ${provider}/${m.id}` : `${provider}/${m.id}`,
  }));

  return (
    <div className="settings-row" data-testid="model-field">
      <span className="settings-label">{label}</span>

      <div className="model-field-controls">
        <Combobox
          value={provider}
          onChange={(v) => {
            setProvider(v);
            reset();
          }}
          options={providers.map((p) => ({ value: p }))}
          ariaLabel="Provider"
          testid="model-field-provider"
          placeholder="provider"
        />
        <button
          type="button"
          className="settings-btn"
          data-testid="model-field-fetch"
          disabled={!provider || loading}
          onClick={() => void fetchModels(provider)}
        >
          {loading ? "Fetching…" : "Fetch models"}
        </button>
      </div>

      {fetched && error ? (
        <p className="settings-field-desc">
          Couldn't fetch models: {error}. Type the model id manually below.
        </p>
      ) : null}
      {fetched && !error && models.length === 0 ? (
        <p className="settings-field-desc">
          No models returned. Type the model id manually below.
        </p>
      ) : null}

      <Combobox
        value={value}
        onChange={onChange}
        options={modelOptions}
        ariaLabel={label}
        testid="model-field-model"
        placeholder="provider/model-id"
      />
    </div>
  );
}

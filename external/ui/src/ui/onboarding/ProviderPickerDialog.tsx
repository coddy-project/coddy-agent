import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { errorMessage } from "../settings/apiError";
import { Combobox } from "../settings/Combobox";
import { CodexAuthField } from "../settings/CodexAuthField";
import { NeuralDeepAuthField } from "../settings/NeuralDeepAuthField";
import { OpenCodeAuthField } from "../settings/OpenCodeAuthField";
import { ProxySettingField } from "../settings/ProxySettingField";
import { useProviderModels } from "../settings/useProviderModels";
import { DevinAuthField } from "./DevinAuthField";
import {
  buildOnboardingConfig,
  catalogEntry,
  NEURALDEEP_API_BASE_OPTIONS,
  OPENCODE_API_BASE_OPTIONS,
  PROVIDER_PRESETS,
  type ProviderPresetId,
} from "./providerConfig";

export function ProviderPickerDialog(props: {
  open: boolean;
  onSaved: () => void;
  onSkip: () => void;
}) {
  const { t } = useT();
  const [selected, setSelected] = useState<ProviderPresetId>("openai");
  const [apiKey, setApiKey] = useState("");
  const [apiBase, setApiBase] = useState("");
  const [proxy, setProxy] = useState("");
  const [modelId, setModelId] = useState("");
  const [showKey, setShowKey] = useState(false);
  const [managedLogin, setManagedLogin] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [testOk, setTestOk] = useState(false);
  // The /coddy/config document to merge into: null until one has loaded. Save
  // must never build from the empty stand-in - PUT /coddy/config writes the
  // lists as sent, so that would replace every saved row with this one.
  const [baseDoc, setBaseDoc] = useState<Record<string, unknown> | null>(null);
  const baseLoad = useRef<Promise<Record<string, unknown>> | null>(null);
  const {
    loading: modelsLoading,
    models: fetchedModels,
    error: modelsError,
    fetched: modelsFetched,
    fetchModels,
    reset: resetModels,
  } = useProviderModels();

  const preset = useMemo(
    () =>
      PROVIDER_PRESETS.find((item) => item.id === selected) ??
      PROVIDER_PRESETS[0],
    [selected],
  );
  const effectiveApiBase = apiBase.trim() || preset.apiBase || "";
  const keyless =
    preset.providerType === "codex" ||
    (managedLogin &&
      !apiKey.trim() &&
      ["neuraldeep", "opencode", "devin"].includes(preset.providerType));
  const providerRow = useMemo(
    () => ({
      name: preset.providerName,
      type: preset.providerType,
      api_base:
        preset.providerType === "codex" || preset.providerType === "devin"
          ? ""
          : effectiveApiBase,
      api_key: keyless ? "" : apiKey,
      api_key_command: "",
      proxy,
    }),
    [apiKey, effectiveApiBase, keyless, preset, proxy],
  );
  const pickedModel = useMemo(
    () => catalogEntry(preset, modelId, fetchedModels),
    [preset, modelId, fetchedModels],
  );

  /** The current config document, one request shared by every caller in
   * flight; a failure leaves no promise behind, so Save may retry it. */
  const loadBaseDoc = useCallback((): Promise<Record<string, unknown>> => {
    const pending =
      baseLoad.current ??
      fetch("/coddy/config")
        .then(async (response) => {
          if (!response.ok)
            throw new Error(
              await errorMessage(response, `HTTP ${response.status}`),
            );
          return ((await response.json()) as Record<string, unknown>) || {};
        })
        .finally(() => {
          baseLoad.current = null;
        });
    baseLoad.current = pending;
    return pending;
  }, []);

  useEffect(() => {
    if (!props.open) return;
    setError(null);
    setTestOk(false);
    setBaseDoc(null);
    loadBaseDoc()
      .then((doc) => setBaseDoc(doc))
      .catch((err: unknown) =>
        setError(err instanceof Error ? err.message : String(err)),
      );
  }, [props.open, loadBaseDoc]);

  useEffect(() => {
    setError(null);
    setTestOk(false);
    setManagedLogin(false);
    setApiBase(preset.apiBase || "");
    setApiKey(preset.id === "ollama" ? "~" : "");
    setModelId(
      ["neuraldeep", "codex", "opencode", "devin"].includes(preset.id)
        ? preset.defaultModel
        : "",
    );
    resetModels();
  }, [preset, resetModels]);

  useEffect(() => {
    if (!props.open || !managedLogin || apiKey.trim()) return;
    const timer = window.setTimeout(() => void fetchModels(providerRow), 100);
    return () => window.clearTimeout(timer);
  }, [props.open, managedLogin, apiKey, providerRow, fetchModels]);

  const testConnection = useCallback(async () => {
    setBusy(true);
    setError(null);
    setTestOk(false);
    try {
      const result = await fetchModels(providerRow);
      if (result === null) return;
      if (result.error) {
        setError(result.error);
        return;
      }
      setTestOk(true);
    } finally {
      setBusy(false);
    }
  }, [fetchModels, providerRow]);

  const save = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      // Save waits for the base document: merging into an empty one would put
      // a one-row document over the config file's lists.
      let base = baseDoc;
      if (!base) {
        base = await loadBaseDoc();
        setBaseDoc(base);
      }
      const body = JSON.stringify(
        buildOnboardingConfig({
          preset,
          apiKey,
          apiBase: effectiveApiBase,
          proxy,
          modelId,
          baseDoc: base,
          catalog: fetchedModels,
          managedLogin,
        }),
      );
      const validation = await fetch("/coddy/config/validate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body,
      });
      if (!validation.ok) {
        setError(
          await errorMessage(validation, t("onboarding.validationFailed")),
        );
        return;
      }
      const verdict = (await validation.json()) as {
        ok?: boolean;
        error?: string;
      };
      if (!verdict.ok) {
        setError(verdict.error || t("onboarding.validationFailed"));
        return;
      }
      const response = await fetch("/coddy/config", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body,
      });
      if (!response.ok) {
        setError(
          await errorMessage(
            response,
            t("onboarding.saveFailed", { status: response.status }),
          ),
        );
        return;
      }
      const saved = (await response.json()) as { ok?: boolean; error?: string };
      if (!saved.ok) {
        setError(
          saved.error ||
            t("onboarding.saveFailed", { status: response.status }),
        );
        return;
      }
      props.onSaved();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : t("onboarding.saveFailedGeneric"),
      );
    } finally {
      setBusy(false);
    }
  }, [
    apiKey,
    baseDoc,
    effectiveApiBase,
    fetchedModels,
    loadBaseDoc,
    managedLogin,
    modelId,
    preset,
    proxy,
    props,
    t,
  ]);

  if (!props.open) return null;

  return (
    <div className="provider-picker-host" data-testid="provider-picker-dialog">
      <button
        type="button"
        className="backdrop is-open"
        aria-label={t("onboarding.close")}
        onClick={props.onSkip}
      />
      <div
        className="provider-picker-panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby="provider-picker-title"
      >
        <div className="provider-picker-head">
          <h2 id="provider-picker-title">{t("onboarding.title")}</h2>
          <p className="provider-picker-lead">{t("onboarding.lead")}</p>
        </div>

        <div className="provider-picker-grid">
          {PROVIDER_PRESETS.map((item) => (
            <button
              key={item.id}
              type="button"
              className={`provider-picker-card${selected === item.id ? " provider-picker-card--active" : ""}`}
              data-testid={`provider-card-${item.id}`}
              onClick={() => setSelected(item.id)}
            >
              <span className="provider-picker-card-title">{item.label}</span>
              <span className="provider-picker-card-desc">
                {t(item.descriptionKey)}
              </span>
            </button>
          ))}
        </div>

        <div className="provider-picker-form">
          {selected === "neuraldeep" || selected === "opencode" ? (
            <label className="provider-picker-field">
              <span>{t("onboarding.apiBase")}</span>
              <select
                className="provider-picker-input"
                value={effectiveApiBase}
                onChange={(event) => {
                  setApiBase(event.target.value);
                  setManagedLogin(false);
                  setTestOk(false);
                  resetModels();
                }}
                data-testid={`provider-${selected}-domain`}
              >
                {(selected === "neuraldeep"
                  ? NEURALDEEP_API_BASE_OPTIONS
                  : OPENCODE_API_BASE_OPTIONS
                ).map((option) => (
                  <option key={option.value} value={option.value}>
                    {t(option.labelKey)}
                  </option>
                ))}
              </select>
            </label>
          ) : null}

          {selected === "codex" ? (
            <CodexAuthField
              providerName={preset.providerName}
              proxy={proxy}
              texts={{
                primary: t("codexAuth.fieldLabel"),
                primaryDesc: t("onboarding.codexAuth.description"),
              }}
              onConnectedChange={setManagedLogin}
            />
          ) : (
            <label className="provider-picker-field">
              <span>{t("onboarding.apiKey")}</span>
              <div className="provider-picker-key-row">
                <input
                  className="provider-picker-input"
                  type={showKey ? "text" : "password"}
                  value={apiKey}
                  onChange={(event) => {
                    setApiKey(event.target.value);
                    setTestOk(false);
                  }}
                  placeholder={preset.envKey || "sk-..."}
                  autoComplete="off"
                  data-testid="provider-api-key"
                />
                <button
                  type="button"
                  className="provider-picker-ghost-btn"
                  onClick={() => setShowKey((value) => !value)}
                >
                  {showKey ? t("onboarding.hideKey") : t("onboarding.showKey")}
                </button>
              </div>
            </label>
          )}

          {selected === "neuraldeep" ? (
            <NeuralDeepAuthField
              providerName={preset.providerName}
              hasExplicitKey={apiKey.trim() !== ""}
              apiBase={effectiveApiBase}
              proxy={proxy}
              texts={{
                primary: "",
                second: t("neuralDeepAuth.fieldLabel"),
                secondDesc: t("onboarding.neuralDeepAuth.description"),
              }}
              onConnectedChange={setManagedLogin}
            />
          ) : null}
          {selected === "opencode" ? (
            <OpenCodeAuthField
              providerName={preset.providerName}
              hasExplicitKey={apiKey.trim() !== ""}
              proxy={proxy}
              texts={{
                primary: "",
                second: t("openCodeAuth.fieldLabel"),
                secondDesc: t("onboarding.openCodeAuth.description"),
              }}
              onConnectedChange={setManagedLogin}
            />
          ) : null}
          {selected === "devin" ? (
            <DevinAuthField
              providerName={preset.providerName}
              hasExplicitKey={apiKey.trim() !== ""}
              proxy={proxy}
              texts={{
                primary: "",
                second: t("devinAuth.fieldLabel"),
                secondDesc: t("onboarding.devinAuth.description"),
              }}
              onConnectedChange={setManagedLogin}
            />
          ) : null}

          {selected === "compatible" || selected === "ollama" ? (
            <label className="provider-picker-field">
              <span>{t("onboarding.apiBase")}</span>
              <input
                className="provider-picker-input"
                value={apiBase}
                onChange={(event) => setApiBase(event.target.value)}
                placeholder="https://api.example.com/v1"
                data-testid="provider-api-base"
              />
            </label>
          ) : null}

          <div className="provider-picker-proxy">
            <ProxySettingField
              value={proxy}
              onChange={(value) => setProxy(String(value ?? ""))}
              texts={{
                primary: t("onboarding.proxy"),
                primaryDesc: t("onboarding.proxyHint"),
                second: t("settings.providerProxy.ignoreSystem"),
                secondDesc: t("settings.providerProxy.ignoreSystemDesc"),
              }}
            />
          </div>

          <label className="provider-picker-field">
            <span>{t("onboarding.defaultModel")}</span>
            <div className="provider-picker-key-row">
              <Combobox
                value={modelId}
                onChange={setModelId}
                options={fetchedModels.map((model) => ({
                  value: model.id,
                  label: model.catalog?.multimodal
                    ? t("onboarding.modelVisionOption", {
                        model: model.name || model.id,
                      })
                    : model.name || model.id,
                }))}
                placeholder={preset.defaultModel}
                ariaLabel={t("onboarding.defaultModel")}
                testid="provider-model-id"
              />
              <button
                type="button"
                className="provider-picker-ghost-btn"
                onClick={() => void testConnection()}
                disabled={busy || modelsLoading}
                data-testid="provider-fetch-models"
              >
                {modelsLoading
                  ? t("onboarding.fetchingModels")
                  : fetchedModels.length > 0
                    ? t("onboarding.refreshModels")
                    : t("onboarding.fetchModels")}
              </button>
            </div>
            {modelsFetched && modelsError ? (
              <span
                className="provider-picker-hint"
                data-testid="provider-models-error"
              >
                {modelsError}
              </span>
            ) : null}
            {pickedModel ? (
              <span
                className="provider-picker-hint"
                data-testid="provider-multimodal-note"
              >
                {pickedModel.catalog?.multimodal
                  ? t("onboarding.modelMultimodalOn")
                  : t("onboarding.modelMultimodalOff")}
              </span>
            ) : null}
          </label>

          {error ? (
            <div className="provider-picker-error" data-testid="provider-error">
              {error}
            </div>
          ) : null}
          {testOk ? (
            <div className="provider-picker-ok" data-testid="provider-test-ok">
              {t("onboarding.testOk")}
            </div>
          ) : null}
        </div>

        <div className="provider-picker-actions">
          <button
            type="button"
            className="provider-picker-secondary"
            onClick={props.onSkip}
            disabled={busy}
            data-testid="provider-skip"
          >
            {t("onboarding.skip")}
          </button>
          <button
            type="button"
            className="provider-picker-secondary"
            onClick={() => void testConnection()}
            disabled={busy || modelsLoading}
            data-testid="provider-test"
          >
            {t("onboarding.test")}
          </button>
          <button
            type="button"
            className="provider-picker-primary"
            onClick={() => void save()}
            disabled={busy}
            data-testid="provider-save"
          >
            {t("onboarding.save")}
          </button>
        </div>
      </div>
    </div>
  );
}

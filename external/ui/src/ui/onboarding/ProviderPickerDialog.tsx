import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { errorMessage } from "../settings/apiError";
import { CollapsibleFieldset } from "../settings/SchemaForm";
import { CodexAuthField } from "../settings/CodexAuthField";
import { NeuralDeepAuthField } from "../settings/NeuralDeepAuthField";
import { KimiAuthField } from "../settings/KimiAuthField";
import { OpenCodeAuthField } from "../settings/OpenCodeAuthField";
import {
  kimiSourceMatches,
  useProviderModels,
} from "../settings/useProviderModels";
import { DevinAuthField } from "../settings/DevinAuthField";
import {
  buildOnboardingConfig,
  startingModelId,
  NEURALDEEP_API_BASE_OPTIONS,
  OPENCODE_API_BASE_OPTIONS,
  PROVIDER_PRESETS,
  type ProviderPresetId,
} from "./providerConfig";

/** EyeIcon is the reveal control of a secret field: an open eye while the
 * value is on screen, the same eye struck through while it is hidden. The
 * ink is centred in the viewBox, so it does not move with the platform's
 * font the way a text glyph would. */
function EyeIcon({ off }: { off: boolean }) {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.3"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M1.5 8S3.9 3.6 8 3.6 14.5 8 14.5 8 12.1 12.4 8 12.4 1.5 8 1.5 8Z" />
      <circle cx="8" cy="8" r="2.1" />
      {off ? <path d="M2.6 2.6 13.4 13.4" /> : null}
    </svg>
  );
}

export function ProviderPickerDialog(props: {
  open: boolean;
  onSaved: () => void;
  onSkip: () => void;
}) {
  const { t } = useT();
  // The picker opens on the first preset, which is what the order of
  // PROVIDER_PRESETS means: no card is picked out of the grid by hand.
  const [selected, setSelected] = useState<ProviderPresetId>(
    PROVIDER_PRESETS[0].id,
  );
  const [kimiMode, setKimiMode] = useState<"subscription" | "platform">(
    "subscription",
  );
  const [kimiIdentity, setKimiIdentity] = useState("");
  const [kimiRegion, setKimiRegion] = useState<"global" | "china">("global");
  const [apiKey, setApiKey] = useState("");
  const [apiBase, setApiBase] = useState("");
  const [command, setCommand] = useState("");
  const [proxy, setProxy] = useState("");
  const [showKey, setShowKey] = useState(false);
  // What the provider's own sign-in would do, published by its field. A modal
  // shows one primary action at a time, so this one takes the actions row
  // until the account is connected, and Save takes it back afterwards.
  const [signInAction, setSignInAction] = useState<{
    label: string;
    disabled: boolean;
    pending: boolean;
    run: () => void;
  } | null>(null);
  const [managedLogin, setManagedLogin] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The /coddy/config document to merge into: null until one has loaded. Save
  // must never build from the empty stand-in - PUT /coddy/config writes the
  // lists as sent, so that would replace every saved row with this one.
  const [baseDoc, setBaseDoc] = useState<Record<string, unknown> | null>(null);
  const [baseLoadFailed, setBaseLoadFailed] = useState(false);
  const [statusRetry, setStatusRetry] = useState<{
    url: string;
    fingerprint: string;
  } | null>(null);
  const formFingerprint = JSON.stringify({
    selected,
    kimiMode,
    kimiRegion,
    kimiIdentity,
    apiKey,
    apiBase,
    command,
    proxy,
  });
  const currentForm = useRef(formFingerprint);
  currentForm.current = formFingerprint;
  const baseLoad = useRef<Promise<Record<string, unknown>> | null>(null);
  const gridRef = useRef<HTMLDivElement>(null);
  const {
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
  // The compatible preset has no endpoint of its own: without a typed api_base
  // the row would save keyless with no destination, so Test and Save wait. An
  // ACP row is its command the same way.
  const apiBaseMissing = preset.id === "compatible" && !effectiveApiBase;
  const commandMissing = preset.commandTyped === true && !command.trim();
  const incomplete =
    apiBaseMissing || commandMissing || (selected === "kimi" && !baseDoc);
  const keyless =
    (preset.providerType === "kimi" && kimiMode === "subscription") ||
    preset.providerType === "codex" ||
    // An ACP agent is a program this machine runs and signs itself in; the
    // row carries its command, never a key.
    preset.commandTyped === true ||
    (managedLogin &&
      !apiKey.trim() &&
      ["neuraldeep", "opencode", "devin"].includes(preset.providerType));
  const providerRow = useMemo(() => {
    const words = command.trim().split(/\s+/).filter(Boolean);
    return {
      name: preset.providerName,
      type: preset.providerType,
      ...(selected === "kimi"
        ? { mode: kimiMode, region: kimiRegion, kimi_identity: kimiIdentity }
        : {}),
      api_base:
        preset.commandTyped ||
        preset.providerType === "codex" ||
        preset.providerType === "devin" ||
        preset.providerType === "kimi"
          ? ""
          : effectiveApiBase,
      api_key: keyless ? "" : apiKey,
      api_key_command: "",
      proxy,
      // An ACP agent is listed by starting it, so the probe needs the
      // command even before the row is saved.
      ...(preset.commandTyped && words.length > 0
        ? { acp: { command: words[0], args: words.slice(1) } }
        : {}),
    };
  }, [
    apiKey,
    command,
    effectiveApiBase,
    keyless,
    preset,
    proxy,
    selected,
    kimiMode,
    kimiRegion,
    kimiIdentity,
  ]);
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

  const acceptBaseDoc = useCallback((doc: Record<string, unknown>) => {
    setBaseDoc(doc);
    setBaseLoadFailed(false);
    const rows = Array.isArray(doc.providers) ? doc.providers : [];
    const row = rows.find(
      (item: unknown) =>
        item &&
        typeof item === "object" &&
        (item as Record<string, unknown>).name === "kimi" &&
        (item as Record<string, unknown>).type === "kimi",
    ) as Record<string, unknown> | undefined;
    setKimiIdentity(
      typeof row?.kimi_identity === "string" ? row.kimi_identity : "",
    );
  }, []);
  const retryBaseDoc = async () => {
    setBusy(true);
    setError(null);
    try {
      acceptBaseDoc(await loadBaseDoc());
    } catch (err) {
      setBaseLoadFailed(true);
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  // Each edge of the catalog fades by what lies past it: the top one grows as
  // the first rows go under it, the bottom one thins as the end comes, and an
  // edge with nothing beyond it is sharp. Written straight onto the node, so
  // a scroll renders nothing.
  useEffect(() => {
    const grid = gridRef.current;
    if (!props.open || !grid) return;
    // Light enough that the row under the edge still reads: at 26 the cards
    // showing through were washed out.
    const FADE = 14;
    const edge = (past: number) => `${Math.max(0, Math.min(FADE, past))}px`;
    const sync = () => {
      const below = grid.scrollHeight - grid.clientHeight - grid.scrollTop;
      grid.style.setProperty("--onboarding-fade-top", edge(grid.scrollTop));
      grid.style.setProperty("--onboarding-fade-bottom", edge(below));
    };
    sync();
    grid.addEventListener("scroll", sync, { passive: true });
    const observer = new ResizeObserver(sync);
    observer.observe(grid);
    return () => {
      grid.removeEventListener("scroll", sync);
      observer.disconnect();
    };
  }, [props.open, selected]);

  useEffect(() => {
    if (!props.open) return;
    setError(null);
    setBaseDoc(null);
    setBaseLoadFailed(false);
    setStatusRetry(null);
    loadBaseDoc()
      .then(acceptBaseDoc)
      .catch((err: unknown) => {
        setBaseLoadFailed(true);
        setError(err instanceof Error ? err.message : String(err));
      });
  }, [props.open, loadBaseDoc, acceptBaseDoc]);

  useEffect(() => {
    if (!baseLoadFailed) setError(null);
    setManagedLogin(false);
    setApiBase(preset.apiBase || "");
    setApiKey("");
    setCommand("");
    // The sign-in action is not reset here: a child effect runs before its
    // parent's, so this would wipe what the provider's field just published.
    // The field's own cleanup clears it when it unmounts.
    resetModels();
  }, [preset, resetModels]);

  useEffect(() => {
    if (!props.open || !managedLogin || apiKey.trim()) return;
    const timer = window.setTimeout(() => void fetchModels(providerRow), 100);
    return () => window.clearTimeout(timer);
  }, [props.open, managedLogin, apiKey, providerRow, fetchModels]);

  const checkSavedStatus = useCallback(
    async (attempt: { url: string; fingerprint: string }) => {
      try {
        const response = await fetch(attempt.url);
        if (!response.ok) throw new Error("status unavailable");
        const status = (await response.json()) as { connected?: boolean };
        if (status.connected !== true) throw new Error("status unavailable");
        setStatusRetry(null);
        if (currentForm.current !== attempt.fingerprint) {
          setError(t("onboarding.savedChangesRemain"));
          return;
        }
        props.onSaved();
      } catch {
        setStatusRetry(attempt);
        setError(t("kimiAuth.statusAfterSaveFailed"));
      }
    },
    [props.onSaved, t],
  );

  const retrySavedStatus = async () => {
    if (!statusRetry) return;
    setBusy(true);
    setError(null);
    try {
      await checkSavedStatus(statusRetry);
    } finally {
      setBusy(false);
    }
  };

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
      if (preset.providerType === "kimi" && kimiMode === "subscription") {
        const existing = Array.isArray(base.providers)
          ? base.providers.find(
              (row: unknown) =>
                row !== null &&
                typeof row === "object" &&
                !Array.isArray(row) &&
                (row as Record<string, unknown>).name === preset.providerName,
            )
          : undefined;
        if (
          existing &&
          (String(existing.api_key ?? "").trim() ||
            String(existing.api_key_command ?? "").trim())
        ) {
          setError(t("onboarding.kimi.existingKey"));
          return;
        }
      }
      // No field asks for a model any more, so Save is where the catalog is
      // learned when nothing fetched it before: without it an endpoint the
      // operator brought would start on the preset's default, which it may
      // not serve at all. A provider that cannot answer is not an error here
      // - the row is still written, on that default.
      let catalog =
        preset.providerType === "kimi"
          ? fetchedModels.filter((model) =>
              kimiSourceMatches(model, providerRow),
            )
          : fetchedModels;
      if (catalog.length === 0) {
        const listing = await fetchModels(providerRow);
        if (listing && !listing.error) {
          catalog = listing.models;
        }
      }
      if (currentForm.current !== formFingerprint) {
        setError(t("onboarding.savedChangesRemain"));
        return;
      }
      const deferKimiModel =
        preset.providerType === "kimi" &&
        catalog.length === 0 &&
        kimiMode === "subscription" &&
        managedLogin &&
        !!kimiIdentity;
      if (
        preset.providerType === "kimi" &&
        catalog.length === 0 &&
        !deferKimiModel
      ) {
        setError(modelsError || t("onboarding.validationFailed"));
        return;
      }
      const nextDoc = buildOnboardingConfig({
        preset,
        apiKey,
        apiBase: effectiveApiBase,
        command,
        proxy,
        modelId: startingModelId(preset, catalog),
        baseDoc: base,
        catalog,
        managedLogin,
        kimiMode,
        kimiRegion,
        kimiIdentity,
      });
      if (deferKimiModel) {
        nextDoc.models = base.models;
        nextDoc.agent = base.agent;
      }
      const body = JSON.stringify(nextDoc);
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
      if (currentForm.current !== formFingerprint) {
        setError(t("onboarding.savedChangesRemain"));
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
      if (
        preset.providerType === "kimi" &&
        kimiMode === "subscription" &&
        kimiIdentity &&
        managedLogin
      ) {
        const params = new URLSearchParams({
          mode: kimiMode,
          region: kimiRegion,
          kimi_identity: kimiIdentity,
        });
        setBaseDoc(JSON.parse(body) as Record<string, unknown>);
        await checkSavedStatus({
          url: `/coddy/providers/${encodeURIComponent(preset.providerName)}/kimi-auth?${params}`,
          fingerprint: formFingerprint,
        });
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
    command,
    effectiveApiBase,
    checkSavedStatus,
    formFingerprint,
    fetchModels,
    fetchedModels,
    loadBaseDoc,
    managedLogin,
    kimiMode,
    kimiRegion,
    kimiIdentity,
    preset,
    providerRow,
    proxy,
    props,
    t,
  ]);

  // A provider that signs in has two ways to present a credential, and
  // showing both at once asks the operator to choose before they know the
  // difference. The sign-in is the way in; the key stays available, under
  // Advanced settings, for whoever already has one. Codex has no key at all,
  // and an ACP agent carries neither.
  const keyIsAdvanced =
    !preset.commandTyped &&
    ["neuraldeep", "opencode", "devin"].includes(preset.providerType);
  // A preset with endpoints of its own opens on the right one, so the choice
  // is a refinement, not a question of the first run: it sits in the fold
  // with the key and the proxy. The compatible preset is the exception - it
  // has no endpoint at all and Save waits for one - so that field stays in
  // the form where it cannot be missed.
  const apiBaseField =
    selected === "neuraldeep" || selected === "opencode" ? (
      <label className="provider-picker-field">
        <span>{t("onboarding.apiBase")}</span>
        <select
          className="provider-picker-input"
          value={effectiveApiBase}
          onChange={(event) => {
            setApiBase(event.target.value);
            setManagedLogin(false);
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
    ) : null;

  const keyField = (
    <label className="provider-picker-field">
      <span>{t("onboarding.apiKey")}</span>
      <div className="provider-picker-secret">
        <input
          className="provider-picker-input"
          type={showKey ? "text" : "password"}
          value={apiKey}
          onChange={(event) => {
            setApiKey(event.target.value);
          }}
          placeholder={preset.envKey || "sk-..."}
          autoComplete="off"
          data-testid="provider-api-key"
        />
        <button
          type="button"
          className="provider-picker-eye"
          onClick={() => setShowKey((value) => !value)}
          aria-label={
            showKey ? t("onboarding.hideKey") : t("onboarding.showKey")
          }
          title={showKey ? t("onboarding.hideKey") : t("onboarding.showKey")}
          aria-pressed={showKey}
          data-testid="provider-key-reveal"
        >
          <EyeIcon off={!showKey} />
        </button>
      </div>
    </label>
  );

  // A key typed into the form is a decision: the person brought their own
  // credential and does not want the account flow, so the action goes back to
  // Save. An explicit key wins over a stored login everywhere else too.
  const signInKey =
    apiKey.trim() && !(selected === "kimi" && kimiMode === "subscription")
      ? null
      : signInAction;

  const card = (item: (typeof PROVIDER_PRESETS)[number]) => (
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
  );

  if (!props.open) return null;

  return (
    <div className="provider-picker-host" data-testid="provider-picker-dialog">
      <button
        type="button"
        className="backdrop is-open"
        aria-label={t("onboarding.close")}
        onClick={props.onSkip}
        disabled={busy}
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

        {/* Every provider in one grid that scrolls: two rows stand open
            and the wheel or the bar reaches the rest. A fold hid half the
            catalog behind a word and cost a row of its own. */}
        <div
          ref={gridRef}
          className="provider-picker-grid"
          data-testid="provider-grid"
          role="group"
          aria-label={t("onboarding.title")}
        >
          {PROVIDER_PRESETS.map(card)}
        </div>

        <div className="provider-picker-form">
          {selected === "kimi" ? (
            <>
              <label className="provider-picker-field">
                <span>{t("onboarding.kimi.variant")}</span>
                <select
                  className="provider-picker-input"
                  data-testid="kimi-variant"
                  value={`${kimiMode}:${kimiRegion}`}
                  onChange={(event) => {
                    const [mode, region] = event.target.value.split(":") as [
                      "subscription" | "platform",
                      "global" | "china",
                    ];
                    setKimiMode(mode);
                    setKimiRegion(region);
                    resetModels();
                  }}
                >
                  <option value="subscription:global">
                    Kimi Code (kimi.ai)
                  </option>
                  <option value="subscription:china">
                    Kimi Code (kimi.com)
                  </option>
                  <option value="platform:global">
                    Kimi Platform (kimi.ai)
                  </option>
                  <option value="platform:china">
                    Kimi Platform (kimi.com)
                  </option>
                </select>
              </label>
              {kimiMode === "subscription" && baseDoc ? (
                <KimiAuthField
                  providerName={preset.providerName}
                  mode={kimiMode}
                  region={kimiRegion}
                  proxy={proxy}
                  identity={kimiIdentity}
                  onIdentity={(id, expected) => {
                    if (kimiIdentity !== expected) return false;
                    setKimiIdentity(id);
                    return true;
                  }}
                  action={setSignInAction}
                  onReadyChange={setManagedLogin}
                  hideAction
                  accountOnly
                />
              ) : kimiMode === "platform" ? (
                keyField
              ) : null}
            </>
          ) : preset.commandTyped ? null : selected === "codex" ? (
            <CodexAuthField
              providerName={preset.providerName}
              proxy={proxy}
              // No heading over the button: it names the provider itself,
              // and a label with nothing under it but one button was a line
              // of chrome. The settings screen keeps its own.
              texts={{ primary: "" }}
              onConnectedChange={setManagedLogin}
              action={setSignInAction}
              hideAction
              accountOnly
            />
          ) : keyIsAdvanced ? null : (
            keyField
          )}

          {selected === "neuraldeep" ? (
            <NeuralDeepAuthField
              providerName={preset.providerName}
              hasExplicitKey={apiKey.trim() !== ""}
              apiBase={effectiveApiBase}
              proxy={proxy}
              texts={{ primary: "", second: "" }}
              onConnectedChange={setManagedLogin}
              action={setSignInAction}
              hideAction
              accountOnly
            />
          ) : null}
          {selected === "opencode" ? (
            <OpenCodeAuthField
              providerName={preset.providerName}
              hasExplicitKey={apiKey.trim() !== ""}
              proxy={proxy}
              texts={{ primary: "", second: "" }}
              onConnectedChange={setManagedLogin}
              action={setSignInAction}
              hideAction
              accountOnly
            />
          ) : null}
          {selected === "devin" ? (
            <DevinAuthField
              providerName={preset.providerName}
              hasExplicitKey={apiKey.trim() !== ""}
              proxy={proxy}
              texts={{ primary: "", second: "" }}
              onConnectedChange={setManagedLogin}
              action={setSignInAction}
              hideAction
              accountOnly
            />
          ) : null}

          {preset.commandTyped ? (
            <label className="provider-picker-field">
              <span>{t("onboarding.command")}</span>
              <input
                className="provider-picker-input"
                value={command}
                onChange={(event) => {
                  setCommand(event.target.value);
                }}
                placeholder={preset.commandExample}
                autoComplete="off"
                spellCheck={false}
                data-testid="provider-command"
              />
              <span className="provider-picker-hint">
                {commandMissing
                  ? t("onboarding.commandRequired")
                  : t("onboarding.commandHint")}
              </span>
            </label>
          ) : null}

          {selected === "compatible" ? (
            <label className="provider-picker-field">
              <span>{t("onboarding.apiBase")}</span>
              <input
                className="provider-picker-input"
                value={apiBase}
                onChange={(event) => setApiBase(event.target.value)}
                placeholder={
                  preset.apiBaseExample || "https://api.example.com/v1"
                }
                data-testid="provider-api-base"
              />
              {apiBaseMissing ? (
                <span
                  className="provider-picker-hint"
                  data-testid="provider-api-base-required"
                >
                  {t("onboarding.apiBaseRequired")}
                </span>
              ) : null}
            </label>
          ) : null}

          <div className="provider-picker-advanced-fold">
            <CollapsibleFieldset
              legend={t("settings.providers.group.advanced")}
              testid="provider-advanced"
            >
              <div className="provider-picker-advanced">
                {apiBaseField}
                {keyIsAdvanced ? keyField : null}
                <label className="provider-picker-field">
                  <span>{t("onboarding.proxy")}</span>
                  <input
                    className="provider-picker-input"
                    value={proxy}
                    onChange={(event) => setProxy(event.target.value)}
                    placeholder={t("settings.providerProxy.placeholderSystem")}
                    autoComplete="off"
                    spellCheck={false}
                    data-testid="provider-proxy"
                  />
                  <span className="provider-picker-hint">
                    {t("onboarding.proxyHint")}
                  </span>
                </label>
              </div>
            </CollapsibleFieldset>
          </div>

          {modelsFetched && modelsError ? (
            <span
              className="provider-picker-hint"
              data-testid="provider-models-error"
            >
              {modelsError}
            </span>
          ) : null}

          {error ? (
            <div className="provider-picker-error" data-testid="provider-error">
              {error}
            </div>
          ) : null}
          {baseLoadFailed ? (
            <button
              type="button"
              className="settings-btn"
              disabled={busy}
              onClick={() => void retryBaseDoc()}
            >
              {t("onboarding.retryConfig")}
            </button>
          ) : null}
        </div>

        <div className="provider-picker-actions">
          {statusRetry ? (
            <button
              type="button"
              className="provider-picker-primary"
              disabled={busy}
              onClick={() => void retrySavedStatus()}
            >
              {t("onboarding.retryKimiStatus")}
            </button>
          ) : signInKey?.pending ? (
            // The sign-in is running and the page already says what to do
            // with the code: a disabled button repeating the wait was the
            // same words twice. The wait reads as the line it belongs to.
            <p
              className="settings-field-desc"
              data-testid="provider-signing-in"
            >
              {signInKey.label}
            </p>
          ) : signInKey ? (
            <button
              type="button"
              className="provider-picker-primary"
              onClick={signInKey.run}
              disabled={busy || signInKey.disabled || incomplete}
              data-testid="provider-sign-in"
            >
              {signInKey.label}
            </button>
          ) : (
            <button
              type="button"
              className="provider-picker-primary"
              onClick={() => void save()}
              disabled={busy || incomplete}
              data-testid="provider-save"
            >
              {t("onboarding.save")}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

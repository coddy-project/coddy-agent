import { AppearanceThemePicker } from "../theme/AppearanceModal";
import { applyModelsChange } from "./applyModelsChange";
import { CodexAuthField } from "./CodexAuthField";
import { Combobox } from "./Combobox";
import { FieldHint } from "./FieldHint";
import { NeuralDeepAuthField } from "./NeuralDeepAuthField";
import { ModelField } from "./ModelField";
import { ModelPicker } from "./ModelPicker";
import { ProviderModelsFetch } from "./ProviderModelsFetch";
import type { ProviderRow } from "./useProviderModels";
import { ProxySettingField } from "./ProxySettingField";
import { ReasoningLevelsField } from "./ReasoningLevelsField";
import {
  defaultForSchema,
  SchemaForm,
  type FieldOverride,
  type JsonSchema,
} from "./SchemaForm";
import { MCPSection } from "./MCPSection";
import {
  schemaFieldDesc,
  schemaFieldLabel,
  schemaFieldPlaceholder,
} from "./schemaI18n";
import { SettingsArraySection } from "./SettingsArraySection";
import { SessionsManager } from "../sessions/SessionsManager";
import { SkillsSection } from "./SkillsSection";
import { SubagentsSection } from "./SubagentsSection";
import { SwitchField } from "./SwitchField";
import type { SectionDescriptor } from "./settingsSections";
import { useT } from "../i18n/I18nProvider";

// The deployments a neuraldeep provider may point at, mirroring
// neuralDeepEndpoints in internal/llm/neuraldeep_auth.go. The backend ignores
// anything else in api_base and falls back to the first entry.
const NEURALDEEP_API_BASE_OPTIONS = [
  {
    value: "https://api.neuraldeep.ru/v1",
    labelKey: "neuralDeepApiBase.optionRu",
  },
  {
    value: "https://api.neuraldeep.tech/v1",
    labelKey: "neuralDeepApiBase.optionTech",
  },
] as const;
const NEURALDEEP_DEFAULT_API_BASE = NEURALDEEP_API_BASE_OPTIONS[0].value;

/** Canonical spelling of a stored api_base, or "" when it names no NeuralDeep endpoint. */
function matchNeuralDeepAPIBase(value: unknown): string {
  let want = String(value ?? "").trim();
  while (want.endsWith("/")) {
    want = want.slice(0, -1);
  }
  want = want.toLowerCase();
  return (
    NEURALDEEP_API_BASE_OPTIONS.find((o) => o.value.toLowerCase() === want)
      ?.value ?? ""
  );
}

type FieldOverrideContext = Parameters<FieldOverride>[0];

// Provider types with an account-usage source server-side
// (internal/session/provider_usage_sources.go). Only they get the
// usage_limits_panel switch, sitting beside the type picker.
const USAGE_PANEL_PROVIDER_TYPES = new Set(["neuraldeep", "codex", "devin"]);

// ProviderTypeField renders the type enum control like any enum field, plus
// the usage-panel switch on the right of it when the picked type has a usage
// source (the usage_limits_panel key itself is suppressed as a standalone
// field). The switch writes the sibling key through the override's setField.
function ProviderTypeField(props: { ctx: FieldOverrideContext }) {
  const { schema, value, onChange, parentObj, setField } = props.ctx;
  const label = schemaFieldLabel("providers", "type", schema.title, "type");
  const desc = schemaFieldDesc("providers", "type", schema.description);
  const fallback = defaultForSchema(schema);
  const v =
    value === undefined || value === null || value === ""
      ? fallback === undefined || fallback === null
        ? ""
        : String(fallback)
      : String(value);
  const usageLabel = schemaFieldLabel(
    "providers",
    "usage_limits_panel",
    undefined,
    "usage_limits_panel",
  );
  const usageDesc = schemaFieldDesc("providers", "usage_limits_panel", "");
  return (
    <div className="settings-row">
      <span className="settings-label">
        {label}
        {desc ? <FieldHint text={desc} /> : null}
      </span>
      <div className="settings-type-row">
        <Combobox
          value={v}
          ariaLabel={label}
          placeholder={schemaFieldPlaceholder("providers", "type")}
          options={(schema.enum ?? []).map((opt) => ({
            value: String(opt),
          }))}
          onChange={(raw) => {
            const match = schema.enum?.find((x) => String(x) === raw);
            onChange(match !== undefined ? match : raw);
          }}
        />
        {USAGE_PANEL_PROVIDER_TYPES.has(v) ? (
          <SwitchField
            checked={parentObj?.usage_limits_panel !== false}
            onChange={(nv) => setField?.("usage_limits_panel", nv)}
            label={usageLabel}
            description={usageDesc || undefined}
            dataTestId="provider-usage-limits-panel"
          />
        ) : null}
      </div>
    </div>
  );
}

function asObject(v: unknown): Record<string, unknown> {
  return v && typeof v === "object" && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : {};
}

function asArray(v: unknown): unknown[] {
  return Array.isArray(v) ? v : [];
}

function stringList(v: unknown, key: string): string[] {
  return asArray(v)
    .map((row) => {
      if (row && typeof row === "object" && !Array.isArray(row)) {
        const cell = (row as Record<string, unknown>)[key];
        return cell === undefined || cell === null ? "" : String(cell);
      }
      return "";
    })
    .filter((s) => s.trim() !== "");
}

function NeuralDeepAPIBaseField(props: { ctx: FieldOverrideContext }) {
  const { schema, value, onChange } = props.ctx;
  const { t } = useT();
  const label =
    schemaFieldLabel("providers", "api_base", schema.title, "api_base") ||
    t("settings.field.apiBaseFallback");
  const stored = String(value ?? "").trim();
  const matched = matchNeuralDeepAPIBase(value);

  // NeuralDeep speaks an OpenAI-compatible API at two official deployments:
  // api.neuraldeep.ru for Russia, api.neuraldeep.tech for everywhere else. Only
  // those are offered, and the choice also decides which hub mints the key for
  // the sign-in block below. Nothing is written until the user picks one, so a
  // base entered for another provider type survives switching to neuraldeep and
  // back; meanwhile the select shows the endpoint requests really use and the
  // note below explains why the stored value is not it.
  return (
    <div className="settings-row">
      <span className="settings-label">
        {label}
        <FieldHint text={t("neuralDeepApiBase.description")} />
      </span>
      <select
        className="settings-input"
        value={matched || NEURALDEEP_DEFAULT_API_BASE}
        aria-label={label}
        data-testid="neuraldeep-api-base"
        onChange={(e) => onChange(e.target.value)}
      >
        {NEURALDEEP_API_BASE_OPTIONS.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {t(opt.labelKey)}
          </option>
        ))}
      </select>
      {stored !== "" && matched === "" ? (
        <p className="settings-field-desc">
          {t("neuralDeepApiBase.unknown", {
            value: stored,
            fallback: NEURALDEEP_DEFAULT_API_BASE,
          })}
        </p>
      ) : null}
    </div>
  );
}

function neuralDeepAPIBaseOverride(ctx: FieldOverrideContext) {
  const providerType =
    ctx.parentObj?.type === undefined || ctx.parentObj.type === null
      ? ""
      : String(ctx.parentObj.type);
  if (ctx.path !== "api_base" || providerType !== "neuraldeep") {
    return null;
  }
  // The overrides stack: the endpoint picker keeps the api_base slot, and the
  // hub sign-in block renders below it. The manual api_key field above stays
  // fully functional - an explicit key wins over the stored login, which the
  // sign-in block reports instead of hiding.
  const providerName =
    ctx.parentObj?.name === undefined || ctx.parentObj.name === null
      ? ""
      : String(ctx.parentObj.name);
  const hasExplicitKey =
    String(ctx.parentObj?.api_key ?? "").trim() !== "" ||
    String(ctx.parentObj?.api_key_command ?? "").trim() !== "";
  // The endpoint requests really use for this row: the picked one, or the
  // default when the stored value names no NeuralDeep deployment.
  const apiBase =
    matchNeuralDeepAPIBase(ctx.value) || NEURALDEEP_DEFAULT_API_BASE;
  return (
    <>
      <NeuralDeepAPIBaseField ctx={ctx} />
      <NeuralDeepAuthField
        providerName={providerName}
        hasExplicitKey={hasExplicitKey}
        apiBase={apiBase}
      />
    </>
  );
}

// Every provider type has the proxy switch and URL field, codex included: its
// sign-in and its requests take the row's route like any other.
function providerProxyOverride(ctx: FieldOverrideContext) {
  return (
    <ProxySettingField
      value={ctx.value}
      onChange={ctx.onChange}
      label={schemaFieldLabel("providers", "proxy", ctx.schema.title, "proxy")}
      description={schemaFieldDesc(
        "providers",
        "proxy",
        ctx.schema.description,
      )}
    />
  );
}

// The Telegram bot's proxy reads like a provider's, so it gets the same
// switch and URL field (System tab, gateways block).
function gatewaysFieldOverride(ctx: FieldOverrideContext) {
  if (ctx.path !== "telegram.proxy") {
    return null;
  }
  return (
    <ProxySettingField
      value={ctx.value}
      onChange={ctx.onChange}
      label={schemaFieldLabel(
        "system.gateways",
        "telegram.proxy",
        ctx.schema.title,
        "proxy",
      )}
      description={schemaFieldDesc(
        "system.gateways",
        "telegram.proxy",
        ctx.schema.description,
      )}
      switchDescriptionKey="settings.gatewayProxy.ignoreSystemDesc"
    />
  );
}

function providerFieldOverride(ctx: FieldOverrideContext) {
  if (ctx.path === "proxy") {
    return providerProxyOverride(ctx);
  }
  if (ctx.path === "type") {
    return <ProviderTypeField ctx={ctx} />;
  }
  if (ctx.path === "usage_limits_panel") {
    // Not a standalone field - the "type" row carries it for the provider
    // types that have a usage source.
    return false;
  }
  const providerType =
    ctx.parentObj?.type === undefined || ctx.parentObj.type === null
      ? ""
      : String(ctx.parentObj.type);
  if (providerType === "codex") {
    if (ctx.path === "api_key" || ctx.path === "api_key_command") {
      return false;
    }
    if (ctx.path === "api_base") {
      const providerName =
        ctx.parentObj?.name === undefined || ctx.parentObj.name === null
          ? ""
          : String(ctx.parentObj.name);
      return <CodexAuthField providerName={providerName} />;
    }
  }
  return neuralDeepAPIBaseOverride(ctx);
}

/**
 * SettingsSection renders the active settings tab. Object sections render their
 * sub-schema fields directly (the tab already names the section); array sections
 * become master–detail lists; the System group stacks its child object sections;
 * Skills and Appearance are special tabs. Model fields receive custom editors via
 * the SchemaForm fieldOverride hook.
 */
export function SettingsSection(props: {
  section: SectionDescriptor;
  schema: JsonSchema;
  doc: Record<string, unknown>;
  setDoc: (next: Record<string, unknown>) => void;
  /** Desktop shows the edited item's name on the array-section back button. */
  isMobileShell?: boolean;
  /** The conversation on screen, so the session table can spare it. */
  activeSessionId?: string;
  /** Session ids the table removed, so the shell can drop them from History. */
  onSessionsDeleted?: (ids: string[]) => void;
  /** Workspace of the viewed session; the Subagents tab asks about it. */
  workspacePath?: string | undefined;
  onSessionTagsChanged?: (id: string, tags: string[]) => void;
}) {
  const {
    section,
    schema,
    doc,
    setDoc,
    activeSessionId,
    onSessionsDeleted,
    onSessionTagsChanged,
  } = props;
  const { t } = useT();
  const props_ = schema.properties ?? {};

  const providerRows = asArray(doc.providers) as ProviderRow[];
  // The provider row the model id points at, as it stands in the (unsaved)
  // form: its type decides the Codex reasoning remap server-side, so it must
  // come from the document being edited rather than from the config on disk.
  const providerTypeFor = (modelId: string): string | undefined => {
    const slash = modelId.indexOf("/");
    if (slash <= 0) {
      return undefined;
    }
    const name = modelId.slice(0, slash);
    const row = asArray(doc["providers"]).find(
      (p) => asObject(p)["name"] === name,
    );
    const type = row === undefined ? "" : String(asObject(row)["type"] ?? "");
    return type.trim() || undefined;
  };
  const modelIds = stringList(doc.models, "model");

  const setKey = (key: string, value: unknown) =>
    setDoc({ ...doc, [key]: value });

  if (section.kind === "appearance") {
    return <AppearanceThemePicker />;
  }

  // The session table is API-driven (/coddy/sessions): it reads and removes
  // stored bundles and never touches the settings document.
  if (section.kind === "sessions") {
    return (
      <SessionsManager
        {...(activeSessionId ? { activeSessionId } : {})}
        {...(onSessionsDeleted ? { onSessionsDeleted } : {})}
        {...(onSessionTagsChanged ? { onSessionTagsChanged } : {})}
      />
    );
  }

  if (section.kind === "skills") {
    const sub = props_.skills;
    if (!sub) {
      return (
        <p className="settings-muted">
          {t("settings.error.skillsSchemaUnavailable")}
        </p>
      );
    }
    return (
      <SkillsSection
        schema={sub}
        value={asObject(doc.skills)}
        onChange={(v) => setKey("skills", v)}
      />
    );
  }

  // The MCP tab is API-driven (/coddy/mcp*): toggles and project entries
  // persist into config.yaml / .coddy/mcp.json immediately, so it does not
  // edit the settings document at all.
  if (section.kind === "mcp") {
    return <MCPSection />;
  }

  // Subagents edits its config section like any object tab, and additionally
  // lists the definitions of the viewed session's workspace, read-only: a
  // project-scope one is approved from a terminal on the machine running
  // coddy (`coddy agents trust <name>`), and the list says so.
  if (section.kind === "subagents") {
    const sub = props_.subagents;
    if (!sub) {
      return (
        <p className="settings-muted">
          {t("settings.error.sectionSchemaUnavailable")}
        </p>
      );
    }
    return (
      <SubagentsSection
        schema={sub}
        value={asObject(doc.subagents)}
        onChange={(v) => setKey("subagents", v)}
        workspacePath={props.workspacePath}
      />
    );
  }

  const key = section.schemaKey ?? section.id;

  if (section.kind === "array") {
    const sub = props_[key];
    if (!sub) {
      return (
        <p className="settings-muted">
          {t("settings.error.sectionSchemaUnavailable")}
        </p>
      );
    }
    const override: FieldOverride | undefined =
      key === "models"
        ? (ctx) => {
            if (ctx.path === "model") {
              return (
                <ModelField
                  value={
                    ctx.value === undefined || ctx.value === null
                      ? ""
                      : String(ctx.value)
                  }
                  onChange={(v) => ctx.onChange(v)}
                  providers={providerRows}
                  label={
                    schemaFieldLabel(key, "model", ctx.schema.title, "model") ||
                    t("settings.field.modelIdFallback")
                  }
                />
              );
            }
            // The generic array editor cannot express "key absent" (auto-detect)
            // and cannot tell it apart from an explicit [] that hides the
            // reasoning selector, so this field owns all three states.
            if (ctx.path === "reasoning_levels") {
              const modelId =
                ctx.parentObj?.["model"] === undefined ||
                ctx.parentObj?.["model"] === null
                  ? ""
                  : String(ctx.parentObj["model"]);
              return (
                <ReasoningLevelsField
                  value={ctx.value}
                  onChange={(v) => ctx.onChange(v)}
                  model={modelId}
                  providerType={providerTypeFor(modelId)}
                  label={
                    schemaFieldLabel(
                      key,
                      "reasoning_levels",
                      ctx.schema.title,
                      "reasoning_levels",
                    ) || t("settings.reasoning.levelsFallback")
                  }
                  description={schemaFieldDesc(
                    key,
                    "reasoning_levels",
                    ctx.schema.description,
                  )}
                />
              );
            }
            return null;
          }
        : key === "providers"
          ? providerFieldOverride
          : undefined;
    // Renaming a logical model id must follow through to the default-model
    // references (agent.model / memory.model), or the saved config becomes
    // invalid ("not found in models list"). applyModelsChange reconciles them.
    const onArrayChange =
      key === "models"
        ? (v: unknown[]) => setDoc(applyModelsChange(doc, v))
        : (v: unknown[]) => setKey(key, v);
    const newItem =
      key === "models"
        ? () => {
            const seed = defaultForSchema(sub.items ?? {});
            if (
              seed === null ||
              typeof seed !== "object" ||
              Array.isArray(seed)
            ) {
              return seed;
            }
            // Empty reasoning_levels explicitly disables server-side detection.
            // A freshly added logical model has no such user choice yet, so omit
            // the optional override and let the backend resolve the model family.
            const { reasoning_levels: _reasoningLevels, ...model } =
              seed as Record<string, unknown>;
            return model;
          }
        : undefined;
    return (
      <SettingsArraySection
        schema={sub}
        value={asArray(doc[key])}
        onChange={onArrayChange}
        labelField={section.labelField}
        fieldOverride={override}
        newItem={newItem}
        backLabelUsesItemName={!props.isMobileShell}
        i18nDomain={section.id}
        advancedPaths={
          key === "providers"
            ? ["api_key_command", "proxy", "timeout_ms"]
            : undefined
        }
        itemExtra={
          key === "providers"
            ? (item, index) => (
                <ProviderModelsFetch
                  key={index}
                  provider={item}
                  existingModels={modelIds}
                  onAddModel={(id) => {
                    if (modelIds.includes(id)) {
                      return;
                    }
                    setDoc(
                      applyModelsChange(doc, [
                        ...asArray(doc.models),
                        { model: id },
                      ]),
                    );
                  }}
                  onRemoveModel={(id) => {
                    setDoc(
                      applyModelsChange(
                        doc,
                        asArray(doc.models).filter(
                          (m) => asObject(m).model !== id,
                        ),
                      ),
                    );
                  }}
                />
              )
            : undefined
        }
      />
    );
  }

  if (section.kind === "group") {
    const children = section.childKeys ?? [];
    return (
      <div className="settings-group">
        {children.map((ck) => {
          const sub = props_[ck];
          if (!sub) {
            return null;
          }
          return (
            <div key={ck} className="settings-group-block">
              <p className="appearance-section-label">
                {schemaFieldLabel("system", ck, sub.title, ck)}
              </p>
              <SchemaForm
                schema={sub}
                value={asObject(doc[ck])}
                onChange={(v) => setKey(ck, v)}
                fieldOverride={
                  ck === "gateways" ? gatewaysFieldOverride : undefined
                }
                i18nDomain={`system.${ck}`}
              />
            </div>
          );
        })}
      </div>
    );
  }

  // object section (agent, tools, memory, …)
  const sub = props_[key];
  if (!sub) {
    return (
      <p className="settings-muted">
        {t("settings.error.sectionSchemaUnavailable")}
      </p>
    );
  }
  const override: FieldOverride | undefined =
    key === "agent" || key === "memory" || key === "compaction"
      ? (ctx) =>
          ctx.path === "model" ? (
            <ModelPicker
              value={
                ctx.value === undefined || ctx.value === null
                  ? ""
                  : String(ctx.value)
              }
              onChange={(v) => ctx.onChange(v)}
              models={modelIds}
              label={
                schemaFieldLabel(key, "model", ctx.schema.title, "model") ||
                t("settings.field.defaultModelFallback")
              }
              description={schemaFieldDesc(
                key,
                "model",
                ctx.schema.description,
              )}
            />
          ) : null
      : undefined;
  return (
    <SchemaForm
      schema={sub}
      value={asObject(doc[key])}
      onChange={(v) => setKey(key, v)}
      fieldOverride={override}
      i18nDomain={section.id}
    />
  );
}

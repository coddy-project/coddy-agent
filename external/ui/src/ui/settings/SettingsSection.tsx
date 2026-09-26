import { AppearanceThemePicker } from "../theme/AppearanceModal";
import { applyModelsChange } from "./applyModelsChange";
import { CodexAuthField } from "./CodexAuthField";
import { FieldLabel } from "./FieldHint";
import { ContextWindowField } from "./ContextWindowField";
import { NeuralDeepAuthField } from "./NeuralDeepAuthField";
import { ModelField } from "./ModelField";
import { ModelPicker } from "./ModelPicker";
import { ProviderModelList } from "./ProviderModelList";
import type { ProviderRow } from "./useProviderModels";
import { ProxySettingField } from "./ProxySettingField";
import { ReasoningLevelsField } from "./ReasoningLevelsField";
import {
  defaultForSchema,
  SchemaForm,
  type FieldOverride,
  type JsonSchema,
  type SchemaFormGroup,
} from "./SchemaForm";
import { MCPSection } from "./MCPSection";
import { schemaFieldDesc, schemaFieldLabel } from "./schemaI18n";
import { SettingsArraySection } from "./SettingsArraySection";
import { SessionsManager } from "../sessions/SessionsManager";
import { SkillsSection } from "./SkillsSection";
import { SubagentsSection } from "./SubagentsSection";
import {
  SESSIONS_CONFIG_KEY,
  type SectionDescriptor,
} from "./settingsSections";
import { useT } from "../i18n/I18nProvider";
import { translate } from "../i18n/i18n";

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
// (internal/session/provider_usage_sources.go): the usage_limits_panel switch
// means something only for them, so the other types do not show it.
const USAGE_PANEL_PROVIDER_TYPES = new Set(["neuraldeep", "codex", "devin"]);

/**
 * seedLogicalModel builds a models[] row from the item schema's defaults,
 * carrying the given id and, when the provider reported one, its context
 * window. reasoning_levels is left out on purpose: an empty list would disable
 * the server-side detection, and a freshly added model has no such choice
 * yet. Both the Add button of Logical models and the provider form's model
 * list go through it, so a model added either way starts the same.
 */
function seedLogicalModel(
  itemSchema: JsonSchema | undefined,
  id: string,
  contextWindow?: number | undefined,
): Record<string, unknown> {
  const seed = defaultForSchema(itemSchema ?? {});
  const row: Record<string, unknown> =
    seed !== null && typeof seed === "object" && !Array.isArray(seed)
      ? { ...(seed as Record<string, unknown>) }
      : {};
  delete row.reasoning_levels;
  row.model = id;
  if (contextWindow && itemSchema?.properties?.max_context_tokens) {
    row.max_context_tokens = contextWindow;
  }
  return row;
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
      <FieldLabel
        label={label}
        description={t("neuralDeepApiBase.description")}
      />
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
// switch and URL field (Gateways tab, Telegram block).
function gatewaysFieldOverride(ctx: FieldOverrideContext) {
  if (ctx.path !== "telegram.proxy") {
    return null;
  }
  return (
    <ProxySettingField
      value={ctx.value}
      onChange={ctx.onChange}
      label={schemaFieldLabel(
        "gateways",
        "telegram.proxy",
        ctx.schema.title,
        "proxy",
      )}
      description={schemaFieldDesc(
        "gateways",
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
  const providerType =
    ctx.parentObj?.type === undefined || ctx.parentObj.type === null
      ? ""
      : String(ctx.parentObj.type);
  if (
    ctx.path === "usage_limits_panel" &&
    !USAGE_PANEL_PROVIDER_TYPES.has(providerType)
  ) {
    return false;
  }
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
 * become master–detail lists; the Prompts tab (id "system") stacks its child
 * object sections; Skills and Appearance are special tabs. Model fields receive custom editors via
 * the SchemaForm fieldOverride hook.
 */
export function SettingsSection(props: {
  section: SectionDescriptor;
  schema: JsonSchema;
  doc: Record<string, unknown>;
  setDoc: (next: Record<string, unknown>) => void;
  /** The row a list section has open, from the `?id=` of the address. */
  routeItem?: string | null | undefined;
  /** Puts the open row (its name, or null for the list) into the address. */
  onRouteItemChange?: ((item: string | null) => void) | undefined;
  /** The drawer's head already leads back to the list. */
  hideBackLink?: boolean | undefined;
  /** Told whether a row form of a list section is open. */
  onEditingChange?: ((editing: boolean) => void) | undefined;
  /** Each change closes an open row form (the head's back arrow). */
  closeSignal?: number | undefined;
  /** Each change says a copy of the config replaced the document. */
  replacedSignal?: number | undefined;
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

  const providerRows = asArray(doc.providers).map(asObject) as ProviderRow[];
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
  // stored bundles and never touches the settings document. Above it, once
  // the schema is in, the `sessions` config section (where the bundles are
  // stored) saves with Save all like any other.
  if (section.kind === "sessions") {
    const sub = props_[SESSIONS_CONFIG_KEY];
    return (
      <div className="settings-sessions-section">
        {sub ? (
          <SchemaForm
            schema={sub}
            value={asObject(doc[SESSIONS_CONFIG_KEY])}
            onChange={(v) => setKey(SESSIONS_CONFIG_KEY, v)}
            i18nDomain={SESSIONS_CONFIG_KEY}
            groups={[
              {
                id: "sessions-storage",
                legend: t("settings.group.sessions.storage"),
                description: t("settings.group.sessions.storageDesc"),
              },
            ]}
          />
        ) : null}
        <SessionsManager
          {...(activeSessionId ? { activeSessionId } : {})}
          {...(onSessionsDeleted ? { onSessionsDeleted } : {})}
          {...(onSessionTagsChanged ? { onSessionTagsChanged } : {})}
        />
      </div>
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
            // 0 means "whatever the provider's listing reports" at run time;
            // this field asks the provider, so the form shows that number.
            if (ctx.path === "max_context_tokens") {
              const modelId =
                ctx.parentObj?.["model"] === undefined ||
                ctx.parentObj?.["model"] === null
                  ? ""
                  : String(ctx.parentObj["model"]);
              const providerName = modelId.includes("/")
                ? modelId.slice(0, modelId.indexOf("/"))
                : "";
              return (
                <ContextWindowField
                  value={ctx.value}
                  onChange={(v) => ctx.onChange(v)}
                  model={modelId}
                  providerRow={providerRows.find(
                    (p) => String(p.name ?? "").trim() === providerName,
                  )}
                  label={schemaFieldLabel(
                    key,
                    "max_context_tokens",
                    ctx.schema.title,
                    "max_context_tokens",
                  )}
                  description={schemaFieldDesc(
                    key,
                    "max_context_tokens",
                    ctx.schema.description,
                  )}
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
      key === "models" ? () => seedLogicalModel(sub.items, "") : undefined;
    // Keyed by section: the list and row form are one component for every
    // array tab, and without the key a row form opened under LLM providers
    // would still be showing when the operator switched to Logical models.
    return (
      <SettingsArraySection
        key={key}
        schema={sub}
        value={asArray(doc[key])}
        onChange={onArrayChange}
        labelField={section.labelField}
        fieldOverride={override}
        newItem={newItem}
        backLabel={section.label}
        routeItem={props.routeItem ?? null}
        onRouteItemChange={props.onRouteItemChange}
        hideBackLink={props.hideBackLink}
        onEditingChange={props.onEditingChange}
        closeSignal={props.closeSignal}
        replacedSignal={props.replacedSignal}
        i18nDomain={section.id}
        groups={
          key === "providers"
            ? [
                { id: "provider", legend: t("settings.providers.group.main") },
                {
                  id: "advanced",
                  legend: t("settings.providers.group.advanced"),
                  paths: ["api_key_command", "proxy", "timeout_ms"],
                  collapsible: true,
                },
              ]
            : key === "models"
              ? [
                  // Which model it is and what it can take in, how it
                  // answers, and how hard it thinks.
                  {
                    id: "model",
                    legend: t("settings.models.group.model"),
                    paths: ["model", "max_context_tokens", "multimodal"],
                  },
                  {
                    id: "generation",
                    legend: t("settings.models.group.generation"),
                    paths: ["max_tokens", "temperature", "stream"],
                  },
                  {
                    id: "reasoning",
                    legend: t("settings.models.group.reasoning"),
                    paths: ["reasoning_levels", "reasoning_default"],
                  },
                ]
              : undefined
        }
        itemFooter={
          key === "providers"
            ? (item) => (
                <ProviderModelList
                  provider={item}
                  existingModels={modelIds}
                  onAddModel={(id, contextWindow) => {
                    if (modelIds.includes(id)) {
                      return;
                    }
                    setDoc(
                      applyModelsChange(doc, [
                        ...asArray(doc.models),
                        seedLogicalModel(
                          props_["models"]?.items,
                          id,
                          contextWindow,
                        ),
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
    // Each folded-in section is a fieldset named after it; a list or a nested
    // object of it (the instruction files, the Telegram gateway) is a block
    // of its own, which names itself.
    const children = section.childKeys ?? [];
    return (
      <div className="settings-group">
        {children.map((ck) => {
          const sub = props_[ck];
          if (!sub) {
            return null;
          }
          return (
            <SchemaForm
              key={ck}
              schema={sub}
              value={asObject(doc[ck])}
              onChange={(v) => setKey(ck, v)}
              i18nDomain={`system.${ck}`}
              groups={[
                {
                  id: ck,
                  legend: schemaFieldLabel("system", ck, sub.title, ck),
                  description: schemaFieldDesc("system", ck, sub.description),
                },
              ]}
            />
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
    key === "gateways"
      ? gatewaysFieldOverride
      : key === "agent" || key === "memory" || key === "compaction"
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
      groups={objectSectionGroups(key)}
    />
  );
}

/**
 * The fieldsets an object section's form is laid out in, fields that belong
 * together side by side. A section's `enable` opens the form outside them,
 * and its lists and nested objects are blocks of their own beside them.
 * Sections not listed stand as one list of fields.
 */
function objectSectionGroups(key: string): SchemaFormGroup[] | undefined {
  if (key === "agent") {
    return [
      {
        id: "turn",
        legend: translate("settings.group.agent.turn"),
        paths: ["model", "max_turns", "queue_mode"],
      },
      {
        id: "retries",
        legend: translate("settings.group.agent.retries"),
        paths: ["llm_retry_max", "llm_retry_base_ms", "llm_min_interval_ms"],
      },
      {
        id: "timeouts",
        legend: translate("settings.group.agent.timeouts"),
        paths: ["llm_first_token_timeout_ms", "llm_stream_idle_timeout_ms"],
      },
      {
        id: "loop",
        legend: translate("settings.group.agent.loop"),
        paths: [
          "loop_guard",
          "loop_tool_repeat_limit",
          "loop_stream_repeat_cycles",
          "loop_nudge_max",
        ],
      },
      {
        id: "limits",
        legend: translate("settings.group.agent.limits"),
        paths: ["wait_for_limit_reset", "wait_for_limit_reset_max_ms"],
      },
    ];
  }
  if (key === "compaction") {
    return [
      {
        id: "summary",
        legend: translate("settings.group.compaction.summary"),
        paths: ["threshold_percent", "keep_recent_turns", "model"],
      },
    ];
  }
  if (key === "tools") {
    return [
      {
        id: "permissions",
        legend: translate("settings.group.tools.permissions"),
        paths: ["permission_mode"],
      },
    ];
  }
  if (key === "memory") {
    return [
      {
        id: "model",
        legend: translate("settings.group.memory.model"),
        paths: ["model", "dir"],
      },
      {
        id: "runs",
        legend: translate("settings.group.memory.runs"),
        paths: ["wait_seconds", "timeout_seconds", "keep_runs"],
      },
      {
        id: "limits",
        legend: translate("settings.group.memory.limits"),
        paths: [
          "recall_max_turns",
          "persist_max_turns",
          "copilot_max_tokens",
          "max_search_hits",
        ],
      },
      {
        id: "instructions",
        legend: translate("settings.group.memory.instructions"),
        paths: ["additional_prompt", "additional_prompt_max_chars"],
      },
    ];
  }
  if (key === "scheduler") {
    return [{ id: "jobs", legend: translate("settings.group.scheduler.jobs") }];
  }
  if (key === "logger") {
    return [{ id: "logger", legend: translate("settings.group.logger.main") }];
  }
  if (key === "hooks") {
    // The switch opens the tab and the files list stands as a block of its
    // own; what is left is how a hook runs.
    return [
      {
        id: "hooks",
        legend: translate("settings.group.hooks.main"),
        description: translate("settings.group.hooks.mainDesc"),
      },
    ];
  }
  return undefined;
}

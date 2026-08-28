import { AppearanceThemePicker } from "../theme/AppearanceModal";
import { applyModelsChange } from "./applyModelsChange";
import { CodexAuthField } from "./CodexAuthField";
import { NeuralDeepAuthField } from "./NeuralDeepAuthField";
import { ModelField } from "./ModelField";
import { ModelPicker } from "./ModelPicker";
import { SchemaForm, type FieldOverride, type JsonSchema } from "./SchemaForm";
import { MCPSection } from "./MCPSection";
import {
  schemaFieldDesc,
  schemaFieldLabel,
} from "./schemaI18n";
import { SettingsArraySection } from "./SettingsArraySection";
import { SkillsSection } from "./SkillsSection";
import type { SectionDescriptor } from "./settingsSections";
import { useT } from "../i18n/I18nProvider";

const NEURALDEEP_API_BASE = "https://api.neuraldeep.ru/v1";

type FieldOverrideContext = Parameters<FieldOverride>[0];

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
  const { schema } = props.ctx;
  const { t } = useT();
  const label =
    schemaFieldLabel("providers", "api_base", schema.title, "api_base") ||
    t("settings.field.apiBaseFallback");
  const desc = schemaFieldDesc("providers", "api_base", schema.description);

  // NeuralDeep speaks an OpenAI-compatible API at a fixed endpoint; the base URL
  // is not user-configurable. Show it read-only (greyed) but do NOT persist it
  // into the config: leaving the stored api_base untouched preserves any value
  // entered for another provider type, so switching back to openai/anthropic
  // restores it. The backend pins the endpoint regardless (providerBaseURL).
  return (
    <div className="settings-row">
      <span className="settings-label">{label}</span>
      {desc ? <p className="settings-field-desc">{desc}</p> : null}
      <input
        className="settings-input"
        type="text"
        value={NEURALDEEP_API_BASE}
        aria-label={label}
        title={desc}
        readOnly
      />
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
  // The overrides stack: the read-only base URL keeps its slot, and the hub
  // sign-in block renders below it. The manual api_key field above stays
  // fully functional - an explicit key wins over the stored login, which the
  // sign-in block reports instead of hiding.
  const providerName =
    ctx.parentObj?.name === undefined || ctx.parentObj.name === null
      ? ""
      : String(ctx.parentObj.name);
  const hasExplicitKey =
    String(ctx.parentObj?.api_key ?? "").trim() !== "" ||
    String(ctx.parentObj?.api_key_command ?? "").trim() !== "";
  return (
    <>
      <NeuralDeepAPIBaseField ctx={ctx} />
      <NeuralDeepAuthField
        providerName={providerName}
        hasExplicitKey={hasExplicitKey}
      />
    </>
  );
}

function providerFieldOverride(ctx: FieldOverrideContext) {
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
}) {
  const { section, schema, doc, setDoc } = props;
  const { t } = useT();
  const props_ = schema.properties ?? {};

  const providerNames = stringList(doc.providers, "name");
  const modelIds = stringList(doc.models, "model");

  const setKey = (key: string, value: unknown) =>
    setDoc({ ...doc, [key]: value });

  if (section.kind === "appearance") {
    return <AppearanceThemePicker />;
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
        ? (ctx) =>
            ctx.path === "model" ? (
              <ModelField
                value={
                  ctx.value === undefined || ctx.value === null
                    ? ""
                    : String(ctx.value)
                }
                onChange={(v) => ctx.onChange(v)}
                providers={providerNames}
                label={
                  schemaFieldLabel(key, "model", ctx.schema.title, "model") ||
                  t("settings.field.modelIdFallback")
                }
              />
            ) : null
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
    return (
      <SettingsArraySection
        schema={sub}
        value={asArray(doc[key])}
        onChange={onArrayChange}
        labelField={section.labelField}
        fieldOverride={override}
        backLabelUsesItemName={!props.isMobileShell}
        i18nDomain={section.id}
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
    key === "agent" || key === "memory"
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
              description={schemaFieldDesc(key, "model", ctx.schema.description)}
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

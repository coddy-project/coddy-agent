import { AppearanceThemePicker } from "../theme/AppearanceModal";
import { ModelField } from "./ModelField";
import { ModelPicker } from "./ModelPicker";
import { SchemaForm, type FieldOverride, type JsonSchema } from "./SchemaForm";
import { SettingsArraySection } from "./SettingsArraySection";
import { SkillsSection } from "./SkillsSection";
import type { SectionDescriptor } from "./settingsSections";

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
      return <p className="settings-muted">Skills schema unavailable.</p>;
    }
    return (
      <SkillsSection
        schema={sub}
        value={asObject(doc.skills)}
        onChange={(v) => setKey("skills", v)}
      />
    );
  }

  const key = section.schemaKey ?? section.id;

  if (section.kind === "array") {
    const sub = props_[key];
    if (!sub) {
      return <p className="settings-muted">Section schema unavailable.</p>;
    }
    const override: FieldOverride | undefined =
      key === "models"
        ? (ctx) =>
            ctx.path === "model" ? (
              <ModelField
                value={ctx.value === undefined || ctx.value === null ? "" : String(ctx.value)}
                onChange={(v) => ctx.onChange(v)}
                providers={providerNames}
                label={ctx.schema.title || "Model id"}
              />
            ) : null
        : undefined;
    return (
      <SettingsArraySection
        schema={sub}
        value={asArray(doc[key])}
        onChange={(v) => setKey(key, v)}
        labelField={section.labelField}
        fieldOverride={override}
        backLabelUsesItemName={!props.isMobileShell}
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
              <p className="appearance-section-label">{sub.title || ck}</p>
              <SchemaForm
                schema={sub}
                value={asObject(doc[ck])}
                onChange={(v) => setKey(ck, v)}
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
    return <p className="settings-muted">Section schema unavailable.</p>;
  }
  const override: FieldOverride | undefined =
    key === "agent" || key === "memory"
      ? (ctx) =>
          ctx.path === "model" ? (
            <ModelPicker
              value={ctx.value === undefined || ctx.value === null ? "" : String(ctx.value)}
              onChange={(v) => ctx.onChange(v)}
              models={modelIds}
              label={ctx.schema.title || "Default model"}
              description={ctx.schema.description}
            />
          ) : null
      : undefined;
  return (
    <SchemaForm
      schema={sub}
      value={asObject(doc[key])}
      onChange={(v) => setKey(key, v)}
      fieldOverride={override}
    />
  );
}

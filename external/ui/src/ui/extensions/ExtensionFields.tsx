import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useExtensions } from "./ExtensionsProvider";
import { ownerValues, setOwnerValues } from "./settings";
import { SchemaForm, type JsonSchema } from "../settings/SchemaForm";
import type {
  Owned,
  SettingsField,
  SettingsFieldComponent,
  SettingValue,
} from "./contributions";

export function ExtensionFields(props: {
  section: string;
  /** Restricts the rows to one owner (an extension's own settings section). */
  owner?: string | undefined;
  doc: Record<string, unknown>;
  setDoc: (doc: Record<string, unknown>) => void;
}) {
  const context = useExtensions();
  return context ? (
    <RegisteredFields {...props} runtime={context.runtime} />
  ) : null;
}
function RegisteredFields({
  section,
  owner,
  doc,
  setDoc,
  runtime,
}: Parameters<typeof ExtensionFields>[0] & {
  runtime: NonNullable<ReturnType<typeof useExtensions>>["runtime"];
}) {
  const { fields } = useSyncExternalStore(runtime.subscribe, runtime.snapshot);
  const visible = fields.filter(
    (field) => field.section === section && (!owner || field.owner === owner),
  );
  const owners = [...new Set(visible.map((field) => field.owner))].sort();
  return (
    <>
      {owners.map((owner) => {
        const owned = visible.filter((field) => field.owner === owner);
        const scalars = owned.filter(
          (field): field is Owned<SettingsField> => field.type !== "component",
        );
        const components = owned.filter(
          (field) => field.type === "component",
        ) as Owned<SettingsFieldComponent>[];
        const value = { ...ownerValues(doc, owner) };
        for (const field of owned)
          if (!Object.hasOwn(value, field.id) && field.default !== undefined)
            value[field.id] = field.default;
        const write = (id: string, next: SettingValue | undefined) => {
          // Only changed fields are written; displaying defaults is not a save.
          const values = { ...ownerValues(doc, owner) };
          if (next === undefined) delete values[id];
          else values[id] = next;
          setDoc(setOwnerValues(doc, owner, values));
        };
        const schema: JsonSchema = {
          type: "object",
          properties: Object.fromEntries(
            scalars.map((field) => [
              field.id,
              {
                type: field.type,
                title: field.title,
                ...(field.description
                  ? { description: field.description }
                  : {}),
                ...(field.default !== undefined
                  ? { default: field.default }
                  : {}),
                ...(field.enum ? { enum: field.enum } : {}),
              },
            ]),
          ),
        };
        return (
          <fieldset
            key={owner}
            className="settings-fieldset"
            data-extension-settings={owner}
          >
            <legend>{owner}</legend>
            {scalars.length ? (
              <SchemaForm
                schema={schema}
                value={value}
                onChange={(next) => {
                  for (const field of scalars)
                    if (next[field.id] !== value[field.id])
                      write(
                        field.id,
                        next[field.id] as SettingValue | undefined,
                      );
                }}
              />
            ) : null}
            {components.map((field) => (
              <ComponentField
                key={field.key}
                field={field}
                value={value[field.id] as SettingValue | undefined}
                onChange={(next) => write(field.id, next ?? undefined)}
                onFail={(error) =>
                  runtime.failMessageRenderer(field.key, field.owner, error)
                }
              />
            ))}
          </fieldset>
        );
      })}
    </>
  );
}

/** A field the extension draws itself. It remounts when the stored value
 * changes under it (a Reload drops its draft), but not on its own writes. */
function ComponentField({
  field,
  value,
  onChange,
  onFail,
}: {
  field: Owned<SettingsFieldComponent>;
  value: SettingValue | undefined;
  onChange: (next: SettingValue | null) => void;
  onFail: (error: unknown) => void;
}) {
  const element = useRef<HTMLDivElement>(null);
  const mountedValue = useRef<SettingValue | undefined>(value);
  const cleanup = useRef<() => void>(() => {});
  const [generation, setGeneration] = useState(0);
  const current = useRef({ value, onChange });
  current.current = { value, onChange };
  useEffect(() => {
    const el = element.current;
    if (!el) return;
    const dispose = () => {
      try {
        Promise.resolve(cleanup.current()).catch(onFail);
      } catch (error) {
        onFail(error);
      }
      cleanup.current = () => {};
      el.replaceChildren();
    };
    try {
      const result = field.mount(
        el,
        field.api,
        Object.freeze({
          value: current.current.value,
          setValue: (next: SettingValue | null) => {
            mountedValue.current = next === null ? undefined : next;
            current.current.onChange(next);
          },
        }),
      );
      mountedValue.current = current.current.value;
      if (typeof result === "function") cleanup.current = result;
      else if (result && typeof result === "object" && !("then" in result))
        cleanup.current = () => result.dispose?.();
      else if (result !== undefined) {
        Promise.resolve(result).catch(() => {});
        throw new Error("Settings field mount must be synchronous");
      }
    } catch (error) {
      onFail(error);
    }
    return dispose;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [field.key, field.mount, field.api, generation]);
  useEffect(() => {
    if (mountedValue.current !== value) setGeneration((n) => n + 1);
  }, [value]);
  return (
    <div className="extension-settings-field">
      <span className="settings-field-title">{field.title}</span>
      <div ref={element} className="extension-settings-field-view" />
    </div>
  );
}

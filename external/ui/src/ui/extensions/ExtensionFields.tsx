import { useSyncExternalStore } from "react";
import { useExtensions } from "./ExtensionsProvider";
import { ownerValues, setOwnerValues } from "./settings";
import { SchemaForm, type JsonSchema } from "../settings/SchemaForm";

export function ExtensionFields(props: {
  section: string;
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
  doc,
  setDoc,
  runtime,
}: Parameters<typeof ExtensionFields>[0] & {
  runtime: NonNullable<ReturnType<typeof useExtensions>>["runtime"];
}) {
  const { fields } = useSyncExternalStore(runtime.subscribe, runtime.snapshot);
  const visible = fields.filter((field) => field.section === section);
  const owners = [...new Set(visible.map((field) => field.owner))].sort();
  return (
    <>
      {owners.map((owner) => {
        const owned = visible.filter((field) => field.owner === owner);
        const value = { ...ownerValues(doc, owner) };
        for (const field of owned)
          if (!Object.hasOwn(value, field.id) && field.default !== undefined)
            value[field.id] = field.default;
        const schema: JsonSchema = {
          type: "object",
          properties: Object.fromEntries(
            owned.map((field) => [
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
            <SchemaForm
              schema={schema}
              value={value}
              onChange={(next) => {
                // Only changed fields are written; displaying defaults is not a save.
                const values = { ...ownerValues(doc, owner) };
                for (const field of owned)
                  if (next[field.id] !== value[field.id])
                    values[field.id] = next[field.id];
                setDoc(setOwnerValues(doc, owner, values));
              }}
            />
          </fieldset>
        );
      })}
    </>
  );
}

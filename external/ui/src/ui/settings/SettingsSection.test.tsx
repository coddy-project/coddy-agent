import React from "react";
import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { SettingsSection } from "./SettingsSection";
import type { JsonSchema } from "./SchemaForm";
import type { SectionDescriptor } from "./settingsSections";

afterEach(cleanup);

const providersSection: SectionDescriptor = {
  id: "providers",
  label: "LLM providers",
  kind: "array",
  schemaKey: "providers",
  labelField: "name",
};

const rootSchema: JsonSchema = {
  type: "object",
  properties: {
    providers: {
      type: "array",
      title: "LLM providers",
      items: {
        type: "object",
        properties: {
          name: { type: "string", title: "Provider name" },
          type: {
            type: "string",
            title: "Provider type",
            enum: ["openai", "anthropic", "neuraldeep"],
          },
          api_base: { type: "string", title: "API base URL" },
          api_key: { type: "string", title: "API key" },
        },
        "x-coddy-property-order": ["name", "type", "api_base", "api_key"],
      },
    },
  },
};

function Harness(props: { provider?: Record<string, unknown> }) {
  const [doc, setDoc] = React.useState<Record<string, unknown>>({
    providers: [
      props.provider ?? {
        name: "neuraldeep",
        type: "neuraldeep",
        api_base: "",
        api_key: "",
      },
    ],
  });
  return (
    <SettingsSection
      section={providersSection}
      schema={rootSchema}
      doc={doc}
      setDoc={setDoc}
    />
  );
}

test("NeuralDeep provider shows a read-only API base URL pinned to the fixed endpoint", async () => {
  render(<Harness />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));

  const base = screen.getByLabelText("API base URL") as HTMLInputElement;
  await waitFor(() => {
    expect(base.value).toBe("https://api.neuraldeep.ru/v1");
  });
  expect(base.readOnly).toBe(true);

  // Editing is rejected: the field stays pinned to the fixed endpoint.
  fireEvent.change(base, { target: { value: "https://custom.example/v1" } });
  expect(base.value).toBe("https://api.neuraldeep.ru/v1");
});

test("switching type away from NeuralDeep restores the previously entered API base", async () => {
  render(
    <Harness
      provider={{
        name: "custom",
        type: "openai",
        api_base: "https://custom.example/v1",
        api_key: "",
      }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));

  // openai: the field is editable and shows the entered value.
  let base = screen.getByLabelText("API base URL") as HTMLInputElement;
  expect(base.readOnly).toBe(false);
  expect(base.value).toBe("https://custom.example/v1");

  // Switch to neuraldeep: field becomes read-only + pinned to the fixed endpoint,
  // and the stored value is not overwritten.
  const type = screen.getByLabelText("Provider type") as HTMLInputElement;
  fireEvent.change(type, { target: { value: "neuraldeep" } });
  base = screen.getByLabelText("API base URL") as HTMLInputElement;
  await waitFor(() => {
    expect(base.readOnly).toBe(true);
  });
  expect(base.value).toBe("https://api.neuraldeep.ru/v1");

  // Switch back to openai: the original value is restored.
  fireEvent.change(type, { target: { value: "openai" } });
  base = screen.getByLabelText("API base URL") as HTMLInputElement;
  await waitFor(() => {
    expect(base.readOnly).toBe(false);
  });
  expect(base.value).toBe("https://custom.example/v1");
});

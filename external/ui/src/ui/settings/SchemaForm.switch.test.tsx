import React from "react";
import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SchemaForm, type JsonSchema } from "./SchemaForm";

afterEach(cleanup);

// A minimal schema with a single boolean field, mirroring what UISchemaMap() emits
// for any on/off config option (e.g. skills.auto_discovery, gateways.telegram.enabled).
const boolSchema: JsonSchema = {
  type: "object",
  properties: {
    feature_flag: { type: "boolean", title: "Feature flag" },
  },
} as unknown as JsonSchema;

function Harness() {
  const [doc, setDoc] = React.useState<Record<string, unknown>>({
    feature_flag: false,
  });
  return <SchemaForm schema={boolSchema} value={doc} onChange={setDoc} />;
}

// Design parity with the Skills page: boolean settings must render as a switch
// toggle, not a raw checkbox.
test("boolean settings field renders a switch, not a checkbox", () => {
  const { container } = render(<Harness />);
  const sw = screen.getByRole("switch", { name: /feature flag/i });
  expect(sw).toBeTruthy();
  expect(sw.getAttribute("aria-checked")).toBe("false");
  expect(container.querySelector('input[type="checkbox"]')).toBeNull();
});

test("toggling the switch flips the boolean value", () => {
  render(<Harness />);
  fireEvent.click(screen.getByRole("switch", { name: /feature flag/i }));
  expect(
    screen
      .getByRole("switch", { name: /feature flag/i })
      .getAttribute("aria-checked"),
  ).toBe("true");
});

import { afterEach, beforeEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SchemaForm, type JsonSchema } from "./SchemaForm";
import { initLocale } from "../i18n/i18n";

afterEach(cleanup);
beforeEach(() => initLocale("en"));

// An enum whose empty value means something (tools.operator_command.multiplexer:
// "" runs the command directly) reads as that, not as a blank choice.
const schema = {
  type: "object",
  properties: {
    operator_command: {
      type: "object",
      title: "Operator commands",
      properties: {
        multiplexer: {
          type: "string",
          title: "Run inside",
          enum: ["", "tmux", "screen"],
        },
      },
    },
  },
} as unknown as JsonSchema;

test("an enum's empty value is named and its choices carry their labels", () => {
  render(
    <SchemaForm
      schema={schema}
      value={{ operator_command: {} }}
      onChange={() => {}}
      i18nDomain="tools"
    />,
  );
  const field = screen.getByLabelText("Run inside") as HTMLInputElement;
  expect(field.placeholder).toBe("Directly (no multiplexer)");
  fireEvent.focus(field);
  fireEvent.keyDown(field, { key: "ArrowDown" });
  expect(
    screen.getByText("Directly (no multiplexer)", { selector: "li *, li" }),
  ).toBeInTheDocument();
});

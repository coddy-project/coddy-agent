import React from "react";
import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SchemaForm, type JsonSchema } from "./SchemaForm";

afterEach(cleanup);

// Mirrors the shape produced by the Go UISchemaMap() for gateways.telegram so the
// Settings form is proven to render the Telegram section (nested object + arrays).
const gatewaysSchema: JsonSchema = {
  type: "object",
  properties: {
    gateways: {
      type: "object",
      title: "Messenger gateways",
      properties: {
        telegram: {
          type: "object",
          title: "Telegram",
          properties: {
            enabled: { type: "boolean", title: "Enabled" },
            token: { type: "string", title: "Bot token" },
            rich_messages: { type: "boolean", title: "Rich messages" },
            admins: { type: "array", title: "Admins", items: { type: "integer" } },
            default_isolation: {
              type: "string",
              title: "Default isolation",
              enum: ["individual", "shared", "admin"],
            },
            chats: {
              type: "array",
              title: "Per-chat overrides",
              items: {
                type: "object",
                properties: {
                  chat_id: { type: "integer", title: "Chat ID" },
                  access: { type: "string", title: "Access" },
                },
              },
            },
          },
          "x-coddy-property-order": [
            "enabled",
            "token",
            "rich_messages",
            "admins",
            "default_isolation",
            "chats",
          ],
        },
      },
    },
  },
} as unknown as JsonSchema;

test("settings form renders the Telegram gateway section", () => {
  function Harness() {
    const [doc, setDoc] = React.useState<Record<string, unknown>>({});
    return <SchemaForm schema={gatewaysSchema} value={doc} onChange={setDoc} />;
  }
  render(<Harness />);

  // The Telegram fields the user said were missing must now be present.
  expect(screen.getByLabelText("Bot token")).toBeTruthy();
  expect(screen.getByText("Rich messages")).toBeTruthy();
  expect(screen.getByText("Admins")).toBeTruthy();
  expect(screen.getByText("Per-chat overrides")).toBeTruthy();

  // The token is an editable input, not read-only.
  const token = screen.getByLabelText("Bot token") as HTMLInputElement;
  fireEvent.change(token, { target: { value: "123:abc" } });
  expect((screen.getByLabelText("Bot token") as HTMLInputElement).value).toBe("123:abc");
});

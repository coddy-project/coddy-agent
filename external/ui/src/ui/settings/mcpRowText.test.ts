import { expect, test } from "vitest";
import { statusTitle, targetLine } from "./mcpRowText";
import type { MCPServerRow } from "./mcpServerJson";

function row(over: Partial<MCPServerRow>): MCPServerRow {
  return {
    name: "srv",
    source: "yaml",
    origin: "config",
    transport: "stdio",
    status: "connected",
    enabled: true,
    trusted: false,
    readonly: false,
    tools: [],
    ...over,
  } as MCPServerRow;
}

test("targetLine joins command and args, falls back to url, then empty", () => {
  expect(targetLine(row({ command: "npx", args: ["-y", "srv"] }))).toBe(
    "npx -y srv",
  );
  expect(targetLine(row({ url: "https://mcp.example" }))).toBe(
    "https://mcp.example",
  );
  expect(targetLine(row({}))).toBe("");
});

test("statusTitle prefers the row error for error/unsupported states", () => {
  expect(statusTitle(row({ status: "error", error: "boom" }))).toBe("boom");
  expect(statusTitle(row({ status: "weird" as never, error: "why" }))).toBe(
    "why",
  );
});

test("statusTitle returns a non-empty label for every known status", () => {
  for (const status of [
    "connected",
    "error",
    "disabled",
    "needs_approval",
    "denied",
  ] as const) {
    expect(statusTitle(row({ status })).length).toBeGreaterThan(0);
  }
});

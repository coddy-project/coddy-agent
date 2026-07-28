import { expect, test } from "vitest";
import {
  MCP_SERVER_TEMPLATE,
  parseServerEntryJson,
  serverRowToEntryJson,
  validateMCPServerName,
} from "./mcpServerJson";

test("template parses as a valid entry", () => {
  const { entry, error } = parseServerEntryJson(MCP_SERVER_TEMPLATE);
  expect(error).toBeUndefined();
  expect(entry?.command).toBeTruthy();
});

test("name validation mirrors the backend rules", () => {
  expect(validateMCPServerName("files")).toBeNull();
  expect(validateMCPServerName("my-server")).toBeNull();
  expect(validateMCPServerName("")).toBeTruthy();
  expect(validateMCPServerName("  ")).toBeTruthy();
  // "__" is the server/tool namespace separator.
  expect(validateMCPServerName("a__b")).toBeTruthy();
  expect(validateMCPServerName("a b")).toBeTruthy();
  expect(validateMCPServerName("a/b")).toBeTruthy();
});

test("entry must be a JSON object with command or url", () => {
  expect(parseServerEntryJson("{broken").error).toBeTruthy();
  expect(parseServerEntryJson("[]").error).toBeTruthy();
  expect(parseServerEntryJson('"str"').error).toBeTruthy();
  expect(parseServerEntryJson("{}").error).toBeTruthy();
  expect(
    parseServerEntryJson('{"command":"npx"}').entry?.command,
  ).toBe("npx");
  expect(
    parseServerEntryJson('{"url":"https://x"}').entry?.url,
  ).toBe("https://x");
});

test("args must be strings, env must be a string map", () => {
  expect(parseServerEntryJson('{"command":"x","args":[1]}').error).toBeTruthy();
  expect(
    parseServerEntryJson('{"command":"x","env":{"A":1}}').error,
  ).toBeTruthy();
  const ok = parseServerEntryJson(
    '{"command":"x","args":["-y"],"env":{"A":"1"},"disabledTools":["t"]}',
  );
  expect(ok.error).toBeUndefined();
  expect(ok.entry?.args).toEqual(["-y"]);
  expect(ok.entry?.disabledTools).toEqual(["t"]);
});

test("serverRowToEntryJson round-trips through the parser", () => {
  const text = serverRowToEntryJson({
    name: "files",
    source: "local",
    origin: "project",
    transport: "stdio",
    command: "npx",
    args: ["-y", "pkg"],
    env: { TOKEN: "v" },
    enabled: false,
    status: "disabled",
    tools: [],
    disabled_tools: ["write"],
  });
  const { entry, error } = parseServerEntryJson(text);
  expect(error).toBeUndefined();
  expect(entry?.command).toBe("npx");
  expect(entry?.disabled).toBe(true);
  expect(entry?.disabledTools).toEqual(["write"]);
});

test("serverRowToEntryJson keeps remote transport, url, and headers", () => {
  const text = serverRowToEntryJson({
    name: "remote",
    source: "global",
    origin: "home",
    transport: "http",
    url: "https://mcp.example.com/mcp",
    headers: { Authorization: "Bearer tok" },
    enabled: true,
    status: "connected",
    tools: [],
  });
  const { entry, error } = parseServerEntryJson(text);
  expect(error).toBeUndefined();
  expect(entry?.type).toBe("http");
  expect(entry?.url).toBe("https://mcp.example.com/mcp");
  // Editing a remote server must not silently drop its auth headers.
  expect(entry?.headers).toEqual({ Authorization: "Bearer tok" });
});

test("originLabel names the owning file", async () => {
  const { originLabel } = await import("./mcpServerJson");
  expect(originLabel("config")).toBe("config.yaml");
  expect(originLabel("home")).toBe("~/.coddy/mcp.json");
  expect(originLabel("project")).toBe("./.coddy/mcp.json");
});

import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MCPSection } from "./MCPSection";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const listResponse = {
  object: "coddy.mcp_list",
  items: [
    {
      name: "files",
      source: "local",
      origin: "project",
      readonly: false,
      transport: "stdio",
      command: "npx",
      args: ["-y", "pkg"],
      enabled: true,
      status: "connected",
      tools: [
        { name: "read_file", description: "Read a file", enabled: true },
        { name: "write_file", description: "Write a file", enabled: false },
      ],
      disabled_tools: ["write_file"],
    },
    {
      name: "shared",
      source: "global",
      origin: "home",
      readonly: false,
      transport: "stdio",
      command: "shared-mcp",
      enabled: true,
      status: "connected",
      tools: [],
    },
    {
      name: "yamlsrv",
      source: "global",
      origin: "config",
      readonly: true,
      transport: "stdio",
      command: "global-mcp",
      enabled: false,
      status: "disabled",
      tools: [],
    },
  ],
};

function stubFetch() {
  const calls: Array<{ url: string; method: string }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      calls.push({ url: String(url), method: init?.method ?? "GET" });
      return Promise.resolve({ ok: true, json: async () => listResponse });
    }),
  );
  return calls;
}

test("renders merged servers with scope badges and per-origin locks", async () => {
  stubFetch();
  render(<MCPSection />);

  await waitFor(() => expect(screen.getByTestId("mcp-list")).toBeTruthy());

  // Local project server: enabled switch, edit and delete active.
  const toggle = screen.getByTestId("mcp-toggle-files");
  expect(toggle.getAttribute("aria-checked")).toBe("true");
  expect(
    (screen.getByTestId("mcp-edit-files") as HTMLButtonElement).disabled,
  ).toBe(false);
  expect(
    (screen.getByTestId("mcp-delete-files") as HTMLButtonElement).disabled,
  ).toBe(false);

  // Badges show the scope (global/local), not the owning file.
  const badges = Array.from(
    document.querySelectorAll(".skills-list-item-badge"),
  ).map((b) => b.textContent);
  expect(badges).toContain("local");
  expect(badges).toContain("global");
  expect(badges).not.toContain("config");
  expect(badges).not.toContain("project");

  // Global ~/.coddy/mcp.json server stays editable.
  expect(
    (screen.getByTestId("mcp-edit-shared") as HTMLButtonElement).disabled,
  ).toBe(false);
  expect(
    (screen.getByTestId("mcp-delete-shared") as HTMLButtonElement).disabled,
  ).toBe(false);

  // Config.yaml-defined server: switch works but edit/delete are locked.
  expect(
    (screen.getByTestId("mcp-edit-yamlsrv") as HTMLButtonElement).disabled,
  ).toBe(true);
  expect(
    (screen.getByTestId("mcp-delete-yamlsrv") as HTMLButtonElement).disabled,
  ).toBe(true);
  expect(
    screen.getByTestId("mcp-status-yamlsrv").className,
  ).toContain("is-disabled");
});

test("expanding a server shows per-tool switches reflecting disabled state", async () => {
  stubFetch();
  render(<MCPSection />);
  await waitFor(() => expect(screen.getByTestId("mcp-list")).toBeTruthy());

  fireEvent.click(screen.getByTestId("mcp-expand-files"));
  const tools = screen.getByTestId("mcp-tools-files");
  expect(tools.textContent).toContain("read_file");
  expect(
    screen.getByTestId("mcp-tool-toggle-files-read_file").getAttribute("aria-checked"),
  ).toBe("true");
  expect(
    screen.getByTestId("mcp-tool-toggle-files-write_file").getAttribute("aria-checked"),
  ).toBe("false");
});

test("tool switch posts the toggle endpoint", async () => {
  const calls = stubFetch();
  render(<MCPSection />);
  await waitFor(() => expect(screen.getByTestId("mcp-list")).toBeTruthy());

  fireEvent.click(screen.getByTestId("mcp-expand-files"));
  fireEvent.click(screen.getByTestId("mcp-tool-toggle-files-read_file"));

  await waitFor(() =>
    expect(
      calls.some(
        (c) => c.url === "/coddy/mcp/files/tools/read_file/disable" && c.method === "POST",
      ),
    ).toBe(true),
  );
});

test("Add server opens the JSON editor prefilled with the template", async () => {
  stubFetch();
  render(<MCPSection />);
  await waitFor(() => expect(screen.getByTestId("mcp-list")).toBeTruthy());

  fireEvent.click(screen.getByTestId("mcp-add-server"));
  const editor = screen.getByTestId("mcp-editor");
  expect(editor).toBeTruthy();
  const json = screen.getByTestId("mcp-editor-json") as HTMLTextAreaElement;
  expect(json.value).toContain('"command"');

  // The scope picker defaults to local.
  expect(
    (screen.getByTestId("mcp-editor-scope-local") as HTMLInputElement).checked,
  ).toBe(true);

  // An invalid name is rejected client-side before any request.
  fireEvent.change(screen.getByTestId("mcp-editor-name"), {
    target: { value: "bad__name" },
  });
  fireEvent.click(screen.getByTestId("mcp-editor-save"));
  await waitFor(() =>
    expect(document.querySelector(".mcp-editor .settings-error")?.textContent).toContain("__"),
  );
});

test("saving with the global scope PUTs scope=global", async () => {
  const calls = stubFetch();
  render(<MCPSection />);
  await waitFor(() => expect(screen.getByTestId("mcp-list")).toBeTruthy());

  fireEvent.click(screen.getByTestId("mcp-add-server"));
  fireEvent.change(screen.getByTestId("mcp-editor-name"), {
    target: { value: "new-global" },
  });
  fireEvent.click(screen.getByTestId("mcp-editor-scope-global"));
  fireEvent.click(screen.getByTestId("mcp-editor-save"));

  await waitFor(() =>
    expect(
      calls.some(
        (c) => c.url === "/coddy/mcp/new-global?scope=global" && c.method === "PUT",
      ),
    ).toBe(true),
  );
});

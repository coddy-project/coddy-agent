import { afterEach, expect, test } from "vitest";

import {
  buildPermissionToolPreview,
  buildToolCallPreview,
  permissionPromptToolName,
  toolCallTargetText,
} from "./permissionToolPreview";
import type { CoddyPermissionPayload } from "./permissionTypes";
import { setLocale } from "../i18n/i18n";

afterEach(() => setLocale("en"));

function payload(
  toolName: string,
  args: Record<string, unknown>,
): CoddyPermissionPayload {
  return {
    sessionId: "sess_x",
    toolCall: {
      toolCallId: "call_1",
      title: `Run: ${toolName}`,
      kind: toolName === "run_command" ? "run_command" : "write",
      content: [
        {
          type: "content",
          content: { type: "text", text: `Arguments: ${JSON.stringify(args)}` },
        },
      ],
    },
    options: [
      { optionId: "allow", name: "Allow", kind: "allow_once" },
      { optionId: "allow_always", name: "Allow always", kind: "allow_always" },
      { optionId: "reject", name: "Reject", kind: "reject_once" },
    ],
  };
}

test("uses the concrete Coddy tool name instead of the generic ACP kind", () => {
  expect(permissionPromptToolName(payload("apply_patch", {}))).toBe(
    "apply_patch",
  );
});

test("builds a command preview", () => {
  const preview = buildPermissionToolPreview(
    payload("run_command", { command: "npm test", timeout_seconds: 45 }),
  );
  expect(preview).toMatchObject({
    toolName: "run_command",
    title: "Run this command?",
    header: "Shell",
    meta: ["timeout 45s"],
    kind: "code",
    text: "npm test",
  });
});

test("localizes permission previews", () => {
  setLocale("ru");
  const preview = buildPermissionToolPreview(
    payload("run_command", { command: "npm test", timeout_seconds: 45 }),
  );

  expect(preview).toMatchObject({
    title: "Запустить эту команду?",
    header: "Оболочка",
    meta: ["тайм-аут 45 с"],
  });
});

test("builds compact previews for every filesystem mutation tool", () => {
  expect(
    buildPermissionToolPreview(
      payload("write", { path: "src/a.ts", content: "hello" }),
    ),
  ).toMatchObject({
    title: "Write this file?",
    header: "src/a.ts",
    kind: "code",
    text: "hello",
  });
  expect(
    buildToolCallPreview({
      title: "write_file",
      argsText: JSON.stringify({ filePath: "src/b.ts", content: "world" }),
    }),
  ).toMatchObject({
    title: "Write this file?",
    header: "src/b.ts",
    kind: "code",
    text: "world",
  });
  expect(
    buildPermissionToolPreview(
      payload("mkdir", { path: "src/new", parents: true }),
    ),
  ).toMatchObject({
    title: "Create this directory?",
    header: "src/new",
    meta: ["create parents"],
  });
  expect(
    buildPermissionToolPreview(
      payload("touch", { path: "src/a.ts", create_parents: false }),
    ),
  ).toMatchObject({
    title: "Create or update this file?",
    header: "src/a.ts",
    meta: ["existing parents only"],
  });
  expect(
    buildPermissionToolPreview(
      payload("mv", { src: "src/a.ts", dst: "src/b.ts" }),
    ),
  ).toMatchObject({
    title: "Move this path?",
    kind: "move",
    sourcePath: "src/a.ts",
    destinationPath: "src/b.ts",
  });
  expect(
    buildPermissionToolPreview(
      payload("rm", { path: "build", recursive: true }),
    ),
  ).toMatchObject({
    title: "Remove this directory tree?",
    header: "build",
    meta: ["recursive"],
  });
  expect(
    buildPermissionToolPreview(payload("rmdir", { path: "empty" })),
  ).toMatchObject({
    title: "Remove this empty directory?",
    header: "empty",
  });
});

test("apply_patch keeps context and colored add/delete line data", () => {
  const patch = [
    "--- a/src/app.ts",
    "+++ b/src/app.ts",
    "@@ -10,4 +10,4 @@",
    " before();",
    "-oldValue();",
    "+newValue();",
    " after();",
  ].join("\n");
  const preview = buildPermissionToolPreview(
    payload("apply_patch", { path: "src/app.ts", patch }),
  );
  expect(preview.title).toBe("Apply this patch?");
  expect(preview.header).toBe("src/app.ts");
  expect(preview.kind).toBe("diff");
  if (preview.kind !== "diff") throw new Error("expected diff preview");
  expect(
    preview.lines.map((line) => [
      line.kind,
      line.oldNo,
      line.newNo,
      line.content,
    ]),
  ).toEqual([
    ["ctx", 10, 10, "before();"],
    ["del", 11, null, "oldValue();"],
    ["add", null, 11, "newValue();"],
    ["ctx", 12, 12, "after();"],
  ]);
});

test("edit shows unchanged lines around the replacement as diff context", () => {
  const preview = buildPermissionToolPreview(
    payload("edit", {
      path: "src/app.ts",
      oldString: "before();\noldValue();\nafter();",
      newString: "before();\nnewValue();\nafter();",
    }),
  );
  expect(preview.title).toBe("Edit this file?");
  expect(preview.kind).toBe("diff");
  if (preview.kind !== "diff") throw new Error("expected diff preview");
  expect(preview.lines.map((line) => line.kind)).toEqual([
    "ctx",
    "del",
    "add",
    "ctx",
  ]);
});

test("builds readable transcript previews for read-only Coddy tools", () => {
  expect(
    buildToolCallPreview({
      title: "read",
      argsText: JSON.stringify({ path: "src/app.ts", offset: 20, limit: 40 }),
    }),
  ).toMatchObject({
    header: "src/app.ts",
    meta: ["from line 20", "40 lines"],
    kind: "path",
  });

  expect(
    buildToolCallPreview({
      title: "grep",
      argsText: JSON.stringify({
        pattern: "permission-preview",
        path: "external/ui/src",
        glob: "*.tsx",
        max_results: 25,
      }),
    }),
  ).toMatchObject({
    header: "external/ui/src",
    meta: ["*.tsx", "max 25"],
    kind: "code",
    text: "permission-preview",
  });
});

test("builds a distinct preview for the plan-to-agent transition", () => {
  expect(
    buildToolCallPreview({ title: "plan_exit", argsText: "{}" }),
  ).toMatchObject({
    toolName: "plan_exit",
    header: "Agent mode",
    meta: [],
    copyText: "",
    kind: "plan_exit",
  });
});

test("localizes the plan-to-agent transition preview", () => {
  setLocale("ru");

  expect(buildToolCallPreview({ title: "plan_exit" })).toMatchObject({
    header: "Агентский режим",
    kind: "plan_exit",
  });
});

test("toolCallTargetText names the one thing each call acts on", () => {
  expect(
    toolCallTargetText({ title: "read", argsText: '{"path":"a/b.ts"}' }),
  ).toBe("a/b.ts");
  expect(
    toolCallTargetText({
      title: "run_command",
      argsText: '{"command":"go test ./..."}',
    }),
  ).toBe("go test ./...");
  expect(
    toolCallTargetText({
      title: "ssh_run_command",
      argsText: '{"command":"uptime","host":"box"}',
    }),
  ).toBe("uptime");
  expect(
    toolCallTargetText({ title: "grep", argsText: '{"pattern":"TODO"}' }),
  ).toBe("TODO");
  expect(
    toolCallTargetText({ title: "websearch", argsText: '{"query":"go slog"}' }),
  ).toBe("go slog");
  expect(
    toolCallTargetText({
      title: "mv",
      argsText: '{"src":"a.ts","dst":"b.ts"}',
    }),
  ).toBe("a.ts");
  expect(
    toolCallTargetText({
      title: "webfetch",
      argsText: '{"url":"https://x.dev"}',
    }),
  ).toBe("https://x.dev");
});

test("toolCallTargetText stays empty when there is nothing to name", () => {
  // A question carries its prompt, not a target; a call whose arguments have not
  // streamed in yet has to render as a bare verb rather than as "undefined".
  expect(
    toolCallTargetText({
      title: "question",
      argsText: '{"question":"which?"}',
    }),
  ).toBe("");
  expect(toolCallTargetText({ title: "read" })).toBe("");
  expect(toolCallTargetText({ title: "read", argsText: "not json" })).toBe("");
});

test("toolCallTargetText accepts the Run: prefix and the Arguments: envelope", () => {
  expect(
    toolCallTargetText({
      title: "Run: run_command",
      argsText: 'Arguments: {"command":"make lint"}',
    }),
  ).toBe("make lint");
});

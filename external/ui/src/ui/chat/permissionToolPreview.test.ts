import { afterEach, expect, test } from "vitest";

import {
  buildPermissionToolPreview,
  buildToolCallPreview,
  permissionPromptToolName,
  toolCallTargetIsPath,
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
    kind: "shell",
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
    header: "Агентный режим",
    kind: "plan_exit",
  });
});

test("toolCallTargetIsPath tells a path apart from a command, a pattern or a name", () => {
  // Only a path may be respelt against the session directory; rewriting a command
  // or a url would change what the row claims the call did.
  for (const context of [
    { title: "read", argsText: '{"path":"/repo/a/b.ts"}' },
    { title: "write", argsText: '{"file_path":"/repo/a/b.ts"}' },
    { title: "edit", argsText: '{"filePath":"/repo/a/b.ts"}' },
    { title: "mv", argsText: '{"src":"/repo/a.ts","dst":"/repo/b.ts"}' },
  ]) {
    expect(toolCallTargetIsPath(context), context.title).toBe(true);
  }

  for (const context of [
    { title: "run_command", argsText: '{"command":"/usr/bin/make docs"}' },
    { title: "grep", argsText: '{"pattern":"/etc/hosts"}' },
    { title: "glob", argsText: '{"pattern":"**/*.go"}' },
    { title: "webfetch", argsText: '{"url":"https://coddy.dev/"}' },
    { title: "load_skill", argsText: '{"name":"rpa-bugfix"}' },
    { title: "spawn_agent", argsText: '{"agent":"explore"}' },
    // A documentation page is named like a path but lives in the binary.
    { title: "coddy_docs_read", argsText: '{"page":"features/mentions"}' },
    { title: "read", argsText: "" },
  ]) {
    expect(toolCallTargetIsPath(context), context.title).toBe(false);
  }
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
    toolCallTargetText({
      title: "spawn_agent",
      argsText: '{"agent":"reviewer","prompt":"review the diff"}',
    }),
  ).toBe("reviewer");
  expect(
    toolCallTargetText({ title: "grep", argsText: '{"pattern":"TODO"}' }),
  ).toBe("TODO");
  expect(
    toolCallTargetText({ title: "websearch", argsText: '{"query":"go slog"}' }),
  ).toBe("go slog");
  expect(
    toolCallTargetText({ title: "coddy_docs_search", argsText: '{"query":"telegram proxy"}' }),
  ).toBe("telegram proxy");
  expect(
    toolCallTargetText({
      title: "coddy_docs_read",
      argsText: '{"page":"features/mentions#completion","offset":40}',
    }),
  ).toBe("features/mentions#completion");
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

test("an http_request names its method and address, never a path", () => {
  const context = {
    title: "http_request",
    argsText:
      '{"method":"patch","url":"https://api.x.dev/items/7","body_file":"src/a.json"}',
  };
  expect(toolCallTargetText(context)).toBe("PATCH https://api.x.dev/items/7");
  expect(
    toolCallTargetText({
      title: "http_request",
      argsText: '{"url":"http://localhost:8080/health"}',
    }),
  ).toBe("http://localhost:8080/health");
  expect(toolCallTargetIsPath(context)).toBe(false);
  expect(buildToolCallPreview(context).title).toBe("Send this HTTP request?");
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

test("a call with no arguments renders as an action card, never as `{}`", () => {
  const preview = buildToolCallPreview(
    { title: "coddy_todo_plan_archive", argsText: "{}" },
    "{}",
  );
  expect(preview).toMatchObject({
    toolName: "coddy_todo_plan_archive",
    header: "archiving the plan",
    meta: [],
    copyText: "",
    kind: "action",
  });
});

test("the action card names an uncatalogued no-argument tool by its own id", () => {
  expect(
    buildToolCallPreview({ title: "mcp__github__list_repos" }, ""),
  ).toMatchObject({ header: "mcp__github__list_repos", kind: "action" });
});

test("localizes the action card", () => {
  setLocale("ru");
  expect(
    buildToolCallPreview({ title: "coddy_todo_plan_archive" }, "{}"),
  ).toMatchObject({ header: "архивирую план", kind: "action" });
});

test("arguments that are not an empty object keep the readable fallback", () => {
  expect(
    buildToolCallPreview({ title: "keep_result" }, "raw tool detail"),
  ).toMatchObject({ kind: "code", text: "raw tool detail" });
});

test("a shell call is its own preview kind so the command can carry a prompt", () => {
  expect(
    buildToolCallPreview({
      title: "run_command",
      argsText: JSON.stringify({ command: "npm test", timeout_seconds: 45 }),
    }),
  ).toMatchObject({
    header: "Shell",
    meta: ["timeout 45s"],
    copyText: "npm test",
    kind: "shell",
    text: "npm test",
  });
  expect(
    buildToolCallPreview({
      title: "ssh_run_command",
      argsText: JSON.stringify({ command: "uptime" }),
    }),
  ).toMatchObject({ header: "SSH shell", kind: "shell", text: "uptime" });
});

test("load_skill previews the skill it pulls in, not its JSON arguments", () => {
  expect(
    buildToolCallPreview({
      title: "load_skill",
      argsText: JSON.stringify({ name: "/code-review" }),
    }),
  ).toMatchObject({
    toolName: "load_skill",
    header: "code-review",
    copyText: "code-review",
    kind: "path",
  });
});

test("the `Arguments:` envelope of an empty object is still an action card", () => {
  expect(
    buildToolCallPreview(
      { title: "coddy_todo_plan_read", argsText: "Arguments: {}" },
      "Arguments: {}",
    ),
  ).toMatchObject({ header: "reading the plan", kind: "action" });
});

test("an empty object is empty however it is spelled, but broken JSON is not", () => {
  expect(
    buildToolCallPreview({ title: "plan_list", argsText: "{ }" }, "{ }"),
  ).toMatchObject({ kind: "action" });
  expect(
    buildToolCallPreview({ title: "plan_list", argsText: "{\n}" }, "{\n}"),
  ).toMatchObject({ kind: "action" });
  // A truncated history preview still has something to show, so it stays a body.
  expect(
    buildToolCallPreview(
      { title: "mcp__ops__deploy", argsText: '{"target":"pro' },
      '{"target":"pro',
    ),
  ).toMatchObject({ kind: "code", text: '{"target":"pro' });
});

// The config family read as bare verbs: "changing the config" with nothing beside
// it, while every neighbouring row names what it touched. A dotted config path is
// also not a filesystem path, so it must never be respelt against the session
// directory.
test("config calls name the key or the command they act on", () => {
  expect(
    toolCallTargetText({
      title: "config_get",
      argsText: '{"path":"scheduler.enable"}',
    }),
  ).toBe("scheduler.enable");
  expect(
    toolCallTargetText({
      title: "config_set",
      argsText: '{"commands":["set scheduler.enable=true"]}',
    }),
  ).toBe("set scheduler.enable=true");
  expect(
    toolCallTargetText({
      title: "config_set",
      argsText:
        '{"commands":["set scheduler.enable=true","set agent.max_turns=20"]}',
    }),
  ).toBe("set scheduler.enable=true, set agent.max_turns=20");
  expect(
    toolCallTargetText({
      title: "config_revert",
      argsText: '{"path":"scheduler.enable"}',
    }),
  ).toBe("scheduler.enable");
  // config_commit, config_changes and config_rollback take no arguments at all.
  expect(toolCallTargetText({ title: "config_commit", argsText: "{}" })).toBe("");

  for (const context of [
    { title: "config_get", argsText: '{"path":"scheduler.enable"}' },
    { title: "config_set", argsText: '{"commands":["set a.b=1"]}' },
    { title: "config_revert", argsText: '{"path":"skills.dirs.0"}' },
  ]) {
    expect(toolCallTargetIsPath(context), context.title).toBe(false);
  }
});

// The config family used to fall through to the generic branch and print its own
// arguments back as a JSON object, next to a row that named nothing at all.
test("config calls preview the key or the staged commands, not their JSON", () => {
  expect(
    buildToolCallPreview({
      title: "config_get",
      argsText: JSON.stringify({ path: "scheduler.enable" }),
    }),
  ).toMatchObject({
    header: "scheduler.enable",
    meta: [],
    kind: "path",
  });

  expect(
    buildToolCallPreview({
      title: "config_revert",
      argsText: JSON.stringify({ path: "skills.dirs.0" }),
    }),
  ).toMatchObject({ header: "skills.dirs.0", kind: "path" });

  expect(
    buildToolCallPreview({
      title: "config_set",
      argsText: JSON.stringify({
        commands: ["set scheduler.enable=true", "set agent.max_turns=20"],
      }),
    }),
  ).toMatchObject({
    header: "",
    kind: "code",
    text: "set scheduler.enable=true\nset agent.max_turns=20",
  });

  // config_commit and its no-argument siblings keep naming the action instead.
  expect(
    buildToolCallPreview({ title: "config_commit", argsText: "{}" }),
  ).toMatchObject({ kind: "action" });
});

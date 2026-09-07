import assert from "node:assert/strict"
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises"
import os from "node:os"
import path from "node:path"
import test from "node:test"

import {
  createProjectRulesHooks,
  globToRegExp,
} from "../lib/project-rules.js"
import { ProjectRulesPlugin } from "../plugins/project-rules.js"

async function fixture(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), "coddy-opencode-rules-"))
  t.after(() => rm(root, { recursive: true, force: true }))

  const rulesDir = path.join(root, ".cursor", "rules")
  await mkdir(rulesDir, { recursive: true })
  await writeFile(
    path.join(rulesDir, "always.mdc"),
    [
      "---",
      "description: Always-on rule",
      "globs: **/*.go",
      "alwaysApply: true",
      "---",
      "Always follow the repository workflow.",
    ].join("\n"),
  )
  await writeFile(
    path.join(rulesDir, "http.mdc"),
    [
      "---",
      "description: HTTP rule",
      "globs: external/httpserver/**/*.go, docs/http-*.md",
      "alwaysApply: false",
      "---",
      "Keep the served OpenAPI document aligned with HTTP handlers.",
    ].join("\n"),
  )
  return root
}

async function systemText(hooks, sessionID) {
  const output = { system: [] }
  await hooks["experimental.chat.system.transform"](
    { sessionID, model: {} },
    output,
  )
  return output.system.join("\n")
}

test("Cursor globs match files directly below a recursive directory", () => {
  const pattern = globToRegExp("external/httpserver/**/*.go")

  assert.equal(pattern.test("external/httpserver/server.go"), true)
  assert.equal(pattern.test("external/httpserver/routes/models.go"), true)
  assert.equal(pattern.test("external/ui/src/App.tsx"), false)
})

test("alwaysApply rules enter every OpenCode system prompt", async (t) => {
  const root = await fixture(t)
  const hooks = await createProjectRulesHooks({ repoRoot: root, directory: root })

  const system = await systemText(hooks, "session-always")

  assert.match(system, /Always follow the repository workflow/)
  assert.doesNotMatch(system, /Keep the served OpenAPI document aligned/)
})

test("the project plugin entrypoint creates the OpenCode hooks", async (t) => {
  const root = await fixture(t)

  const hooks = await ProjectRulesPlugin({ directory: root, worktree: root })

  assert.equal(typeof hooks["experimental.chat.system.transform"], "function")
  assert.equal(typeof hooks["tool.execute.before"], "function")
})

test("reading a governed file activates its scoped rules", async (t) => {
  const root = await fixture(t)
  const hooks = await createProjectRulesHooks({ repoRoot: root, directory: root })

  await hooks["tool.execute.before"](
    { tool: "read", sessionID: "session-read", callID: "call-1" },
    { args: { filePath: path.join(root, "external", "httpserver", "server.go") } },
  )

  const system = await systemText(hooks, "session-read")
  assert.match(system, /Keep the served OpenAPI document aligned/)
})

test("the first direct edit is retried after scoped rules are activated", async (t) => {
  const root = await fixture(t)
  const hooks = await createProjectRulesHooks({ repoRoot: root, directory: root })
  const input = { tool: "edit", sessionID: "session-edit", callID: "call-1" }
  const output = { args: { filePath: "external/httpserver/server.go" } }

  await assert.rejects(
    () => hooks["tool.execute.before"](input, output),
    /Project rules were activated.*Retry the edit/s,
  )
  assert.match(
    await systemText(hooks, "session-edit"),
    /Keep the served OpenAPI document aligned/,
  )
  await assert.doesNotReject(() => hooks["tool.execute.before"](input, output))
})

test("apply_patch paths activate every matching scoped rule", async (t) => {
  const root = await fixture(t)
  const hooks = await createProjectRulesHooks({ repoRoot: root, directory: root })
  const patch = [
    "*** Begin Patch",
    "*** Update File: docs/http-api.md",
    "@@",
    "-old",
    "+new",
    "*** End Patch",
  ].join("\n")

  await assert.rejects(
    () =>
      hooks["tool.execute.before"](
        { tool: "apply_patch", sessionID: "session-patch", callID: "call-1" },
        { args: { patch } },
      ),
    /Project rules were activated/,
  )
  assert.match(
    await systemText(hooks, "session-patch"),
    /Keep the served OpenAPI document aligned/,
  )
})

test("deleted sessions release their scoped rule state", async (t) => {
  const root = await fixture(t)
  const hooks = await createProjectRulesHooks({ repoRoot: root, directory: root })
  const toolInput = { tool: "read", sessionID: "session-delete", callID: "call-1" }
  const toolOutput = { args: { filePath: "external/httpserver/server.go" } }
  await hooks["tool.execute.before"](toolInput, toolOutput)
  assert.match(
    await systemText(hooks, "session-delete"),
    /Keep the served OpenAPI document aligned/,
  )

  await hooks.event({
    event: {
      type: "session.deleted",
      properties: { info: { id: "session-delete" } },
    },
  })

  assert.doesNotMatch(
    await systemText(hooks, "session-delete"),
    /Keep the served OpenAPI document aligned/,
  )
})

test("compaction receives active scoped rules", async (t) => {
  const root = await fixture(t)
  const hooks = await createProjectRulesHooks({ repoRoot: root, directory: root })
  await hooks["tool.execute.before"](
    { tool: "read", sessionID: "session-compact", callID: "call-1" },
    { args: { filePath: "external/httpserver/server.go" } },
  )
  const output = { context: [] }

  await hooks["experimental.session.compacting"](
    { sessionID: "session-compact" },
    output,
  )

  assert.match(
    output.context.join("\n"),
    /Keep the served OpenAPI document aligned/,
  )
})

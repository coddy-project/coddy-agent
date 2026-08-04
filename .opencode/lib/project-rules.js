import { readdir, readFile } from "node:fs/promises"
import path from "node:path"

const PATCH_FILE_RE = /^\*\*\*\s+(?:Add|Update|Delete)\s+File:\s*(.+?)\s*$/gm
const PATCH_MOVE_RE = /^\*\*\*\s+Move to:\s*(.+?)\s*$/gm
const MUTATING_TOOLS = new Set([
  "apply_patch",
  "edit",
  "multiedit",
  "multi_edit",
  "patch",
  "write",
])
const PATH_KEYS = new Set([
  "destination",
  "destinationpath",
  "file",
  "filename",
  "filepath",
  "path",
  "paths",
  "target",
  "targetpath",
])

function escapeRegExp(character) {
  return character.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
}

export function globToRegExp(pattern) {
  const normalized = pattern.replaceAll("\\", "/")
  const chunks = []
  let index = 0

  while (index < normalized.length) {
    if (normalized.startsWith("**/", index)) {
      chunks.push("(?:.*/)?")
      index += 3
    } else if (normalized.startsWith("**", index)) {
      chunks.push(".*")
      index += 2
    } else if (normalized[index] === "*") {
      chunks.push("[^/]*")
      index += 1
    } else if (normalized[index] === "?") {
      chunks.push("[^/]")
      index += 1
    } else {
      chunks.push(escapeRegExp(normalized[index]))
      index += 1
    }
  }

  return new RegExp(`^${chunks.join("")}$`)
}

function unquote(value) {
  const trimmed = value.trim()
  if (
    trimmed.length >= 2 &&
    ((trimmed.startsWith('"') && trimmed.endsWith('"')) ||
      (trimmed.startsWith("'") && trimmed.endsWith("'")))
  ) {
    return trimmed.slice(1, -1)
  }
  return trimmed
}

function parseGlobs(value) {
  const trimmed = value.trim().replace(/^\[/, "").replace(/\]$/, "")
  return trimmed
    .split(",")
    .map(unquote)
    .filter(Boolean)
}

export function parseRule(filePath, text, repoRoot) {
  const frontmatter = text.match(
    /^---\s*\r?\n([\s\S]*?)\r?\n---\s*(?:\r?\n|$)([\s\S]*)$/,
  )
  if (!frontmatter) return undefined

  let description = ""
  let globs = []
  let always = false
  for (const line of frontmatter[1].split(/\r?\n/)) {
    const separator = line.indexOf(":")
    if (separator < 0) continue
    const key = line.slice(0, separator).trim()
    const value = line.slice(separator + 1).trim()
    if (key === "description") description = unquote(value)
    if (key === "globs") globs = parseGlobs(value)
    if (key === "alwaysApply") always = value.toLowerCase() === "true"
  }

  const body = frontmatter[2].trim()
  if (!body) return undefined
  return {
    always,
    body,
    description,
    globs,
    path: path.relative(repoRoot, filePath).replaceAll("\\", "/"),
  }
}

export async function loadRules(repoRoot) {
  const rulesDir = path.join(repoRoot, ".cursor", "rules")
  let entries
  try {
    entries = await readdir(rulesDir, { withFileTypes: true })
  } catch {
    return []
  }

  const rules = []
  entries.sort((left, right) => left.name.localeCompare(right.name))
  for (const entry of entries) {
    if (!entry.isFile() || !entry.name.endsWith(".mdc")) continue
    const filePath = path.join(rulesDir, entry.name)
    try {
      const rule = parseRule(filePath, await readFile(filePath, "utf8"), repoRoot)
      if (rule) rules.push(rule)
    } catch {
      // One malformed rule must not disable the rest of the project rules.
    }
  }
  return rules
}

function patchPaths(text) {
  const found = []
  for (const matcher of [PATCH_FILE_RE, PATCH_MOVE_RE]) {
    matcher.lastIndex = 0
    for (const match of text.matchAll(matcher)) found.push(match[1])
  }
  return found
}

function collectRawPaths(node, key = "") {
  if (typeof node === "string") {
    const found = patchPaths(node)
    if (PATH_KEYS.has(key.replaceAll(/[^a-z]/gi, "").toLowerCase())) found.push(node)
    return found
  }
  if (Array.isArray(node)) return node.flatMap((value) => collectRawPaths(value, key))
  if (!node || typeof node !== "object") return []
  return Object.entries(node).flatMap(([childKey, value]) => collectRawPaths(value, childKey))
}

function repoRelativePath(rawPath, repoRoot, directory) {
  const candidate = rawPath.trim().replace(/^['"]|['"]$/g, "")
  if (!candidate || candidate.includes("\0")) return undefined

  const resolved = path.resolve(path.isAbsolute(candidate) ? candidate : path.join(directory, candidate))
  const relative = path.relative(repoRoot, resolved)
  if (!relative || relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
    return undefined
  }
  return relative.replaceAll("\\", "/")
}

export function toolPaths(args, repoRoot, directory) {
  return [
    ...new Set(
      collectRawPaths(args)
        .map((candidate) => repoRelativePath(candidate, repoRoot, directory))
        .filter(Boolean),
    ),
  ]
}

function matchesRule(rule, files) {
  if (!rule.globs.length) return false
  const patterns = rule.globs.map(globToRegExp)
  return files.some((file) => patterns.some((pattern) => pattern.test(file)))
}

function renderRules(rules) {
  if (!rules.length) return ""
  return [
    "Project rules for this repository. They are authoritative; `.cursor/rules/` is their single source of truth.",
    ...rules.map((rule) => `<!-- from ${rule.path} -->\n\n${rule.body}`),
  ].join("\n\n")
}

export async function createProjectRulesHooks({ repoRoot, directory = repoRoot }) {
  const resolvedRoot = path.resolve(repoRoot)
  const resolvedDirectory = path.resolve(directory)
  const activeBySession = new Map()

  async function selectedRules(sessionID) {
    const rules = await loadRules(resolvedRoot)
    const active = sessionID ? activeBySession.get(sessionID) : undefined
    return rules.filter((rule) => rule.always || active?.has(rule.path))
  }

  return {
    "experimental.chat.system.transform": async (input, output) => {
      const rendered = renderRules(await selectedRules(input.sessionID))
      if (rendered) output.system.push(rendered)
    },

    "experimental.session.compacting": async (input, output) => {
      const rendered = renderRules(await selectedRules(input.sessionID))
      if (rendered) output.context.push(rendered)
    },

    "tool.execute.before": async (input, output) => {
      const files = toolPaths(output.args, resolvedRoot, resolvedDirectory)
      if (!files.length) return

      const rules = await loadRules(resolvedRoot)
      const scoped = rules.filter((rule) => !rule.always && matchesRule(rule, files))
      if (!scoped.length) return

      const active = activeBySession.get(input.sessionID) ?? new Set()
      const newlyActive = scoped.filter((rule) => !active.has(rule.path))
      for (const rule of scoped) active.add(rule.path)
      activeBySession.set(input.sessionID, active)

      if (newlyActive.length && MUTATING_TOOLS.has(input.tool.toLowerCase())) {
        const names = newlyActive.map((rule) => rule.path).join(", ")
        throw new Error(
          `Project rules were activated for this change (${names}). Retry the edit; the rules are now in the model context.`,
        )
      }
    },

    event: async ({ event }) => {
      if (event.type === "session.deleted") activeBySession.delete(event.properties?.info?.id)
    },
  }
}

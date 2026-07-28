# Skills

Skills are reusable instruction packs that extend the agent with slash commands, domain knowledge, and specialized workflows. They power the **`{{.Skills}}`** block in the system prompt and the slash-command catalog surfaced to ACP clients and the HTTP UI.

> **Project rules** (`.coddy/rules`, `.cursor/rules`, etc.) are a separate mechanism injected as **`{{.Rules}}`**. Do not place rules in `skills.dirs`. See [rules.md](rules.md).

---

## Where to get skills

### skills.sh — community registry

The open agent skills ecosystem lives at **[https://skills.sh](https://skills.sh)**. Skills are plain GitHub repos with a `SKILL.md` file, compatible across agents that support the format (Cursor, Codex, Claude Code, Coddy, etc.).

Install via **`npx skills`** (Node.js required):

```bash
# Search the registry
npx skills find [query]

# Install a skill globally into ~/.agents/skills/
npx skills add <owner/repo@skill>

# Update all installed skills
npx skills update

# Check for updates
npx skills check
```

Global skills land in **`~/.agents/skills/`** — shared with any agent that reads that directory.

### skillsbd — Coddy-curated registry

**[https://neuraldeep.ru/skills](https://neuraldeep.ru/skills)** is the **skillsbd** registry, curated for Coddy specifically. Install its CLI:

```bash
npm install -g skillsbd
```

Key commands:

```bash
# Search the registry
npx skillsbd search [query]

# Install a skill
npx skillsbd install <name>

# List installed skills
npx skillsbd list
```

Skills from skillsbd are also installed into **`~/.agents/skills/`** by default, so Coddy picks them up automatically via the default `skills.dirs`.

You can also browse and install through the Coddy web UI: **Settings → Skills → Registry**.

---

## Install from a repository or marketplace API (agents standard)

Coddy can fetch skills itself, without any external CLI, from a **GitHub repo**, a **git URL**, or an **http(s) URL** to an [agents-standard](https://agents.md) `marketplace.json`. Configure sources under `skills.sources` and install them on demand — nothing is fetched automatically.

```yaml
skills:
  sources:
    - "EvilFreelancer/rpa-skills"                    # owner/repo shorthand (GitHub)
    - "artwist-polyakov/polyakov-claude-skills"      # a marketplace monorepo
    - "owner/repo@v1.2"                              # pin a branch or tag
    - "https://github.com/owner/single-skill.git"    # any git URL
    - "https://example.com/skills/marketplace.json"  # an API marketplace URL
```

### The `plugin` command (CLI and `/plugin` in chat)

Plugin and marketplace management uses one command surface, available identically as the
`coddy plugin ...` CLI and the built-in `/plugin` chat command (a deterministic slash command that
runs without an LLM turn, like `/compact`):

```bash
coddy plugin marketplace list                         # configured marketplaces + validity status
coddy plugin marketplace add <owner/repo | url>       # register a marketplace and fetch its skills
coddy plugin marketplace remove <owner/repo | url>    # drop a marketplace from config.yaml
coddy plugin marketplace sync [owner/repo | url]      # refresh all marketplaces, or just one
coddy plugin install <owner/repo | url>               # install (and update) a marketplace's skills
coddy plugin remove <name>                             # delete an installed skill (bundled = read-only)
coddy plugin enable <name>   |   plugin disable <name> # toggle a skill
coddy plugin list                                      # installed skills with versions
```

In chat, the same words follow `/plugin`, e.g. `/plugin marketplace add EvilFreelancer/rpa-skills`,
`/plugin install owner/repo`, `/plugin marketplace list`. `marketplace add` accepts both a GitHub
`owner/repo` shorthand and a direct git or `marketplace.json` URL. `marketplace list` probes each
source and reports whether it is a **valid marketplace** (agents standard, with its name, version, and
plugin count), a repo with **no marketplace.json** (skills discovered directly), or **unreachable**.

The lower-level `coddy skills` commands remain for skill files themselves:

```bash
coddy skills list                                      # all skills (with a VERSION column)
coddy skills enable <name>  |  disable <name>
coddy skills add <src>  |  sync  |  remove <name>      # remote source install (see below)
```

Three surfaces stay in parity — pick whichever fits:

- **CLI** — `coddy plugin ...` (and `coddy skills ...`).
- **Chat** — the `/plugin ...` command.
- **Web UI** — **Settings → Skills → Remote skill sources** (add a source, **Sync**, remove a source,
  **Refresh** to check versions, and a per-skill **Update** button when a newer version exists).

### Versions and updates

A marketplace `marketplace.json` may declare a `version` per plugin (semantic version), and a skill's
`SKILL.md` frontmatter may carry its own `version:`. Coddy records the installed version in the
`${CODDY_HOME}/skills/.remote.json` lockfile and shows it in `coddy skills list`, `coddy plugin list`,
the HTTP skill rows, and the Settings UI. `coddy plugin marketplace sync` (or the UI **Refresh**
button, backed by `GET /coddy/skills/updates`) re-reads each source's manifest and reports which skills
have a newer version upstream; the per-skill **Update** button (or `POST /coddy/skills/{name}/update`)
re-syncs just that skill's source to install it. Version-less plugins are shown without a version and
are never flagged for updates (no false positives).

### How a source is resolved

1. `owner/repo` shorthands and git URLs are cloned (`git clone --depth 1`, refreshed with `git pull --ff-only`); an API URL is downloaded as JSON.
2. If the repo (or API response) is an agents-standard **marketplace** (`.agents/plugins/marketplace.json` or `.claude-plugin/marketplace.json`), each listed plugin is resolved:
   - an **external** source (`{"source":"github","repo":"owner/repo"}` / `{"source":"url","url":"…","ref":"…"}`) is cloned;
   - a **relative** source (`"./plugins/foo"`) is read from inside the marketplace repo.
3. If there is **no manifest**, the repo is scanned directly for `SKILL.md`.
4. Every discovered skill directory (root `SKILL.md`, `skills/<name>/`, `.claude/skills/<name>/`, or `plugins/<p>/skills/<s>/`) is copied — with its sibling `scripts/`, `references/`, `examples/` — into `${CODDY_HOME}/skills/<name>/`, where the normal loader picks it up.

Provenance is tracked in `${CODDY_HOME}/skills/.remote.json`. Because synced skills live in a normal skills directory, `enable`/`disable` work on them like any other skill; `remove` deletes the copy (re-running `sync` re-installs it unless you also drop the source from `skills.sources`).

Private repositories rely on your ambient `git` credentials; API URLs are checked against the same SSRF guard used by the `webfetch` tool.

---

## Directory layout

Coddy searches all directories in `skills.dirs` and deduplicates by skill name. **Later directories have higher priority** — if the same skill name appears in multiple directories, the version from the directory listed last wins.

Default directories (lowest → highest priority):

| Priority | Path | Purpose |
|----------|------|---------|
| lowest | `~/.agents/skills/` | Global skills installed by `npx skills` / `npx skillsbd` — shared with all agents |
| ↑ | `~/.coddy/skills/` | Coddy-specific skills; may contain symlinks into `~/.agents/skills/` |
| highest | `${CWD}/.coddy/skills/` | Project-local skills — override anything from global/user directories |

Override in `config.yaml`:

```yaml
skills:
  dirs:
    - "~/.agents/skills"
    - "${CODDY_HOME}/skills"
    - "${CWD}/.coddy/skills"
    - "~/my-team-skills"
```

`${CODDY_HOME}` and `${CWD}` expand at runtime (per-session cwd for `${CWD}`).

---

## Supported file formats

### `subdir/SKILL.md` (recommended)

One skill per directory. Compatible with the standard agent skills layout and `npx skills`:

```
~/.agents/skills/
  code-review/
    SKILL.md
  docker-helper/
    SKILL.md
```

### Root `.md` / `.mdc` in a skill directory

Flat files at the root of a `skills.dirs` entry also register as skills (stem becomes the slash name).

### YAML frontmatter

Each skill file must have a frontmatter block with exactly two fields — both required and non-empty:

```markdown
---
name: code-review
description: One-line summary shown in the slash-command catalog and UI.
---

# Code Review

Full skill body here...
```

`name` sets the canonical slash-command identifier (e.g. `/code-review`). It overrides the filesystem-derived name when set. `description` is shown in the catalog and the Settings → Skills panel.

---

## Enable / disable without uninstalling

```bash
coddy skills list              # show all skills with enabled/disabled status
coddy skills disable <name>    # skip a skill without removing it
coddy skills enable <name>     # re-enable
```

Disabled state is stored in `~/.coddy/skills/.disabled` (plain text, one name per line).

---

## Writing your own skill

Create a directory anywhere and add `SKILL.md`:

```markdown
---
name: my-skill
description: Short description shown in the catalog.
---

# My skill

Instructions the agent will follow when this skill is active.
```

Then add the parent directory to `skills.dirs` in `config.yaml`, or drop the directory into `~/.coddy/skills/` or `${CWD}/.coddy/skills/`.

To share it with others, publish to GitHub and list it on [skills.sh](https://skills.sh) or submit to [neuraldeep.ru/skills](https://neuraldeep.ru/skills).

---

## How skills are applied

On each `session/prompt` the agent:

1. Scans `skills.dirs` for the session cwd and `CODDY_HOME`.
2. All loaded (and enabled) skills are always active — their bodies are available as slash commands and injected on demand.
3. Builds the **`{{.Skills}}`** system-prompt block: the slash-command catalog listing all skills, plus the full body of any always-active or glob-matched skill whose name is **not** already in the catalog.
4. At LLM call time, if the last user message contains `/name` invocations, each matched skill's body is **prepended to the user message** before it is sent to the model. This augmentation happens only inside the LLM request — it is **not stored in session history** and is **not visible in the chat transcript**.

ACP clients receive `available_commands_update` after `session/new` and `session/load`. The HTTP UI queries `GET /coddy/slash-commands` for autocomplete.

---

## References

- Implementation: `internal/skills/`, wiring in `internal/session/`, `internal/agent/system_prompt.go`, `internal/agent/react.go`
- Config reference: [config.md](config.md) → `skills`
- Rules (separate mechanism): [rules.md](rules.md)
- Registry UI: Settings → Skills (requires `coddy http`)

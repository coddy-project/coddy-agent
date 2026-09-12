# Writing documentation

How the documentation under `docs/` is organised, what a change to Coddy must carry into it, how screenshots and videos get onto a page, and which checks guard all of that. The rule in one sentence: a capability a user will notice, or a change to anything a page describes, is finished when the page says so, in the same pull request.

## Layout

| Folder | What lives there | Page type |
|--------|------------------|-----------|
| `docs/getting-started/` | Install, configure, update, troubleshoot, the changelog | Guides, read once in order |
| `docs/surfaces/` | One page per way of talking to the agent: console, web UI, editors, Telegram | Surface pages |
| `docs/operate/` | Running it as a service, remote mode, swarm, scheduler, security | Operator guides |
| `docs/features/` | One page per capability: modes, sessions, rules, skills, subagents, hooks, MCP, background tasks, compaction, memory, export | Feature pages |
| `docs/reference/` | Complete lists: CLI, `config.yaml`, environment variables, slash commands, keyboard, tools, HTTP API, ACP | Reference pages, generated where the code is the source of truth |
| `docs/recipes.md` | Task-shaped guides that combine features | Recipes |
| `docs/contributing/` | How Coddy is built, tested, designed and documented | Contributor pages |
| `docs/plans/` | Design records, decisions as they were taken | Frozen: not rewritten to match a later rename |
| `docs/assets/` | What the pages embed: screenshots, videos, brand files | See [the assets index](../assets/INDEX.md) |

The map of all of it is [`docs/nav.yaml`](../nav.yaml): every page with its group, title and a one-line summary. [`docs/README.md`](../README.md) (the hub), [`docs/llms.txt`](../llms.txt) and [`docs/llms-full.txt`](../llms-full.txt) are generated from it, and the sidebar of the documentation site will be too. A page that is not in the map does not exist as far as readers and agents are concerned, and `make docs-check` says so.

Files directly under `docs/` whose first line is `<!-- docs-stub` are redirect stubs at the addresses of the previous flat layout. The binary (`coddy -t` hints, `--dry-run` findings), the config schema descriptions, the bundled `configure-coddy` skill and the site still print those addresses, so a page that moves leaves a stub behind until the site has stable addresses of its own.

## Page types

Every page starts with `# <title>` (the title from `nav.yaml`), then one lead paragraph that says what the page covers and for whom, then H2 sections. English, the voice of the existing pages, a plain hyphen with spaces where a dash would go, fenced code blocks with a language, relative links (`../features/hooks.md`, `#anchor` for a heading of the same page). No YAML frontmatter: GitHub renders it as a table.

**Feature page** (`docs/features/`): what it is and when the agent uses it; how it looks on each surface, with a screenshot of the web UI or the console next to the paragraph that explains it; how to configure it, linking the field rows of [config.yaml reference](../reference/config.md#field-reference) rather than repeating them; the tools, commands or files involved, linking the reference rows; limits and what is deliberately absent; where the tests live.

**Surface page** (`docs/surfaces/`): how to start it; the layout with a screenshot; what the surface offers (modes, models, permissions, commands, keys) and where it differs from the others; remote use; captures and tests.

**Operator guide** (`docs/operate/`): the command, the configuration keys, what happens on reload and restart, security notes, troubleshooting pointers.

**Reference page** (`docs/reference/`): a lead paragraph and tables. Generated blocks sit between `<!-- docsgen:NAME:start -->` and `<!-- docsgen:NAME:end -->` markers; the prose around them is hand-written and survives regeneration. Never edit inside the markers.

**Recipe** (`docs/recipes.md`): a goal, numbered steps with the exact commands and YAML, links to the feature pages for the details.

## What a change must carry

- **A new capability** gets a page under `features/`, `surfaces/` or `operate/` (or a section of the page that owns the area), an entry in `nav.yaml`, a row in the reference that lists things of its kind (`tools.md` for a tool, `slash-commands.md` for a command, `environment-variables.md` for a variable, `keyboard.md` for a key, the generated `config.md` for a key through the schema description), and a line in the README "What it does" list when it is something a newcomer chooses Coddy for.
- **A changed behaviour** updates the page that describes it. Find it with `git grep -n '<key or command>' docs/`; the old text keeps teaching itself to the next reader, human or agent, until it is gone.
- **A web UI or console feature** is shown, not only described: a screenshot embedded next to the explaining paragraph, following the capture recipes below. A change that alters what a screenshot shows re-captures it.
- **A removed feature** takes its page, its `nav.yaml` entry and its assets with it (a stub stays at the page's address).
- **A renamed key, command or flag** is swept through the whole tree, docs included, as the workflow rules describe (`.cursor/rules/workflow.mdc`, step 9).

The workflow rules (`.cursor/rules/workflow.mdc`, mirrored in `.claude/rules/workflow.md`, delivered to Codex, OpenCode and ZCode by their hooks) carry the same contract as step 10, so every coding agent working on the repository reads it before it changes behaviour.

## Screenshots

Capture the real surface, never a mockup and never a re-used older image that no longer matches.

- **Web UI**: `make build TAGS="http ui scheduler memory cli gateway swarm"`, `coddy serve` on a disposable `CODDY_HOME` at a presentable path, Playwright at 1280x800 in the Dark theme as the default, 390 wide when the page discusses the narrow layout, Light when the page discusses themes. Never capture the provider detail pane: it renders `api_key` values in full. The `examples/` harnesses and `.cursor/rules/ui-verification.mdc` describe the stand.
- **Console**: a real Konsole window on an isolated Xvfb display, driven with XTEST (`demo-videos/rig/stage_konsole.sh` in the demo-videos repository), cropped to the used rows. Deterministic states of the usage footer come from `examples/cli/capture_usage.py`.
- **Naming and placement**: `docs/assets/<page-or-feature>/<state>-<theme>-<width>.png` when a page needs more than a couple of images, otherwise `docs/assets/<feature>-<state>-<theme>-<width>.png` at the top level. Kebab-case, no issue or pull request numbers, under 1 MB.
- **On the page**: an image line followed by a one-line caption in italics, placed next to the paragraph that explains what the image shows. One image per view and state; a table with two images side by side when the page compares them.

  ```markdown
  ![The Tasks drawer with a subagent run in progress](../assets/subagents/tasks-panel-agent-running-dark.png)

  *The Tasks drawer with a subagent run in progress*
  ```

Screenshots taken as evidence for a pull request (before and after pairs, every theme, every width) belong to the pull request: drag them into the description on GitHub, which stores them under `user-attachments`, or push them to the orphan `screenshots` branch and link the raw file. They never go under `docs/assets/`.

## Videos

The five demos under `docs/assets/video/` (console, web UI, Zed and VS Code over ACP, swarm) were recorded with the rig in the demo-videos repository: scripted XTEST takes on Xvfb, post-production with `post.py` (zooms, captions, hotkey badges, intro and outro), then re-encoded for the repository:

```bash
ffmpeg -i take.mp4 -vf "scale=1280:-2" -c:v libx264 -preset slow -crf 31 -pix_fmt yuv420p -movflags +faststart -an docs/assets/video/<name>.mp4
```

Keep a video under 4 MB and under four minutes; a feature clip is better at twenty seconds than at two minutes. GitHub plays an `.mp4` on its file page but not inline in Markdown, so a page links the video from a poster image (a screenshot of the same surface) or from a plain link; the documentation site can embed it. A video that shows a surface that changed is re-recorded or removed.

## Assets index

[`docs/assets/INDEX.md`](../assets/INDEX.md) states what belongs in the folder, the capture recipes and the brand files, and carries a generated inventory: every asset with its size and the files that reference it. `make docs-check` fails on an asset nothing references, so a file is either used by a page, the README, `DESIGN.md`, the `Dockerfile` or a capture script, or it is deleted.

## Generated pages

`internal/docsgen` (run through `cmd/docsgen`) generates, from `nav.yaml`, the embedded config schema and the `--help` screens of a full-tag binary:

| File | Block | Source |
|------|-------|--------|
| `docs/README.md` | `docsgen:nav` | `nav.yaml`: groups, pages, summaries |
| `docs/llms.txt`, `docs/llms-full.txt` | whole file | `nav.yaml` and the pages themselves |
| `docs/reference/config.md` | `docsgen:config` | `internal/config/config.schema.json` descriptions and the loader's defaults (`config.DocDefaults`) |
| `docs/reference/cli.md` | `docsgen:cli` | `coddy --help` and the `--help` of every command with a flag set |
| `docs/assets/INDEX.md` | `docsgen:assets` | the files under `docs/assets` and their references |
| `docs/getting-started/changelog.md` | whole file | the GitHub Releases, only with `make docs-changelog` |

```bash
make docs            # regenerate everything (builds the full-tag binary first, which needs Node for the ui tag)
make docs-fast       # everything but the CLI reference, no binary and no Node needed
make docs-check      # regenerate into memory and fail on drift, missing pages, broken links, unused assets
make docs-changelog  # also refresh the changelog from GitHub Releases (needs gh)
```

`make docs-check` runs in CI as the job **Documentation** and in the pre-commit hook for commits that touch `docs/`, the README, `AGENTS.md`, `DESIGN.md`, `CONTRIBUTING.md` or the config schema (there without the CLI build: `go run ./cmd/docsgen -skip-cli`). The link check resolves every relative link and image and every `#anchor` against the headings of the target page, GitHub style; fenced code blocks are ignored.

## The site

Every page of the map has a stable address on coddy.dev, so the binary, the schema, the bundled skill, posts and other people's links can name a page without naming a path in this repository, and nothing is duplicated there:

| Address | What happens |
|---------|--------------|
| `https://coddy.dev/docs/<slug>` | The visitor lands on the page on GitHub; a `#fragment` survives the hop |
| `https://coddy.dev/docs/<slug>.md` | The raw Markdown of the page on the main branch |
| `https://coddy.dev/docs/` | The hub, `docs/README.md` on GitHub |
| `https://coddy.dev/llms.txt`, `https://coddy.dev/llms-full.txt` | The same files as `docs/llms.txt` and `docs/llms-full.txt`; the index links the raw Markdown on `main` |
| `https://coddy.dev/config.schema.json` | The config schema, published by `make site-schema` |

The slug is the page's path under `docs/` without `.md` (`getting-started/install`, `reference/config`); a page outside `docs/` is known by its file name (`CONTRIBUTING`). GitHub Pages has no server-side redirects, but it serves `404.html` for every address that is not a file, so the site's `404.html` loads `docs-redirect.js`, one generated script that reads the path and forwards the browser (`window.coddyDocsTarget` is the pure mapping, testable without navigating). The answer carries a 404 status, which is fine for people and irrelevant to agents, who get the raw addresses from `llms.txt`; a redirect rule on the Cloudflare zone in front of the site turns the same mapping into a real 301 (Rules, Redirect Rules, dynamic: when the path starts with `/docs/`, redirect to `concat("https://github.com/coddy-project/coddy-agent/blob/main/docs/", substring(http.request.uri.path, 6), ".md")` with status 301, and a second rule for the `.md` suffix pointing at raw.githubusercontent.com). `internal/docsgen` renders the layer from `nav.yaml`:

```bash
make site-docs        # write docs-redirect.js, llms.txt and llms-full.txt into the site checkout beside this one (SITE_REPO=... if elsewhere)
make site-docs-check  # report drift without writing
```

The site repository is `coddy-project.github.io`. A change to a page, to `nav.yaml` or to the schema is published there with the same pull request; the site commit follows the merge of the coddy-agent change, because `llms.txt` names pages by their path on `main`. Links that leave the repository use the `coddy.dev/docs/<slug>` form, never a GitHub path.

## Design records

`docs/plans/` holds the plans and decisions behind the larger features as they were written, dates and all. They are read for the why, not for the how, and are not rewritten when the code moves on: a rename elsewhere leaves them alone, and only a link target is repaired when a page moves. A new plan is a new file named after the feature; when a plan carries a date, the date is the day the decision was taken.

## The web UI design contract

[`DESIGN.md`](../../DESIGN.md) at the repository root is the visual and behavioural contract of the embedded SPA: tokens, layout, component behaviour, localisation, the dev workflow. A change to `external/ui/src/` that alters what a component looks like or does updates `DESIGN.md` in the same change, and the surface page [Web UI](../surfaces/web-ui.md) when the user sees the difference. The brand files (logos, favicons, the social preview) are described in the assets index.

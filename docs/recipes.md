# Recipes

Each recipe below is one job done end to end: the commands to type, the YAML to write and the order to do it in. They combine features that have pages of their own, so every step links to the page that explains the feature in full instead of repeating it. The commands assume a release binary - the one-line installer, a package, the Docker image or `make build` with the full tag set - which carries every surface a recipe needs; a lean `make build` has neither the console nor the HTTP server.

## Coddy in CI

A pipeline step that asks the agent one question and reads the answer: a review of a pull request, a summary of a diff, a check that a document still matches the code. The console's print mode, `coddy -p` ([Console](surfaces/console.md), "One-shot print mode"), runs one turn without a terminal, streams the assistant text to stdout and the diagnostics to stderr, and exits non-zero when something went wrong. The release binary carries the console; a lean build answers `-p` with a rebuild hint.

1. **Install the binary on the runner.** The one-line installer ([Install](getting-started/install.md)) puts `coddy` into `~/.local/bin` and creates `~/.coddy/config.yaml` from the example file when there is none. Put that directory on `PATH` for the steps that follow.

   ```bash
   curl -fsSL https://coddy.dev/install.sh | bash
   export PATH="$HOME/.local/bin:$PATH"
   coddy -v
   ```

2. **Give the job a home of its own.** `CODDY_HOME` is where the config, the sessions and the logs live (default `~/.coddy`). Point it at a temporary directory, so the transcript of one job never leaks into the next and nothing is left behind when the runner is discarded. The loader reads `$CODDY_HOME/config.yaml` and, before that, `$CODDY_HOME/.env` for variables the shell did not set; `CODDY_CONFIG` names a config file somewhere else, and `--cwd` (or `CODDY_CWD`) sets the workspace of the session, which in a job is the checkout. Every session of the job lands under `$CODDY_HOME/sessions`.

   ```bash
   export CODDY_HOME="$(mktemp -d)"
   ```

3. **Write a config whose key comes from the environment.** A provider's `api_key` may be a `${VAR}` reference, expanded when the file loads, or it may be left empty, in which case the key is read at call time from `NAME_API_KEY` - the provider name in upper case with hyphens turned into underscores, so `openai` reads `OPENAI_API_KEY` and `team-gateway` reads `TEAM_GATEWAY_API_KEY`. Either way the secret stays in the CI secret store and never in a file. Pin the model and the turn cap: `agent.max_turns` is what bounds the cost of one step. With no `config.yaml` at all, `OPENAI_API_KEY` or `ANTHROPIC_API_KEY` alone gives a default provider and model, but a job should pin its own. [Configuration](getting-started/configuration.md) has every key.

   ```yaml
   providers:
     - name: openai
       type: openai
       api_key: "${OPENAI_API_KEY}"   # or leave it empty: OPENAI_API_KEY is read when the model is called

   models:
     - model: "openai/gpt-5.4-mini"
       max_tokens: 16384

   agent:
     model: "openai/gpt-5.4-mini"
     max_turns: 20
   ```

4. **Gate the config before spending a token.** `coddy -t` checks the file a start would load against the embedded schema and the loader's rules, prints each problem with its line and a `fix:` line, and exits 1 on errors without starting anything ([Checking the file from the command line](getting-started/configuration.md#checking-the-file-from-the-command-line)). `coddy --dry-run` runs the same check and then asks every provider for its model list, which exercises the address and the key in one request, so a wrong secret fails here on a named line instead of as a 401 in the middle of the review ([Dry run](getting-started/configuration.md#dry-run-probing-what-the-file-points-at)). Both flags work in every build.

   ```bash
   coddy -t          # the file: schema and loader rules
   coddy --dry-run   # the file, then the providers it names
   ```

5. **Run the prompt.** `-p` takes the prompt. `--mode ask` restricts the turn to the read-only tools (`read`, `glob`, `grep`, `print_tree`, `websearch`, `webfetch`, `question`, `load_skill`: no shell, no file writes, no MCP tools), which is the right shape for a review and means no permission prompt can arise. `--model` picks one of the configured models, `--cwd` the workspace. The answer goes to stdout and everything else to stderr, so redirecting stdout gives a file you can post as a comment.

   ```bash
   git diff origin/main...HEAD > review.patch
   coddy -p "Review the change in review.patch. Read the files it touches for context, report defects with path:line, and end with one line: VERDICT: ok or VERDICT: needs work." --mode ask > review.md
   ```

   A step that should edit files runs in `agent` mode instead, and its permission requests are then answered without a human: `--permission-mode bypass` allows everything, `accept_edits` approves file writes and rejects shell commands, and `ask` (the default) rejects every request with a note on stderr. The question tool gets empty answers. A later step continues the same session with `coddy -c -p "..."`, so a second question sees the files and tool results of the first.

6. **Read the exit status.**

   | Command | Exit 0 | Exit 1 |
   |---|---|---|
   | `coddy -p "..."` | the turn finished and the answer was printed | the config did not load, the provider failed, a flag named an unknown model or mode, or the turn was cancelled |
   | `coddy -t` | the file is valid (warnings do not fail it) | the file has errors or does not exist |
   | `coddy --dry-run` | every probe passed | the file has errors or a probe failed |

   The status of `-p` reports the process, not the verdict: a turn that stopped at `agent.max_turns` still exits 0. Make the prompt end with an explicit line, as above, and let the pipeline grep for it.

7. **Decide what a checkout may execute.** `.coddy/mcp.json`, `.coddy/hooks.json` and `.coddy/agents/` arrive with the repository and are held under the default `ask` policy until an operator approves them, which nobody does on a runner. For a repository you own, pass `--mcp-project-trust allow` (the console, `acp` and `serve` all take it) or set `mcp.project_trust`, `hooks.project_trust` and `subagents.project_trust` to `allow` in the job's config. Leave them at `ask`, or set `deny`, for jobs that run on pull requests from strangers, so a fork cannot choose what the runner executes. [MCP servers](features/mcp.md), [Hooks](features/hooks.md) and [Subagents](features/subagents.md) describe the three gates.

Put together as a GitHub Actions job:

```yaml
name: Review
on: [pull_request]

jobs:
  review:
    runs-on: ubuntu-latest
    env:
      CODDY_HOME: ${{ runner.temp }}/coddy
      OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Install Coddy
        run: |
          curl -fsSL https://coddy.dev/install.sh | bash
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"

      - name: Write the config
        run: |
          mkdir -p "$CODDY_HOME"
          cat > "$CODDY_HOME/config.yaml" <<'EOF'
          providers:
            - name: openai
              type: openai
              api_key: "${OPENAI_API_KEY}"
          models:
            - model: "openai/gpt-5.4-mini"
              max_tokens: 16384
          agent:
            model: "openai/gpt-5.4-mini"
            max_turns: 20
          EOF

      - name: Check the config and the provider
        run: coddy --dry-run

      - name: Review the change
        run: |
          git diff origin/main...HEAD > review.patch
          coddy -p "Review the change in review.patch. Read the files it touches for context, report defects with path:line, and end with one line: VERDICT: ok or VERDICT: needs work." --mode ask | tee review.md
          grep -q 'VERDICT: ok' review.md
```

## A Telegram bot for a team

One `coddy serve` process polls a bot, answers in the chats you allow, and keeps every conversation as an ordinary session that the web UI on the same process can watch and continue. The full reference is [Telegram gateway](surfaces/gateway.md); this recipe walks the setup for a team room.

1. **Get a binary with the gateway.** The release binaries, the packages and the image all carry the `gateway` tag. From source, build the Telegram adapter alone or the full set:

   ```bash
   make build TAGS="gateway.telegram"
   # or the set a release ships
   make build TAGS="http ui scheduler memory cli gateway swarm"
   ```

   Enabling the bot in a binary built without the tag is a startup error naming the tag, so a mismatch cannot pass unnoticed.

2. **Get the token and the ids.** Create the bot with `/newbot` at [@BotFather](https://t.me/BotFather) and save the token. Ask [@userinfobot](https://t.me/userinfobot) for your Telegram user id, and for the id of every teammate you want on an allowlist. Keep the token out of `config.yaml`: `~/.coddy/.env` is read before the file loads, and the process environment always wins over it.

   ```text
   TELEGRAM_BOT_TOKEN=8992982910:AAF...
   ```

3. **Describe the team in the config** ([Configuration reference](surfaces/gateway.md#configuration-reference)).

   ```yaml
   gateways:
     telegram:
       enable: true
       token: "${TELEGRAM_BOT_TOKEN}"   # may be omitted: an empty token reads TELEGRAM_BOT_TOKEN
       admins: [123456789]              # your own user id; admins pass every access check
       default_access: "admins"         # all | admins | group:<name>
       default_isolation: "shared"      # individual | shared | admin (group chats only)
       user_groups:
         - name: "devs"
           user_ids: [111222333, 444555666]
       chats:
         - chat_id: -1001234567890      # the team's group: every dev, one shared session
           access: "group:devs"
           isolation: "shared"
   ```

   `default_access` says who may talk to the bot in a chat that has no override: `admins` answers only the listed ids, `all` answers anyone who can write to the chat, `group:<name>` answers the members of that `user_groups` entry (admins always pass). Denied messages are dropped silently. `default_isolation` applies to group chats only - a private chat is always one session per user - and picks the session scope: `individual` gives every member a session of their own, `shared` gives the room one session everybody continues, `admin` answers admins only and lets them share one session. `chats` overrides both per chat; a group's `chat_id` is negative. Add `rich_messages: true` for native Markdown and collapsible tool blocks on a Bot API 10.1 server.

4. **Check the file, then start in the background** ([coddy serve and the daemon](operate/serve.md)).

   ```bash
   coddy serve --dry-run   # checks the token against the Bot API and names the bot
   coddy serve --daemon    # or -d
   coddy serve status
   ```

   `--daemon` detaches and leaves a dispatcher behind that restarts the worker whenever it dies for any reason other than `coddy serve stop`. The command waits for the first worker before it returns, so a configuration that cannot start is reported in the terminal that typed the command. The record lives in `~/.coddy/serve.json`, the log in `~/.coddy/logs/serve.log`; `coddy serve restart` brings it back with the arguments it was started with. Under `systemd`, Docker or another supervisor that already owns process lifetimes, run `coddy serve` in the foreground instead and let that supervisor restart it. Run one process per bot token: Telegram hands each update to one long poll, so a second poller steals messages from the first. A bot and nothing else is `--http=false` or `httpserver.enable: false`.

5. **Open the same chat in the browser** ([The same session in the chat and in the browser](surfaces/gateway.md#the-same-session-in-the-chat-and-in-the-browser)). With `httpserver.enable` on, which is the default, the same process serves the web UI at `http://127.0.0.1:12345/`. Chat conversations are sessions with a `gw_` prefix: they are listed in the session rail, a turn the bot is answering streams into a tab watching it, and a reply typed in the browser is in the chat's history on the next message. Permission prompts stay with the chat, and the gateway answers them itself so the bot works unattended - so give the process a workspace you can afford to let it change (`coddy serve --cwd DIR`, else the directory it was started from). Only one turn runs per session; a message that arrives while a browser turn is in flight gets a busy notice instead of interleaving. To reach the UI from another machine, bind `httpserver.host: "0.0.0.0"` and set a token as in the next recipe.

6. **Use it from the chat** ([Bot interaction model](surfaces/gateway.md#group-chats)). In a private chat every message is answered. In a group the bot responds only when it is @mentioned, when a message replies to one of its own, or to `/clear`; under `admin` isolation it also ignores everyone outside `admins`. `/mode` and `/model` open inline keyboards for the session's mode and model, `/context` shows what fills the context window, and `/clear` starts a fresh session for that user or chat - the old bundle stays on disk and `coddy sessions list` still shows it. A changed token rebuilds the gateway in place and a flipped `enable` starts or stops it without a restart; when a tap seems to do nothing, raise one component: `coddy serve --log-level "info,gateway.telegram=debug"` ([Debugging a chat](surfaces/gateway.md#debugging-a-chat)).

## A project set up for agents

A repository can carry everything an agent needs to work in it well: the conventions, the workflows, the tool servers and the guard rails. All of it lives in files under the checkout, so it is versioned with the code and arrives with every clone. This is the tree the steps below produce, and what each file does:

```text
my-project/
  AGENTS.md                       # project docs preamble, in every prompt
  .coddy/
    rules/
      go-style.md                 # a rule, attached once a Go file comes into play
    skills/
      release-notes/
        SKILL.md                  # /release-notes, project-local
    mcp.json                      # project MCP servers, held until approved
    hooks.json                    # lifecycle hooks, held until approved
    hooks/
      gofmt.sh                    # the script hooks.json runs
    agents/
      reviewer.md                 # a subagent definition, held until approved
```

1. **`AGENTS.md` at the root.** It is read into every prompt unconditionally, as the project docs preamble ([Project docs preamble](features/rules.md#project-docs-preamble)), and so is a `DESIGN.md` beside it. Keep it a short map: where things live, how to build and test, what a change must carry. Nested `AGENTS.md` files deeper in the tree are read on demand, the moment a tool enters their folder, and never walked for.

2. **Rules in `.coddy/rules/`.** A `.md` file is a Claude Code rule (`paths` gates it, and it is unconditional without them); a `.mdc` file is a Cursor rule (`description`, `globs`, `alwaysApply`). Both dialects are accepted in every rule folder, and `.coddy/rules` wins over `.agents/rules`, `.cursor/rules`, `.claude/rules` and `.codex/rules` when two files share a name ([Rule file formats](features/rules.md#rule-file-formats)). A gated rule enters the prompt the first time a matching file is attached or touched by a tool, then sticks for the session.

   ```markdown
   ---
   description: Go coding standards
   paths:
     - "**/*.go"
   ---

   Write comments in English. Wrap errors with fmt.Errorf("context: %w", err).
   Run `make lint` before you report a change as done.
   ```

   `coddy rules list` prints the catalog with the source folder, the dialect each file was read with, whether it is in every prompt and what activates the others.

3. **A skill in `.coddy/skills/<name>/SKILL.md`.** `${CWD}/.coddy/skills` is the highest-priority entry of the default `skills.dirs`, so a project skill overrides one of the same name from `~/.coddy/skills` or `~/.agents/skills` ([Skills](features/skills.md)). The directory name is the slash command, `/release-notes` here; writing the file is the last recipe on this page. Run `coddy skills list` inside the project to see it listed for that directory.

4. **MCP servers in `.coddy/mcp.json`** ([MCP servers](features/mcp.md)). The file has Cursor's shape, one `mcpServers` object keyed by name; a URL-only entry is a streamable HTTP server, and `${CWD}` in `args` is the session workspace.

   ```json
   {
     "mcpServers": {
       "filesystem": {
         "command": "npx",
         "args": ["-y", "@modelcontextprotocol/server-filesystem", "${CWD}"]
       },
       "docs": { "url": "https://mcp.example.com/mcp" }
     }
   }
   ```

   Because the file arrives with the checkout, the repository - not the operator - would be choosing the command a session starts. Under the default `mcp.project_trust: ask` these entries stay cold until approved for that workspace ([Workspace trust for project-local servers](features/mcp.md#workspace-trust-for-project-local-servers)):

   ```bash
   coddy mcp list                # scope, trust state and the command of every merged server
   coddy mcp trust filesystem    # prints the declaration once more, then records the receipt
   ```

   The receipt in `~/.coddy/mcp-trust.json` is keyed by the workspace and a digest of the declaration, so rewriting the entry asks again; `config.yaml` and `~/.coddy/mcp.json` are yours and are never gated.

5. **Hooks in `.coddy/hooks.json`** ([Hooks](features/hooks.md)). The file uses Claude Code's shape. This one runs `gofmt` after every edit and tells the model what it changed:

   ```json
   {
     "hooks": {
       "PostToolUse": [
         {
           "matcher": "edit|write|apply_patch",
           "hooks": [{ "type": "command", "command": ".coddy/hooks/gofmt.sh" }]
         }
       ]
     }
   }
   ```

   ```bash
   #!/bin/sh
   # .coddy/hooks/gofmt.sh - runs in the session cwd with the call's JSON on stdin
   path=$(jq -r '.tool_input.path // ""')
   case "$path" in
     *.go)
       if out=$(gofmt -l "$path" 2>&1) && [ -n "$out" ]; then
         gofmt -w "$path"
         printf '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"gofmt reformatted %s"}}' "$path"
       fi
       ;;
   esac
   exit 0
   ```

   Hooks run with your permissions, before any permission prompt, so a file that came with a clone is parsed and listed but runs nothing until it is approved ([Project files and trust](features/hooks.md#project-files-and-trust)); the first turn that finds it records a notice in the session. Review the commands, then:

   ```bash
   coddy hooks list
   coddy hooks trust .coddy/hooks.json
   ```

6. **A subagent in `.coddy/agents/reviewer.md`** ([Definition files](features/subagents.md#definition-files)). Only `description` is required; `name` defaults to the file stem. `tools`, `disallowed_tools` and `permission_mode` can only narrow what the parent could do, and the body is the child's role.

   ```markdown
   ---
   name: reviewer
   description: Reviews a diff or a module for defects and reports findings with file paths and lines.
   tools: read, glob, grep, print_tree, run_command
   permission_mode: ask
   max_turns: 20
   ---
   You review code. Read before you judge, cite `path:line` for every finding,
   separate defects from style remarks, and end with a short verdict.
   ```

   Project definitions follow `subagents.project_trust`, with receipts of their own ([Scopes and project trust](features/subagents.md#scopes-and-project-trust)):

   ```bash
   coddy agents list
   coddy agents trust reviewer
   ```

7. **Approve once per clone, or set a policy.** The three approvals above are per workspace and per file digest, so a fresh clone or an edited file asks again. For a checkout you own, `mcp.project_trust`, `hooks.project_trust` and `subagents.project_trust` set to `allow` in `config.yaml` skip the receipts; `deny` never reads the project files at all. Under `--remote` the approvals belong to the server, where the code would run (next recipe).

## A server driven from a laptop

`coddy serve` on a server with a workspace and a model behind it; on the laptop only a client. The console, an editor over ACP and the web UI all speak to the same server, and the sessions stay there. The complete guide is [Remote mode](operate/remote.md); the sources for the flags below are [Console](surfaces/console.md), "Remote mode (`--remote`)", and [Authentication](reference/http-api.md#authentication-optional).

1. **Give the server a token and bind it off loopback.** Bearer auth is off by default. Set the token through the environment or `--auth-token` rather than writing it into the file; a `${ENV}` reference in `httpserver.auth_token` works too. `host` defaults to `127.0.0.1` because one process starts every enabled subsystem, so asking for a bot must not open the API to the network as a side effect. `cors` is for the browser in step 4.

   ```yaml
   httpserver:
     host: "0.0.0.0"
     port: 12345
     auth_token: "${CODDY_HTTP_TOKEN}"
     cors:
       enable: true
       allowed_origins: ["http://localhost:12345"]   # the origin the laptop's UI is served from
   ```

   ```bash
   export CODDY_HTTP_TOKEN="a-long-random-string"   # or a line in ~/.coddy/.env
   coddy serve --dry-run    # binds the address once, names a port somebody else holds
   coddy serve --daemon     # foreground `coddy serve` under systemd or Docker
   ```

   The startup banner says what is reachable and how: `httpserver  http://0.0.0.0:12345  (bearer auth)`. Binding a non-loopback address without a token logs a warning at start, and clients warn when they send a token over plain http to an address that is not loopback: put TLS in front of the server, a reverse proxy is enough, when the path crosses a network you do not control.

2. **Drive it from the laptop's console.** `--remote` takes a bare `host:port` (scheme defaults to http), a full URL, or the name of a remote listed under `httpserver.remotes` in the laptop's own `config.yaml`. The token comes from `--remote-token` or `CODDY_REMOTE_TOKEN`, never from the file.

   ```bash
   export CODDY_REMOTE_TOKEN="a-long-random-string"
   coddy --dry-run --remote box.example:12345   # the target has to accept the token
   coddy --remote box.example:12345
   coddy -p "What changed in the last release?" --remote box.example:12345
   ```

   ```yaml
   httpserver:
     remotes:
       - name: "box"
         url: "https://box.example:12345"
   ```

   With the entry above, `coddy --remote box` is enough. The turn runs on the server in its workspace: tool boxes, thinking and token counts stream back, permission and question modals answer through the server, `/resume`, `-c` and `--session-id` pick from the server's session list, and the banner reads `remote: <url>`. Two things are refused remotely on purpose: `--permission-mode` (the server's configuration governs it) and the `!!` local shell (the workspace is not on this machine).

3. **Drive it from an editor.** `coddy acp` takes the same two flags, so an ACP client such as Zed points its agent command at the remote server; put `CODDY_REMOTE_TOKEN` in the editor's environment or pass `--remote-token`.

   ```bash
   coddy acp --remote box.example:12345
   ```

4. **Drive it from the browser.** The environment chip sits in the composer's workspace row and reads `Local` or the name of the remote in use. Its menu lists the remotes of the `coddy serve` that served the page (`httpserver.remotes`) and `+ Add remote…`, which takes a name, a URL and the bearer token; choosing an entry connects at once and reloads, and the token stays in the browser, never in a config file. The remote server has to allow the page's origin through `httpserver.cors`, which is what step 1 set; the dot on each entry turns green when a cross-origin `GET /v1/models` succeeds with the token ([Cross-origin access and the remote UI](reference/http-api.md#cross-origin-access-and-the-remote-ui), [Web UI](surfaces/web-ui.md)). Opening the server's own address instead loads the SPA shell without a token, since the shell is public, and the page asks for the token before it calls the API.

5. **Remember what stays on the server.** Sessions, the trust receipts and the child sessions of subagents are the server's, so a project file is approved there: `coddy mcp trust`, `coddy hooks trust` and `coddy agents trust` on the server itself, or `POST /coddy/mcp/{name}/trust`, `POST /coddy/hooks/trust` and `POST /coddy/subagents/{name}/trust` over the API. A dropped connection leaves the server turn running; `/resume` shows the outcome once it ends.

## A skill of your own

A skill is a `SKILL.md` file with a frontmatter and a body of instructions; it becomes a slash command on every surface and its body reaches the model only when invoked. The reference is [Skills](features/skills.md), in particular [Writing your own skill](features/skills.md#writing-your-own-skill) and [Supported file formats](features/skills.md#supported-file-formats).

1. **Create the pack.** One skill per directory under `~/.coddy/skills/`; the directory name is the slash command, so keep `name` equal to it. `description` is the line the catalog and the Settings page show, and an optional `version` is shown by `coddy skills list` and used to detect updates for skills installed from a source.

   ```markdown
   ---
   name: release-notes
   description: Turns the commits since the last tag into release notes grouped by area.
   version: 1.0.0
   ---

   # Release notes

   Run `git describe --tags --abbrev=0` to find the last tag, then `git log <tag>..HEAD --oneline`.
   Group the commits by scope, drop merge commits, write one line per change in the imperative
   mood, and end with a "Breaking changes" section, or "None" when there are none.
   ```

   Saved as `~/.coddy/skills/release-notes/SKILL.md`. The other two default directories are `~/.agents/skills/` (global, shared with `npx skills` and other agents) and `${CWD}/.coddy/skills/` (project-local, the highest priority); a later directory wins when two skills share a name, and any other folder can be added to `skills.dirs` in `config.yaml`.

2. **See it in the catalog.** `coddy skills list` prints the search roots and a table with the skill, its version, `enabled` or `disabled`, and the description.

   ```bash
   coddy skills list
   ```

3. **Find it as a slash command.** Type `/` on the first line of the console editor and the suggestions include `/release-notes`; the web UI's composer lists it from `GET /coddy/slash-commands`, and an ACP editor receives it in `available_commands_update` after `session/new`. When a message contains `/release-notes`, the body is prepended to that message for the model under `## Invoked skill: /release-notes`, for that request only: the transcript keeps the message as you typed it. With `skills.auto_discovery` left at its default the model can also pull the skill in on its own through the `load_skill` tool when a request matches the description.

4. **Test it.** The loader rescans `skills.dirs` on every prompt, so edit, save and send again; no restart is involved. Print mode gives a quick loop from a repository with tags:

   ```bash
   cd ~/src/my-project
   coddy -p "/release-notes for everything since the last tag"
   ```

5. **Switch it off without deleting it.** `coddy skills disable release-notes` keeps the files and drops the command; `coddy skills enable release-notes` brings it back. The state is one name per line in `~/.coddy/skills/.disabled`.

   ```bash
   coddy skills disable release-notes
   coddy skills enable release-notes
   ```

6. **Share it.** Publish the directory in a GitHub repository. Others install it with `coddy skills add owner/repo` followed by `coddy skills sync`, or through `coddy plugin install owner/repo`, and a listing on [skills.sh](https://skills.sh) makes it findable with `npx skills find`.

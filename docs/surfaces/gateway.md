# Messenger Gateway

The messenger gateway lets you drive a Coddy agent directly from a chat application such as Telegram. The agent runs the same ReAct loop, tools, and skills as in the HTTP UI or ACP mode — the gateway is only a transport layer.

This page is about the Telegram bot and the gateway's common parts. The Pachca (Пачка) integration bot has a page of its own: [Pachca gateway](pachca.md).

## Contents

- [Overview](#overview)
- [Build tags](#build-tags)
- [Quick start (Telegram)](#quick-start-telegram)
- [Configuration reference](#configuration-reference)
  - [Access levels](#access-levels)
  - [Session isolation modes](#session-isolation-modes)
  - [Per-chat overrides](#per-chat-overrides)
  - [User groups](#user-groups)
- [Running the gateway](#running-the-gateway)
- [Debugging a chat](#debugging-a-chat)
- [Bot interaction model](#bot-interaction-model)
  - [Private chats](#private-chats)
  - [Replies](#replies)
  - [Group chats](#group-chats)
  - [Commands](#commands)
- [What the messenger needs, and where it is said](#what-the-messenger-needs-and-where-it-is-said)
- [Writing a new adapter](#writing-a-new-adapter)
  - [1. Implement the Adapter interface](#1-implement-the-adapter-interface)
  - [2. Add serve_name.go and its stub](#2-add-serve_namego-and-its-stub)
  - [3. Implement acp.UpdateSender](#3-implement-acpupdatesender)
  - [4. Add a build tag](#4-add-a-build-tag)
  - [5. Register the subsystem in coddy serve](#5-register-the-subsystem-in-coddy-serve)
- [The same session in the chat and in the browser](#the-same-session-in-the-chat-and-in-the-browser)
- [Mini App](#mini-app)
- [Woken turns land in the chat](#woken-turns-land-in-the-chat)
- [Pictures the agent looked at](#pictures-the-agent-looked-at)
- [Session lifecycle](#session-lifecycle)
- [Security notes](#security-notes)

---

## Overview

```
Telegram / Pachca / future messengers
         │  polling / events history
         ▼
  external/gateway/          ← build tag: gateway | gateway.telegram | gateway.pachca
    Hub (goroutine per adapter, auto-restart)
         │
         ▼
  sessionstore               ← maps chat+user context → Coddy session ID
         │                     /clear replaces the stored ID, /resume binds it
         │                     to a session chosen from the server's list
         ▼
  session.Manager            ← shared with coddy acp / coddy serve
    HandleSessionPromptWithSender(...)
         │
         ▼
  ReAct agent loop           ← identical to HTTP and ACP paths
  (tools, skills, MCP, LLM)
         │
         ▼
  Sender (per-message)       ← buffers agent output, sends back to chat
```

Multiple gateways (Telegram and Pachca today) run in the same process, each as a subsystem of its own with its own session map, over the same session manager.

They also share it with everything else `coddy serve` started. One process, one
`session.Manager`: the chat conversations are ordinary Coddy sessions, listed
and openable in the web UI while they are happening.

---

## Build tags

| Tag | Includes |
|-----|----------|
| `gateway.telegram` | Telegram adapter only |
| `gateway.pachca` | Pachca adapter only |
| `gateway` | all adapters (Telegram and Pachca) |

`gateway` is part of the recommended full set, so the released binaries, the
packages and the published image all carry it. Build it on its own only for a
slimmer binary:

```bash
# Telegram only
make build TAGS="gateway.telegram"

# Pachca only
make build TAGS="gateway.pachca"

# All gateways
make build TAGS="gateway"

# The full set (what a release ships)
make build TAGS="http ui scheduler memory cli gateway swarm"
```

A bot enabled in `config.yaml` but not compiled into the binary is a startup error of `coddy serve` naming the tag; all other subcommands are unaffected.

---

## Quick start (Telegram)

**Step 1 — create a bot**

Talk to [@BotFather](https://t.me/BotFather) on Telegram:

```
/newbot
```

Save the token it returns. **Never commit the token to version control.** Store it in an environment variable:

```bash
export TELEGRAM_BOT_TOKEN="<your-token>"
```

**Step 2 — find your Telegram user ID**

Send any message to [@userinfobot](https://t.me/userinfobot). It will reply with your user ID. That ID becomes the `admins` list entry.

**Step 3 — add the gateway config**

In `~/.coddy/config.yaml` (or wherever your `config.yaml` lives), add:

```yaml
gateways:
  telegram:
    enable: true
    token: "${TELEGRAM_BOT_TOKEN}"
    admins: [98874093]           # your Telegram user ID
    default_access: "all"
    default_isolation: "individual"
```

**Step 4 — build and run**

```bash
make build TAGS="gateway.telegram"
./build/coddy serve --config ~/.coddy/config.yaml
```

Open Telegram, find your bot, send a message. The agent replies in the same chat.

---

## Configuration reference

All gateway config lives under the `gateways` key in `config.yaml`. When running `coddy serve` with the bundled UI, the same fields are editable under **Settings → Gateways → Telegram**; the `gateways` block round-trips through `GET`/`PUT /coddy/config`, so saving settings in the UI preserves it (the bot token is shown in full — use only on trusted networks).

```yaml
gateways:
  telegram:
    enable: false
    # Bot token. Optional: leave empty (or omit) to read it from the TELEGRAM_BOT_TOKEN
    # environment variable (e.g. via .env), the same way provider api_key falls back to
    # NAME_API_KEY. When telegram is enabled but no token can be resolved, the gateway
    # logs a warning and skips the bot instead of failing config validation.
    token: "${TELEGRAM_BOT_TOKEN}"

    # How the bot reaches the Bot API (see "Proxy" below). Left out, or inherit,
    # it follows HTTPS_PROXY, HTTP_PROXY and NO_PROXY of the Coddy process;
    # none connects directly; a URL (http, https, socks5, socks5h) goes through it.
    # proxy: none
    # proxy: "socks5h://127.0.0.1:1080"
    # proxy: "http://proxy.example.com:3128"

    # Bot API 10.1 Rich Messages (see "Rich Messages" below). Default false.
    rich_messages: true

    # Telegram user IDs with elevated privileges.
    # Admins always pass every access check regardless of default_access.
    admins: []

    # Default access level for chats without a per-chat override.
    # Values: "all" | "admins" | "group:<name>"
    default_access: "all"

    # Default session isolation for group chats without a per-chat override.
    # Values: "individual" | "shared" | "admin"
    default_isolation: "individual"

    # Named user groups for group-level access control.
    user_groups:
      - name: "devs"
        user_ids: [111222333, 444555666]

    # Per-chat overrides (optional). chat_id is negative for groups/supergroups.
    chats:
      - chat_id: -1001234567890
        isolation: "individual"
        access: "all"
      - chat_id: -1009876543210
        isolation: "admin"
        access: "admins"
```

### Proxy

`proxy` decides how the bot reaches the Bot API, and it reads like a provider's `proxy` ([Provider proxy](../getting-started/configuration.md#provider-proxy)):

- no value, or `inherit` (the default): the proxy the environment of the Coddy process names - `HTTPS_PROXY` for `https://api.telegram.org`, `NO_PROXY` for the hosts that go direct, never for a loopback address; `ALL_PROXY` is not read. An empty field never meant a direct connection: on a machine with `HTTPS_PROXY` set, the bot goes through that proxy;
- `none`: a direct connection, those variables ignored for the bot;
- a proxy URL: every Bot API request goes through it.

```yaml
gateways:
  telegram:
    proxy: none                       # the machine's proxy cannot reach Telegram
    # proxy: "socks5h://127.0.0.1:1080"  # or "http://proxy.example.com:3128"
```

Supported schemes: `http`, `https`, `socks5`, `socks5h`; with either SOCKS scheme the proxy resolves host names. In the web UI the field is the **Ignore system proxy** switch, which writes `none`, above the **Proxy URL** input (**Settings → Gateways**, Telegram block). `coddy --dry-run` asks `getMe` the way the bot will, through the same route.

### Rich Messages

Set `rich_messages: true` to use the [Bot API 10.1 Rich Messages](https://core.telegram.org/bots/api#rich-messages) transport instead of the legacy Telegram Markdown subset:

```yaml
gateways:
  telegram:
    rich_messages: true
```

| Aspect | Legacy (default) | `rich_messages: true` |
|--------|------------------|------------------------|
| Final message | `mdToTelegram` downgrades headings/tables to plain text | Agent's native Markdown sent verbatim via `sendRichMessage` — headings, tables, task lists, fenced code, footnotes, LaTeX all render |
| Streaming (private chats) | progressive `editMessageText` of a live message | ephemeral `sendRichMessageDraft` preview (30 s, animated) |
| Tool activity | `⚙️ toolname…` line, dropped from the final message | live `<tg-thinking>` placeholder during streaming **and** one collapsed `<details>` block per executed tool (name + output, `❌` on failure) in the final message |
| What the session sees | what the person typed | what the person typed |
| System prompt block | the legacy subset, spelled out for the turn | full GFM renders; keep it short for a phone |

**Behaviour notes:**

- **Group chats** don't get draft streaming (`sendRichMessageDraft` is private-chat only); the bot sends the final `sendRichMessage` after the turn, showing a typing indicator while it works.
- **Drafts are ephemeral** — they expire after ~30 s and are never persisted; the turn is finalized by a separate `sendRichMessage`. There is no `editRichMessage` in Bot API 10.1.
- **Graceful fallback** — if a rich send fails (e.g. the Bot API server doesn't support 10.1), the gateway automatically falls back to the legacy formatted send, so the bot never goes silent.
- Requires a Bot API server that implements Bot API 10.1.

### Access levels

| Value | Who can interact |
|-------|-----------------|
| `all` | Anyone who can write to the chat |
| `admins` | Only user IDs listed in `admins` |
| `group:<name>` | Members of the named `user_groups` entry (admins always pass) |

Access is checked on every incoming message. Denied messages are silently dropped.

### Session isolation modes

Applies to **group and supergroup chats only**. Private chats are always per-user regardless of this setting.

| Mode | Session scope |
|------|--------------|
| `individual` | Each group member gets their own private session |
| `shared` | All members of the group share one session |
| `admin` | Only admin users can interact; all admins share one session |

Example: a shared DevOps bot for a team chat uses `shared`. A personal assistant added to a group uses `individual`.

### Per-chat overrides

Use `chats` to override `isolation` and `access` for specific chats:

```yaml
gateways:
  telegram:
    default_access: "all"
    default_isolation: "individual"
    chats:
      - chat_id: -1001111111111   # team group: shared session, all members
        isolation: "shared"
        access: "all"
      - chat_id: -1002222222222   # private project: admins only
        isolation: "admin"
        access: "admins"
```

### User groups

Define named groups of Telegram user IDs and reference them in `access`:

```yaml
gateways:
  telegram:
    user_groups:
      - name: "devs"
        user_ids: [111, 222, 333]
      - name: "qa"
        user_ids: [444, 555]
    chats:
      - chat_id: -1001234567890
        access: "group:devs"    # only devs (+ admins) can use the bot here
```

---

## Running the gateway

The gateway is not a command of its own. `coddy serve` runs every subsystem the
configuration enables, and the bot is one of them:

```yaml
gateways:
  telegram:
    enable: true
```

```bash
coddy serve [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `$CODDY_HOME/config.yaml` | Path to config file |
| `--home` | `~/.coddy` | Agent state directory (`CODDY_HOME`) |
| `--cwd` | process cwd | Default session working directory |
| `--sessions-dir` | `$CODDY_HOME/sessions` | Where session bundles are stored |
| `--log-level` | from config | `debug\|info\|warn\|error`, or a comma-separated spec with per-component overrides such as `info,gateway.telegram=debug` (see [Debugging a chat](#debugging-a-chat)) |
| `--gateway` | from config | Override `gateways.telegram.enable` for this run |
| `--http=false` | — | Run the bot without serving the HTTP API |

Typical production invocation:

```bash
coddy serve \
  --config /etc/coddy/config.yaml \
  --home /var/lib/coddy \
  --sessions-dir /var/lib/coddy/sessions
```

That process serves the web UI on `127.0.0.1:12345` as well, because
`httpserver.enable` defaults to true. A bot and nothing else is
`--http=false`, or `httpserver.enable: false` in the file.

If the binary was built without a gateway tag, enabling the bot is a startup
error naming the tag rather than a warning nobody reads.

The process blocks until `SIGINT` or `SIGTERM`. Each adapter runs in its own goroutine with automatic restart on error (5-second backoff). On shutdown the poller stops taking new messages first and the turns already generating are given up to 20 seconds to finish, so an answer is not cut off mid-sentence.

**With Docker Compose** — the repo's compose files run `coddy serve`, so turning
the bot on is a line in the mounted `config.yaml`, not a second container:

```bash
export TELEGRAM_BOT_TOKEN="<bot-token>"          # or leave it in $CODDY_HOME/.env
docker compose -f docker-compose.dev.yml up -d --build
docker compose -f docker-compose.dev.yml logs -f coddy   # expect: "telegram bot connected"
```

Running the bot as a service separate from the web UI is still possible - two
`coddy serve` processes, one with `--http=false` and one with `--gateway=false`
- but they then have separate session managers again, and a chat conversation
is no longer live in the browser. Prefer one process unless you need the
isolation.


---

## Debugging a chat

A command that appears to do nothing - a `/model` tap that leaves the model unchanged, a message the bot never answers - leaves no trace at `info`. That level carries what succeeded (connecting, sessions loaded and cleared, a model or mode applied) and what failed loudly enough to warn; an update that was quietly dropped, or a tap that never arrived, is in neither list. `gateway.telegram` at `debug` records the whole path instead: every update as it arrives, why one was dropped (access denied, an admin-only chat, a group message not addressed to the bot, a full worker queue), each recognised command, each menu the bot builds with the session it belongs to, and each callback with the model or mode it resolved to and whether it applied.

Raise that one component and leave the rest of the process alone:

```yaml
logger:
  level: "info"
  levels:
    - component: "gateway.telegram"
      level: "debug"
```

For a single restart under systemd, the flag carries the same spec and needs no edit to the config file:

```bash
coddy serve --log-level "info,gateway.telegram=debug"
```

Every record keeps its `component` attribute, so a file that mixes subsystems still filters:

```bash
grep '"component":"gateway.telegram"' /var/log/coddy/coddy.log
```

A switch that lands is reported at `info`, so the confirmation is in the log without raising anything: `telegram: model applied` and `telegram: mode applied` name the session and the new value, `telegram: session resumed` names the session the chat left and the one it moved to. A tap that reaches the bot and fails logs why at `warn`, equally visible: `telegram: callback session` when the session cannot be loaded, `telegram: callback model unknown` when the button names a model that is no longer configured, `telegram: callback session unknown` when a resume button names a session deleted since the keyboard was sent, `telegram: resume session` when the chosen bundle cannot be loaded, and `telegram: set model` when the manager refuses the change. At `debug`, `telegram: resume menu` records each page of the picker with the chat's current session and `telegram: resume query` records the words after `/resume` with how many sessions they matched. Silence at `warn` and nothing at `debug` means the update never arrived - check the bot token, the ACL, and whether another process is polling the same bot, since Telegram delivers each update to one long poll only.

### Debugging against a fake Bot API

The log tells what the bot did with an update; it does not let you send one
without a phone, a token and a model behind the answer. `cmd/tgfake` does: a
stand-in Bot API server that answers every method the gateway calls
(`getMe`, `getUpdates` with real long polling, `sendMessage`,
`editMessageText`, `answerCallbackQuery`, the Bot API 10.1 `sendRichMessage`
and `sendRichMessageDraft`, ...), keeps the chats it is sent, and serves a page
where you are the person in the chat - the bot's inline keyboards are buttons.
With `--llm` it also serves a scripted model, so the whole stand runs with no
network at all:

```bash
go run ./cmd/tgfake --llm --llm-delay 50ms      # Bot API + model on 127.0.0.1:18790
```

Point `coddy serve` at it with **`CODDY_TELEGRAM_API_BASE`**, the origin the
`--dry-run` probe honours as well, and give it the stub as its provider (the
command prints this snippet on start):

```yaml
providers:
  - name: stub
    type: openai
    api_base: "http://127.0.0.1:18790/v1"
    api_key: "sk-tgfake"
models:
  - model: stub/coddy-demo
agent:
  model: stub/coddy-demo
httpserver:
  enable: false                     # the stand is the bot alone; drop this to watch the chat in the web UI too
gateways:
  telegram:
    enable: true
    token: "123456:fake"            # any token; the fake accepts all of them
    admins: [4242]                  # the page's user: /resume, approvals and settings are an admin's
logger:
  levels:
    - component: gateway.telegram
      level: debug
```

```bash
export CODDY_TELEGRAM_API_BASE=http://127.0.0.1:18790   # PowerShell: $env:CODDY_TELEGRAM_API_BASE="http://127.0.0.1:18790"
coddy serve --dry-run --config stand.yaml               # ok  gateways.telegram: token accepted by the Bot API, bot @coddy_fake_bot
coddy serve --gateway --http=false --config stand.yaml  # telegram: api base override ... telegram bot connected
```

Then open `http://127.0.0.1:18790/`, type `hello`, send `/model` and tap a
model (add a second entry to `models` for the keyboard to offer a choice), try
a settings command such as `/plan --once What can you do?`, and read the Bot
API calls on the right as the log fills on the left.

![The chat page of cmd/tgfake on the dark scheme: the person's side of the chat on the left with the bot's /model keyboard as buttons and a /plan --once message answered, every Bot API call the bot made listed on the right](../assets/tgfake-chat-dark-1280.png)

*The chat page of `cmd/tgfake`: a greeting answered by the scripted model, the `/model` keyboard with the tap applied, a message sent in plan mode for one turn, the notice of a bare `/ask`, and on the right every Bot API call the bot made, `getUpdates` polls hidden.*

The same page is an HTTP API, which is what a script or a coding agent drives:

| Route | Body / answer |
|-------|---------------|
| `POST /sim/message` | `{"chat_id": 4242, "user_id": 4242, "username": "alice", "text": "hello"}`; `chat_type: group`, `mention: true` and `reply_to_message_id` for the group paths. A leading `/word` becomes a `bot_command` entity. |
| `POST /sim/callback` | `{"chat_id": 4242, "label": "stub/coddy-mini"}` taps the button by its text (the `✓` prefix is ignored), or `{"message_id": 4, "data": "model:stub/coddy-mini"}`. |
| `GET /sim/chat/4242` | the transcript: messages, keyboards after every edit, drafts, `typing`; `?format=text` for `grep`. |
| `GET /sim/outbox?method=sendMessage&since=10` | every Bot API call with its parameters and the answer; `/sim/outbox/count?method=...` for a script. |
| `POST /sim/fault` | `{"method": "sendMessage", "code": 429, "retry_after": 2, "times": 1}` makes the next `sendMessage` fail like a flood; `"method": "*"` fails everything until `DELETE /sim/fault`; `"contains": "<details>"` narrows the fault to calls whose parameters carry that text, which is Telegram refusing one entity rather than the method. |
| `POST /sim/webapp/launch` | `{"chat_id": 4242, "url": "https://coddy.example.com/"}` opens a Mini App the way a client does and answers `{url, init_data}`: the address with the launch parameters in its fragment and the launch data signed with the bot's token. Without `url` it opens the chat's menu button, which only a private chat has. |
| `POST /sim/reset` | forgets chats, outbox, faults and menu buttons. Update ids keep growing, so a polling bot is not confused. |

The fake is strict where Telegram is. An edit that changes nothing, an edit
of a message that was never sent, a text over 4096 characters, a reply to a
message the chat does not hold (unless `allow_sending_without_reply` says to
send it anyway), an answer to a callback query the fake never issued, a second
answer to one it did (a query takes one answer, so a failure told in an alert
after the tap was acknowledged never reaches the user), a `reply_markup` whose
`inline_keyboard` is not an array (`null` included, what an empty keyboard of
the Go library encodes to), a keyboard whose `callback_data` is longer than
64 bytes (`BUTTON_DATA_INVALID`), a button with no action, and a `web_app`
button outside a private chat or to an address that is neither https nor plain
http on this machine are refused with Telegram's own error. The stand is
stricter in one place: it refuses a button with more than one action, which
Telegram would read as its first. A keyboard that works on the stand works in
a chat.

It also remembers `allowed_updates` the way Telegram does. A bot token that
once ran under another framework may be subscribed to messages alone, and a
poll that names no kinds inherits that: text arrives, keyboard taps are
dropped before anyone sees them. The gateway therefore asks for `message` and
`callback_query` on every poll. `GET /sim/state` shows the subscription in
force; `tgfake.Options.AllowedUpdates` starts a bot under a stale one, which
is how the polling feature reproduces the case. On a real bot,
`getWebhookInfo` reports the same field.
The subscription is applied when an update is created: changing it preserves
already queued updates and cannot recover events excluded at creation.

The page is a Telegram client for [Mini Apps](#mini-app) as well. A `web_app`
button, or the menu button a bot set to a Mini App (left of the message
field), opens the app in a phone frame beside the chat, and the page is its
Telegram, the way web.telegram.org is: it signs the launch data with the
bot's token (the one of the latest Bot API call, or `--token`), answers the
viewport, safe area and theme requests, shows **Back** when the app asks for
it, paints its header with the colour the app sets, and logs every event both
ways under the phone. The controls above the phone switch the size, the
window between expanded and half open (the frame clips the full-height
WebView, as a phone does), Telegram's theme, fullscreen insets and an Android
keyboard. To open the web UI there, let the stand's `coddy serve` serve it and
advertise it:

```yaml
httpserver:
  allow_insecure: true                 # no sign-in on the stand, advertised on purpose
gateways:
  telegram:
    mini_app:
      url: "http://127.0.0.1:12345/"   # plain http passes on a loopback host
```

Then send `/app` and tap **Open in Coddy**. The page and a web UI on the same
host are one site to a cookie, so the frame keeps a sign-in the way a phone's
WebView does; the page on `localhost` with the web UI on `127.0.0.1` is a
cross-site frame, the way Telegram Web is. `npm run check:telegram` drives the
same stand from a script (*Checking the Telegram Mini App* on the
[web UI page](web-ui.md)).

![The chat page of cmd/tgfake with a Mini App open: the chat on the left with the bot's answers, the web UI in a half-open phone frame in the middle with the mode sheet over its docked composer and Telegram's header with Back and Close above it, the Bot API calls on the right](../assets/tgfake-mini-app-dark-1280.png)

*The chat page of `cmd/tgfake` with the web UI open as the bot's Mini App, half open: the mode sheet and the composer stay in the part the phone shows, the chat above it.*

Rich-message previews expire 30 seconds after their last successful revision.
The chat page and `/sim/chat/{id}` stop showing expired drafts; reading the
chat or writing another draft also removes expired entries from its storage.
The outbox retains the calls for debugging, and persistent messages remain.

`--llm-answer` (repeatable) scripts the model's replies in turn, `--llm-script
rules.json` matches them by substring (`[{"match": "weather", "answer":
"Sunny."}]`), and without either the model echoes the prompt - the person's
message, not the `<turn_context>` block Coddy appends to every request. Rules are the
reliable choice: the title a session derives from its first message is one more
model call, so a list of answers advances a step earlier than the chat shows.
A rule can also make the model act: with `"tool": {"name": "run_command",
"arguments": {...}}` the first request of a matching turn gets that tool call,
and the request carrying its result gets the rule's `answer` - enough to start
a background command, or to meet a permission prompt, without a model.
The streamed answer arrives one word per `--llm-delay`, long enough for the
live `editMessageText` path, or the draft path with `rich_messages: true`, to
run.

**`examples/gateway/tg_e2e_offline.sh`** does all of the above in one go -
builds `tgfake`, writes a temporary home, boots `coddy serve` against it, sends
`hello` and checks the reply, then leaves the session with `/clear`, comes back
to it from the `/resume` keyboard and checks that the next message landed in
that bundle, and finally asks the agent to "start the tests" - a tool rule
starts a failing command in the background with `notify_on_finish` - and waits
for the [woken turn](#woken-turns-land-in-the-chat) to reach the chat, the note
and then the answer, with no HTTP server in the process - and `TG_E2E_KEEP=1`
leaves the stand running with the page URL printed. It runs in Git Bash on Windows as well.

The variable is not only for the fake: a self-hosted Bot API server
(`telegram-bot-api` for large files or a local network) is pointed at the same
way, and `coddy serve --dry-run` confirms which server answered before the bot
starts.

---

## Bot interaction model

### Private chats

Every user who starts a private conversation with the bot gets their own isolated session. No configuration needed. Every message is for the bot, with or without a mention.

### Replies

A person who replies to a message asks about it. The agent receives the replied-to message quoted in front of what the person wrote, its first line naming the author:

```
> Anna:
> the build is red since noon

why?
```

This works in a private chat and in a group alike, for a reply to the bot's own answer and for a reply to somebody else's message (in a group, together with a mention of the bot). A mention alone under a reply asks the agent to deal with the quoted message. The quote is part of the user message, so the transcript in the web UI reads the way the chat did; a settings command is never quoted. The model is told about the convention for each turn ([What the messenger needs](#what-the-messenger-needs-and-where-it-is-said)).

### Group chats

A group is where many people talk, so the bot **only responds** when explicitly addressed:

1. A message that **@mentions** the bot (`@coddy_agent_bot hello`)
2. A **direct reply** to a previous bot message

Commands follow the same rule: in a group a bot command or a settings command needs the mention, which Telegram writes as `/clear@coddy_agent_bot` (the command menu of a group inserts that form), or a reply to the bot's message. A bare `/clear` in a group is left to the people in it.

### What somebody who is not an admin may do

The bot sees who wrote every message, and its admins (`gateways.telegram.admins`) can do everything. Anybody else the access rules let in gets the conversation and nothing that changes the agent:

- the turn may call only reading tools (`read`, `grep`, `glob`, `print_tree`) kept inside the session's working directory and out of the agent's home, the built-in documentation, a web search, the session's plan and to-do list and a question back; anything else - the configuration, `switch_model`, subagents, worktrees, the scheduler, background tasks, servers, shell commands, writes, fetching a page, every MCP tool - is refused, and the model is told why;
- an `@` mention attaches files of the working directory only, and no web page;
- every call that needs approval asks the bot, whatever the session's permission mode, and the bot refuses it; the "always allow" grants an admin gave in a shared session do not apply;
- a permission button is answered by an admin only, also in a group whose session is shared;
- a woken turn and a background subagent's request take the rights of whoever the conversation belongs to: a private chat's or an individual group session's person, never a shared group's.

The tool definitions the model sees stay the same, so the provider's prompt cache holds across admins and everybody else in one shared session. A bot with an empty `admins` list therefore lets everybody only chat; `coddy serve --dry-run` warns about it.

What changes the settings is the admins' in a group: the settings commands (`/agent`, `/plan`, `/ask`, `/think`, `/nothink`, `/reasoning`, `/model <id>`), the `/model` menu and its taps, `/clear` and `/resume`. Anybody else gets *Only the bot's admins can change settings in this chat.* and nothing changes; in a private chat each person still changes their own session. The `/mcp` switches change the whole agent's configuration, so a tap on them is the admins' in every chat, and so is `/resume`, which reaches every session the server keeps, the operator's own included.

When `isolation` is `admin`, the bot additionally ignores everyone who is not in the `admins` list.

### Commands

| Command | Available to | Effect |
|---------|-------------|--------|
| `/start` | all users | Greeting and quick introduction. |
| `/help` | all users | Lists all available commands. |
| `/model [id]` | all permitted users (admins only in a group) | Bare, opens an inline keyboard to switch the active LLM model (from the configured `models` list); with an id, switches to it at once. An admin's pick is also what the next fresh chat of the bot starts on; anybody else's stays in their own session. |
| `/mcp` | all permitted users (its switches: admins) | Lists global and project MCP servers with status and tool count; a server that is switched off while its status is a trust verdict reads `off` as well (`checkout · needs_approval · off · 0 tools`). Buttons enable or disable servers already trusted for the workspace; a project server nobody approved gets none, because project trust is granted through the CLI, console or web UI. A tap that fails says why on the first line of the menu message, above the menu drawn afresh, or alone and without buttons when the server list cannot be read. |
| `/agent`, `/plan`, `/ask` | all permitted users (admins only in a group) | Switch the session mode. |
| `/reasoning <level>`, `/think [level]`, `/nothink` | all permitted users (admins only in a group) | Set the reasoning level, or turn thinking on or off where the model's provider can. Not in the command menu. |
| `/context` | all permitted users | Displays the current session's context window usage broken down by category (conversation, system prompt, tool definitions, rules, skills, MCP). |
| `/goal <objective>`, `/goal`, `/goal pause`, `/goal resume`, `/goal clear` | admins (`admins`) to change, all permitted users to show | Sets a session goal and starts working on it, shows it, pauses, resumes or removes it ([Session goal and supervisor](../features/session-supervisor.md)). Every turn the supervisor starts is announced with a message of its own, after the answer of the previous one. |
| `/resume [id or title]` | admins (`admins`) | Continues another session. Alone it opens an inline keyboard over the sessions the server keeps, newest first, eight per page, the chat's own session marked; a tap binds the chat to the one chosen. With words after it, the session whose id they are, or whose id starts with them or whose title contains them (those two case-insensitively), is resumed at once; several matches come back as the keyboard, and no match is answered with a message. The reply names the model and the reasoning level the resumed session runs on, its own. The session left behind stays loaded. |
| `/app` | admins (`admins`) | Only with `gateways.telegram.mini_app.url` set: answers with **Open in Coddy**, a button that opens the chat's conversation in the web UI - as a Mini App in a private chat, as a link in a group ([Mini App](#mini-app)). |
| `/clear` | all permitted users (admins only in a group) | Starts a new session for the current user/chat context. The old session is removed from memory (persisted history remains on disk); `/resume` brings it back. |

When the supervisor starts a continuation in the chat, the bot posts a separate `🔁` note with the remaining work before the next answer. The same prompt is saved as its own transcript row, so `/resume` on another surface sees it too.

The settings commands take `--once` or `--count=N` to change a setting for the next messages only, and a message may follow them: `/plan --once how would you split this package?` plans one answer and leaves the chat in its mode. A command alone runs no turn and is answered with a line saying what changed. `/permissions` is not a bot command: the bot approves its chat agent's tools itself, so the session's permission mode would only change what other surfaces watching the session ask ([Session settings](../features/session-settings.md)).

---

## What the messenger needs, and where it is said

A messenger has its own dialect and its own shape of screen. Coddy says both in
two places that belong to the gateway, and neither of them touches the
conversation the session keeps.

### The model is told, for that turn

Before a chat turn runs, the adapter hands the session a block of the **system
prompt** describing how to answer into this chat: the emphasis Telegram
renders, the headings it does not, that a table becomes a wall of pipes, that
identifiers belong in backticks because a bare `_` opens italics, and that a
chat is a narrow column on a phone. It lives in
`external/gateway/telegram/prompt.go`, and `rich_messages: true` sends a
different one - there the chat renders GitHub-flavoured Markdown in full, so
the only thing worth saying is how much of it a phone screen wants.

The block belongs to the **turn**, not to the session
(`session.PromptRunOpts.SurfaceSystemPrompt`). Nothing of it is persisted, so
the transcript holds the conversation and not the surface that ran it, and a
browser turn on the same session is built without it. That does mean the prompt
prefix differs between surfaces, so a turn that follows one from elsewhere does
not reuse its cached prefix. It is the deliberate price of letting each
integration speak for itself instead of teaching the core about messengers.

### The answer is rendered, on its way out

A model does not always comply, and a chat that shows a raw `##` is a worse
answer than one the gateway quietly fixed, so the reply is also converted as it
leaves, in `external/gateway/telegram/markdown.go`.

For the legacy send: ATX headings and `**bold**` become `*bold*`, `__x__`
becomes `_x_`, an asterisk bullet becomes `•`, a table is flattened to plain
rows and a horizontal rule to a separator line. Fenced blocks and inline code
spans are set aside before any rule runs and put back untouched, so a Go `**p`
or a `# comment` inside a block reaches the chat as the model wrote it. The live
streaming preview is sent with no parse mode - half a sentence is half a markup
- so it gets the same conversion with the emphasis markers dropped rather than
shown as punctuation. If Telegram still refuses to parse a message, the sender
resends it without a parse mode: a stray asterisk in prose costs formatting,
never the reply.

With `rich_messages: true` there is nothing to downgrade - the agent's Markdown
goes out verbatim - and the fallback path is the legacy rendering above.

### Adding an integration

Both halves are the new adapter's to write: a `prompt.go` saying what its
messenger needs, and a renderer for its syntax. Nothing in `internal/` learns
about it.

---

## Writing a new adapter

To add, for example, a Discord adapter alongside Telegram, follow this pattern.

### 1. Implement the Adapter interface

Create `external/gateway/discord/bot.go`:

```go
//go:build gateway || gateway.discord

package discord

import (
    "context"
    "fmt"

    "github.com/EvilFreelancer/coddy-agent/external/gateway"
    "github.com/EvilFreelancer/coddy-agent/external/gateway/access"
    "github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
    "github.com/EvilFreelancer/coddy-agent/internal/config"
)

type Bot struct {
    cfg   *config.DiscordGatewayConfig  // add to config.GatewayConfig
    store *sessionstore.Store
    // ... discord client, session runner, logger
}

func New(cfg *config.DiscordGatewayConfig, runner SessionRunner, cwd string, log *slog.Logger) *Bot {
    return &Bot{cfg: cfg, store: sessionstore.New()}
}

// Name satisfies gateway.Adapter.
func (b *Bot) Name() string { return "discord" }

// Start connects and polls. Must block until ctx is cancelled.
func (b *Bot) Start(ctx context.Context) error {
    // connect discord client
    // poll or use websocket events
    // for each message: call b.handleMessage(ctx, msg)
    return nil
}
```

The `SessionRunner` interface (`external/gateway/telegram/bot.go`) is what you need from the session manager:

```go
type SessionRunner interface {
    EnsureHTTPSession(ctx context.Context, sessionID string, defaultCWD string) (*session.State, error)
    HandleSessionPromptWithSender(ctx context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error)
    ForgetLiveSession(sessionID string)
    HandleSessionSetConfigOption(ctx context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error)
    HandleSessionList(ctx context.Context, params acp.SessionListParams) (*acp.SessionListResult, error)
    Cfg() *config.Config
}
```

`session.Manager` already satisfies this interface — pass it directly. `HandleSessionSetConfigOption` is what the `/model` inline keyboard calls, and the settings commands (`/agent`, `/model <id> --once`, ...) reach the session as prompt text through `HandleSessionPromptWithSender`, whose manager takes them off the start of the message; `HandleSessionList` is what `/resume` offers to the chat; `Cfg()` returns the loaded config (used by `/model` to list available models).

### 2. Add serve_name.go and its stub

Each adapter is started by a function of its own in `external/gateway`, in a file under the adapter's tag, with a stub for builds without it. The Telegram pair is `serve_telegram.go` / `serve_telegram_stub.go`, the Pachca pair `serve_pachca.go` / `serve_pachca_stub.go`:

```go
//go:build gateway || gateway.discord

package gateway

// DiscordAvailable reports whether this binary carries the Discord adapter.
const DiscordAvailable = true

// ServeDiscord builds the Discord bot and runs it until ctx is cancelled.
func ServeDiscord(ctx context.Context, opts Options) error {
    bot := discord.New(&opts.Cfg.Gateways.Discord, opts.Mgr, opts.DefaultCWD,
        logger.Component(opts.Log, logger.ComponentGatewayDiscord), storePath, opts.Mirror)
    NewHub(logger.Component(opts.Log, logger.ComponentGateway), bot).Start(ctx)
    return nil
}
```

The stub (`//go:build !(gateway || gateway.discord)`) sets `DiscordAvailable = false` and returns an error naming the tag. Give the adapter a session map file of its own: two `sessionstore.Store` instances rewriting one file would undo each other's writes.

### 3. Implement acp.UpdateSender

Each message dispatch needs a `Sender` that implements three methods:

```go
type UpdateSender interface {
    SendSessionUpdate(sessionID string, update interface{}) error
    RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error)
    RequestQuestion(ctx context.Context, params acp.QuestionRequestParams) (*acp.QuestionResult, error)
}
```

- `SendSessionUpdate` receives streaming events: `acp.MessageChunkUpdate` carries a text delta in `update.Content.Text`; `acp.ToolCallUpdate` is a tool start notification. Buffer text chunks and send them as a single message in `Flush()` after the agent turn.
- `RequestPermission` should auto-approve the chat agent's own requests (the admin configured the bot deliberately): return `&acp.PermissionResult{Outcome: "allow", OptionID: "allow"}`. A request stamped with a subagent's own mode below `bypass` (`params.EffectivePermissionMode`) is not the admin's to wave through: ask the chat, as `external/gateway/telegram/permission.go` does, or deny it.
- `RequestQuestion` can send the question text to the chat and return an empty answer, or implement a proper reply-based flow.

See `external/gateway/telegram/sender.go` for a working reference.

### 4. Add a build tag

Follow the existing pattern:

- `external/gateway/discord/*.go` → `//go:build gateway || gateway.discord`
- `external/gateway/discord/*_test.go` → same constraint
- Stub (if needed) → `//go:build !(gateway || gateway.discord)`

Add the tag to the constraint of the shared files - `gateway.go`, `hub.go`, `access`, `sessionstore`, `proxyutil` - and to `TEST_TAG_SETS` in the `Makefile`.

### 5. Register the subsystem in coddy serve

Add a `serve.Kind` for the bot in `internal/serve/subsystem.go` and a descriptor in `subsystems()` of `cmd/coddy/serve.go`: its `ConfigKey`, the `BuildTag`, `Available: gateway.DiscordAvailable`, `NeedsSessions: true`, a `Fingerprint` over every setting a rebuilt bot would read differently, and `Run` calling `gateway.ServeDiscord`. A bot that is a subsystem of its own is rebuilt alone when its settings change, and is refused by name when it is enabled in a binary without its tag. Add a line to the banner (`printServeBanner`) and a probe of the token to `internal/dryrun`.

---

## The same session in the chat and in the browser

With `httpserver.enable` and `gateways.telegram.enable` both on, a Telegram
conversation and the web UI are two views of one session.

- **The chat session appears in the browser.** A chat conversation is an
  ordinary session with an ordinary `sess_` id - where a person is sitting
  decides nothing about the session behind the conversation - so
  `GET /coddy/sessions` lists it beside the sessions started in a terminal or
  a browser tab, and opening one loads the same transcript.
- **A chat turn streams into the browser while it runs.** The gateway publishes
  its turn into the session's composer relay - the relay the HTTP server's own
  turns and woken turns use - so a tab watching that session sees the tokens as
  they arrive, not after the fact.
- **The browser watches; the chat answers.** Session updates fan out to both
  surfaces, but permission requests and questions go only to the chat, because
  it is the only one with somebody reading. A watcher is a spectator. The one
  exception is a background subagent that asks after the turn ended: that
  prompt is offered to the chat and to the browser at once, and the first
  answer wins.
- **Continuing works in either direction.** Reply in the browser and the next
  `/context` in Telegram shows it; reply in Telegram and the browser has it on
  the next load. Only one turn runs at a time: the session's turn lock is a
  file lock, so a message that arrives while a browser turn is in flight is
  answered with a busy notice instead of interleaving.
- **A turn already being watched is left alone.** If a browser turn is running
  on the session, an arriving chat message does not take over its stream - the
  chat message gets the busy answer a moment later anyway.

If a session is deleted from the browser, the chat's mapping in
`gateway_sessions.json` still points at that id; the next message finds no
bundle and starts a fresh transcript under it. The conversation resets, which
is what deleting it meant.

## Mini App

The bot can open the web UI as its [Mini App](https://core.telegram.org/bots/webapps): a tap on the menu button beside the message field, or on the button `/app` answers with, opens the web UI inside Telegram, on the chat's own conversation. That conversation is the session the browser shows ([above](#the-same-session-in-the-chat-and-in-the-browser)), so the chat and the Mini App are two views of it. What the web UI does inside Telegram is described under *Telegram Mini App* on the [web UI page](web-ui.md).

Telegram opens a Mini App from the person's phone, over https only, so the web UI has to be reachable there:

1. Publish `coddy serve` behind a TLS proxy (Caddy, nginx, a tunnel) under a public address such as `https://coddy.example.com/`, and keep `httpserver.host` on loopback behind it.
2. Turn sign-in on with `coddy serve set-password`, or `CODDY_HTTP_USER` and `CODDY_HTTP_PASSWORD`. The menu button is shown to everybody who opens a private chat with the bot, whatever `default_access` says, so the bot does not advertise a web UI that asks for nothing: with no sign-in and no token it leaves the menu button alone, `/app` says what is missing, and `coddy serve --dry-run` warns. `httpserver.allow_insecure: true` tells it you publish the web UI open on purpose.
3. Name the address:

```yaml
gateways:
  telegram:
    mini_app:
      url: https://coddy.example.com/
      menu_button: true     # the default
```

Only the bot's admins (`gateways.telegram.admins`) open the web UI from the chat. The Mini App signs an admin in by its launch data, with no password: Telegram signs it with the bot's token, the server checks the signature, takes each launch once and at most an hour old, and opens a session of its own (`POST /coddy/auth/telegram`) that also works behind a token-only gate and ends when the token changes or the person stops being an admin. Anybody else who taps the menu button sees *Only the bot's admins can open Coddy from Telegram.* above the ordinary sign-in form (or alone, when the server has no form), and `/app` answers them the same way. The web UI is the whole agent; the chat is what everybody else gets.

On every start the bot then points its menu button at `url`, labelled **Coddy**, and adds `/app` to its command list. `/app` answers with **Open in Coddy**: in a private chat a button that opens the chat's conversation as a Mini App (`url?session=<id>`), in a group an ordinary link to the same address, since Telegram allows Mini App buttons in private chats only. Before the chat has a conversation, the button opens the start screen.

The bot borrows the menu button from @BotFather and gives it back. The first time, it keeps the button it replaces in `gateway_sessions.json`, and when Telegram does not say which button that is, it leaves the menu button alone until the next start. When you set `menu_button: false`, empty `url` or the web UI stops asking for sign-in, it puts that button back, as long as the button still opens the address the bot set. A button of yours that opens the same address as the bot's comes back too, unless the web UI stopped asking for sign-in: then the chat shows the commands. A button you changed in @BotFather in the meantime stays as you left it. The bot settles the button when it starts, so turning the bot off (`gateways.telegram.enable: false`) leaves the button opening the web UI: to take it back, set `menu_button: false` and start the bot once. Without `url` the bot never touches the menu button, so a Mini App you wired in @BotFather by hand keeps working: the web UI adapts to Telegram whoever opened it.

`coddy serve --dry-run` asks `url` for the web UI and says when the bot will hold it back. Telegram Web (web.telegram.org) runs a Mini App in a frame of another site, where the browser drops the sign-in cookie; the sign-in screen then says so and links to Coddy in a tab of its own. To try all of this with no Telegram and no phone, open the Mini App from the offline stand ([Debugging against a fake Bot API](#debugging-against-a-fake-bot-api)).

## Woken turns land in the chat

The agent in a chat can start a long command or a subagent in the background
and end its turn: when the task ends, the process wakes the agent, unless the
call set `notify_on_finish: false` ([Background tasks](../features/background-tasks.md#waking-the-agent-when-a-task-finishes)).
The woken turn belongs to the chat bound to the session, so the bot runs it
there, through the chat's own sender, exactly like a message the person sent:

- the chat first receives a note of its own, above the answer:
  `🔔 Woken by a finished background task: bg_3 make test, failed, exit 2, 1m 30s`
  (one line per task when several ended together);
- then the answer streams and is finalized like any other, with the same
  surface prompt and the same Markdown rendering;
- a browser watching the session follows it through the composer relay, and
  the chat's permission rules apply: the chat's agent is allowed what it asks,
  a subagent is asked about in the chat.

This needs no HTTP server. Under `coddy serve` the process owns the waker and
offers each woken turn first to the surface that owns the conversation - the
bot, when a chat is bound to the session - and only then to the HTTP server; a
`coddy serve --gateway --http=false` wakes the chat all the same. A session no
chat is bound to (the chat moved away with `/clear` or `/resume`) is not the
bot's: it runs in the web UI's relay, or with no surface at all through the
manager, and the chat hears nothing of it. The bot takes a woken turn only
while it is connected; a woken turn that finds the chat's own turn still
running waits for it, as it does on every surface.

## Pictures the agent looked at

When the chat's agent reads an image file and the session's model reads images
(`models[].multimodal: true`), the bot sends that picture into the chat as a
photo, captioned with the file's name, as soon as the `read` finishes
([Images](../features/images.md)). The person sees what the agent looked at
before the answer comes, in private chats and groups alike. The photo is the
copy Coddy kept with the session, so it is the picture the model was shown
even when the workspace file changed since. Without Rich Messages the live
message the answer grows in was sent before the call ran, so the bot drops
it once a photo is posted and the answer comes below the photo as a new
reply to the question; the old message is deleted when Telegram allows it.

A picture Telegram refuses as a photo - a side too long, a shape too narrow,
photos not allowed in the chat - goes as a document instead; a rate limit, a
failure on the way or a refusal about the chat itself is not retried as a
document. Only the chat's own session sends
pictures: what a subagent reads stays in its session, and the web UI shows it
there. A model without `multimodal` is refused the picture, so nothing is sent
and the `read` reports why.

## Session lifecycle

```
User message arrives
        │
        ▼
sessionstore.SessionKey(gateway, chatID, userID, isolationMode, isGroup)
        │  returns a string key like "tg:chat:-100:user:42"
        ▼
store.Get(key)
        │  returns existing session ID (from gateway_sessions.json),
        │  or mints a new one on first use
        ▼
manager.EnsureHTTPSession(ctx, sessionID, cwd)
        │  loads from disk if persisted, creates fresh otherwise
        ▼
manager.HandleSessionPromptWithSender(ctx, params, sender, nil)
        │  runs the ReAct loop; sends stream events to Sender
        │  Sender streams tokens: first chunk → new Telegram message;
        │  subsequent chunks → progressive editMessageText (throttled);
        │  tool executions → shows "⚙️ toolname…" indicator in the live message.
        ▼
sender.Flush()
        │  replaces the live streaming message with the final formatted text
        │  (markdown.go renders the answer for Telegram: headings, double-star
        │   bold and tables into the legacy subset, fenced code untouched)
        ▼
Session bundle written to disk ($CODDY_HOME/sessions/<id>/)
```

**Session store persistence** — The key→session-ID mapping is persisted in `gateway_sessions.json` inside `$CODDY_HOME/sessions/` (same directory as session bundles). On restart the bot reloads this file and continues existing conversations seamlessly. The same file carries a reserved `$last_model` entry: the model an operator last picked on this gateway (`/model` keyboard or a typed session-scoped `/model <id>`). A **fresh** session — a chat's first contact, or a new session after `/clear` — starts on it instead of `agent.model`; on the gateway's very first use it starts on the alphabetically first configured model. A session that already chose a model, including one with a pick but no messages yet, keeps it.

**`/clear` flow:**

```
store.Reset(key)   → replaces stored session ID in gateway_sessions.json
manager.ForgetLiveSession(oldID)   → drops the in-memory session (disk persists)
Next message → EnsureHTTPSession creates a fresh session for the new ID
```

The old session files remain on disk under the old ID. Use `coddy sessions list` to inspect them, or `/resume` in the chat to come back to one.

**`/resume` flow:**

```
manager.HandleSessionList(...)     → the sessions the server keeps, newest first
                                     (no folder filter: a chat has no cwd of its own)
/resume            → inline keyboard, one button per session: the title, or the
                     id of a session without one, then its age; the chat's own
                     session is marked; eight per page with Prev/Next
/resume <query>    → the session whose id the query is (byte for byte), or
                     whose id starts with it or whose title contains it (case-
                     insensitive); one match is resumed at once, several come
                     back as the keyboard
manager.EnsureHTTPSession(ctx, chosenID, cwd)   → loads the bundle first, so a
                                                   bundle that cannot be read is
                                                   reported here and nothing changes
store.Bind(key, chosenID)   → replaces the stored id in gateway_sessions.json
Next message → runs in the resumed session
```

The session the chat came from stays loaded. `/resume` is a switch, not an
ending - the chat may come straight back - while `/clear` says a conversation
is over, and dropping it from memory belongs there. A tap on a keyboard that
outlived its session - deleted from the web UI since the list was shown -
binds nothing and is answered in the chat, as a reply to the keyboard. It is
not an alert: the bot acknowledges every tap as it arrives, and Telegram takes
one answer per tap.

---

## Security notes

- **Token exposure** — never commit the bot token to version control. Use `"${TELEGRAM_BOT_TOKEN}"` in YAML and export the variable before starting.
- **Permissions** — the gateway auto-approves the chat agent's own tool permission requests so it can work unattended. Restrict `tools.command_allowlist` in `config.yaml` if you want to limit which shell commands the agent can run. A subagent whose definition narrowed its permission mode below `bypass` is not waved through: the bot asks in the chat with **Allow** / **Reject** buttons naming the subagent - during the turn, and after it ended for a background subagent - and only the person whose session asked can answer (in a group with individual sessions another member's tap is ignored and leaves the owner's buttons available). The message reads *Allowed*, *Denied* or *No longer waiting* once it settles.
- **Access control** — set `default_access: "admins"` for bots that should only respond to a specific set of users. Open bots (`default_access: "all"`) will respond to any Telegram user who can write to the chat.
- **`/resume` lists every session of the server** — the sessions started in a console or a browser included, and a permitted user can continue any of them from the chat, which puts their transcripts in front of the model. With the defaults - every permission auto-approved, an unrestricted shell - the bundles on disk were within a permitted user's reach already; with a narrowed tool set (`tools.command_allowlist`, `ask` mode) `/resume` is a new path to other people's conversations. Either way, keep `default_access` narrow on a bot that more than one person can write to.
- **Mini App** — the menu button is shown to everybody who opens a private chat with the bot, whatever `default_access` says, and a tap on it reaches the web UI's address. The bot therefore advertises the web UI only when it asks for a sign-in or a token, or when `httpserver.allow_insecure: true` says it is open on purpose. The launch data Telegram signs (`initData`) is not a sign-in: the web UI keeps it out of its address and never takes it for an identity, and you sign in as in a browser.
- **Network** — the gateway uses Telegram long-polling (not webhooks). No inbound port needs to be open. A Mini App is the exception you choose: its `url` has to be reachable from the people who open it.

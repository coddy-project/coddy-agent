# Pachca gateway

Coddy can answer in [Pachca](https://pachca.com/) (Пачка), the corporate messenger, as an integration bot of your workspace. A person writes to the bot in a direct chat, mentions it in a group chat or replies to it in a thread, and the message starts or continues an agent turn; the answer streams into one message of the chat. The bot runs inside `coddy serve` next to the HTTP server and the Telegram bot, and every conversation is an ordinary Coddy session, so the web UI shows it live.

The adapter is deliberately smaller than the [Telegram gateway](gateway.md): it reads the bot's events history instead of receiving a webhook, so the machine running Coddy needs no public address, and it covers chat turns, a few commands, buttons for the model menu and for subagent permission prompts, and woken turns.

## Contents

- [Set up the bot in Pachca](#set-up-the-bot-in-pachca)
- [Configuration](#configuration)
- [Running it](#running-it)
- [How the bot behaves](#how-the-bot-behaves)
- [Commands](#commands)
- [Answers, limits and formatting](#answers-limits-and-formatting)
- [Permissions and woken turns](#permissions-and-woken-turns)
- [What is not supported](#what-is-not-supported)
- [Troubleshooting](#troubleshooting)

## Set up the bot in Pachca

1. In the web or desktop Pachca app open **Automations → Integrations → Chat bots and webhooks** (Автоматизации → Интеграции → Чат-боты и Вебхуки), press **+** and pick **For several chats**, so the bot can talk to every person and group (a bot "for one chat" is limited to one conversation or channel, threads and direct chats aside). On the **API** tab copy its `access_token`. This is an ordinary integration bot, not a personal token: the bot acts as itself, with the role of an employee, and its messages carry the bot badge.
2. On the bot's **API** tab grant the scopes the adapter uses:
   - `messages:create`, `messages:update`, `messages:read` - posting, streaming edits, recognising replies to the bot;
   - `chats:read` - telling a direct chat from a group;
   - `profile:read` - the bot's own nickname, which a mention names;
   - `users:read` (recommended) - the author's name in a quoted reply;
   - `webhooks:events:read` - reading the events history;
   - `webhooks:events:delete` (recommended) - removing handled events, so the history does not grow.
3. On the **Outgoing webhook** tab:
   - turn on **Save events history** (`events_history_enabled`). Without it the history stays empty and the bot never hears anything; the webhook URL itself can stay empty;
   - subscribe to **New messages** in the "any message" mode and to **Button clicks**;
   - turn on **Ignore own messages**. The adapter filters the bot's own messages anyway, but there is no reason to read them.
4. Add the bot to the chats it should work in. A bot sees open channels by itself; a private conversation or channel needs the bot as a member, and a thread can take the bot directly. A person can write to the bot in a direct chat when the bot is public, when it wrote to them first, or when they share a chat with it.

## Configuration

```yaml
gateways:
  pachca:
    enable: true
    token: ""                    # or PACHCA_BOT_TOKEN in the environment or ~/.coddy/.env
    proxy: ""                    # inherit (default), none, or an http(s)/socks5(h) proxy URL
    poll_interval_seconds: 2     # 1 to 60
    admins: [12345]              # Pachca user ids
    default_access: all          # all | admins | group:<name>
    default_isolation: individual  # individual | shared | admin
    user_groups:
      - name: devs
        user_ids: [12345, 67890]
    chats:
      - chat_id: 334
        isolation: shared
        access: group:devs
```

Access and isolation read exactly like the Telegram ones ([Access levels](gateway.md#access-levels), [Session isolation modes](gateway.md#session-isolation-modes)): `default_access` decides who may talk to the bot, `default_isolation` whether a group chat has one session per person, one for everyone, or one for the admins only, and `chats[]` overrides both per chat. A direct chat always has a session of its own. A thread has a chat id of its own in Pachca, so every thread is a conversation of its own as well.

Leave `token` empty and export `PACHCA_BOT_TOKEN` (for example in `~/.coddy/.env`) to keep the secret out of `config.yaml`. The same keys are on the **Gateways** tab of the web settings, and a token or any other key changed there rebuilds the Pachca bot in place, leaving a Telegram bot in the same process running. The field reference is in [config.md](../reference/config.md).

## Running it

The adapter is behind the `gateway` build tag (or `gateway.pachca` for this adapter alone); the release binaries, the packages and the Docker image carry it.

```bash
make build TAGS="gateway.pachca"
```

```bash
coddy serve --dry-run
```

`--dry-run` checks the token with `GET /oauth/token/info`: an error for a missing or revoked token or a missing required scope, a warning when only the recommended `webhooks:events:delete` or `users:read` is missing. Then start the server:

```bash
coddy serve
```

The banner names the bot (`gateway     pachca`), and the log says `pachca bot connected` with its nickname. `CODDY_PACHCA_API_BASE` points the bot and the dry run at another API base, a test server for example; the default is `https://api.pachca.com/api/shared/v1`.

## How the bot behaves

- **Direct chat**: every message is for the bot, with or without a mention or a reply.
- **Group chat or channel**: a group is where many people talk, so the bot answers only a message that mentions it (`@nickname`, or `<@id>`) or replies to one of its messages. A command needs the mention too, and so does a message in a thread opened under the bot's message. Everything else is left to the people in the chat.
- **Replies**: a person who replies to a message asks about it. The agent receives the replied-to message quoted in front of what the person wrote, the first line naming its author (`> Anna:`), in a direct chat and in a group alike, for the bot's own answer and for a colleague's message. A mention alone under a reply asks the agent to deal with the quoted message. The quote stays in the session's transcript; a settings command is never quoted. Naming the author needs the `users:read` scope; without it the quote has no author line.
- **Order and catch-up**: the bot reads the events history every `poll_interval_seconds`, newest first, down to the last event it handled, and hands the new ones over oldest first. The position is kept in `<sessions>/gateway_pachca_state.json`, so a restart answers what was written while the bot was down. The very first start begins after the newest event in the history and leaves older ones alone.
- **One turn at a time per session**: messages of one conversation queue up behind the running turn, and a stop or a restart waits up to 20 seconds for the running turn and the queued ones before the bot goes. A message that finds the queue full (32 waiting) is not taken, and the chat is told to send it again.
- **A passing failure loses nothing**: when Pachca does not answer a lookup the bot needs (whether a chat is a group, who wrote the message a reply answers), the event stays in the history and is read again on the next poll.
- The chat-to-session map lives in `<sessions>/gateway_pachca_sessions.json`, apart from the Telegram one.

## Commands

| Command | What it does |
|---------|--------------|
| `/help`, `/start` | The command list and a greeting. |
| `/clear` | Starts a new session for the conversation; the old one stays on disk. |
| `/model` | Posts the configured models as buttons; a click switches the conversation's model, and the next new conversation of the bot starts on it when an admin picked it (anybody else's pick stays in their own session). `/model <id>` sets it directly. |
| `/context` | The context window usage of the conversation's session. |
| `/agent`, `/plan`, `/ask`, `/think`, `/nothink`, `/reasoning <level>` | Settings commands, handed to the session like on every surface; `--once` and `--count=N` limit them to the next messages. |

In a group chat a command needs the bot's mention (`@nickname /clear`) or a reply to its message, and what changes the settings - the settings commands, `/model` and its buttons, `/clear` - is the admins' (`admins`): anybody else is answered *Only the bot's admins can change settings in this chat.* In a direct chat each person changes their own session. `/permissions` is not taken from a chat: the bot approves its own agent's requests itself.

## What somebody who is not an admin may do

The bot sees who wrote every message. Its admins (`admins`) can do everything; anybody else gets the conversation and nothing that changes the agent: only reading inside the session's working directory, the documentation, a web search, the plan and a question back; everything else - MCP tools included - is refused, `@` mentions stay inside the working directory, and every call that needs approval is asked about and refused, whatever the session's permission mode and its grants - the same rule as the Telegram bot. With an empty `admins` list everybody only chats, and `--dry-run` warns ([What somebody who is not an admin may do](gateway.md#what-somebody-who-is-not-an-admin-may-do)). A woken turn and a background subagent's request take the rights of the conversation's owner. Pachca has no web UI entry: the chat is the whole surface.

## Answers, limits and formatting

The answer is posted as one message at its first words and edited in place at most every two seconds while it is written, with the running tool's name under it; at the end the message is replaced by the full answer. Edits do not count against Pachca's limit of messages per chat. An answer longer than one message continues in further messages, and a piece Pachca refuses as too long is split again.

Pachca renders bold, italic, strikethrough, links, inline code and fenced code blocks, and shows a `#` heading as a bold line. It does not render lists or tables. The bot tells the model so for each turn, and on the way out turns a Markdown table into a fenced text block and puts a bar in front of a quote, whose `>` Pachca would drop.

A short rate limit (`429` with a pause up to a minute) is waited out. The daily limit of messages per chat is never retried, because every attempt during that pause doubles it; the answer is then not delivered and the log says so.

## Permissions and woken turns

An admin's own agent is allowed what it asks, like in Telegram: the operator configured the bot deliberately; for anybody else nothing is approved (above). A subagent whose definition narrows what it may do asks in the chat with **Allow** and **Reject** buttons, and only one of the bot's admins whose session in that chat is the one that asked can answer: in a group whose isolation is `shared` the session is everybody's, so a member who is not an admin cannot answer it. A background subagent asks the same way after the turn ended. A background task the agent started with `notify_on_finish` wakes the conversation when it ends, and the woken turn runs in the chat it belongs to: a note says what woke the agent, then the answer follows.

## What is not supported

The webhook intake (the bot only reads the events history), `/resume` and `/mcp`, pictures and files in either direction, reactions and forms. A person editing or deleting a message does not start a turn.

## Troubleshooting

- **The bot never answers and the log is quiet.** Check that **Save events history** is on: without it the history is always empty. The bot warns once in the log when the history has stayed empty for five minutes after the start.
- **The bot answers in a direct chat but not in a group.** Mention it or reply to its message; in a group even a command or a message in a thread under its answer needs one of the two. Check that the bot is a member of the chat and that `default_access` or the chat's override lets the person in.
- **`--dry-run` reports missing scopes.** Grant them on the bot's API tab in Pachca and copy the token again.
- **The history keeps growing.** The token lacks `webhooks:events:delete`; the bot still works on its saved position.
- **Debug output for this bot alone**: `logger.levels` with `component: gateway.pachca` and `level: debug`, or `--log-level "info,gateway.pachca=debug"`. Every ignored event is logged at debug with a reason.

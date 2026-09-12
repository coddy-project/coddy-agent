# A Telegram bot for a team

One `coddy serve` process polls a bot, answers in the chats you allow, and keeps every conversation as an ordinary session that the web UI on the same process can watch and continue. The full reference is [Telegram gateway](../surfaces/gateway.md); this recipe walks the setup for a team room.

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

3. **Describe the team in the config** ([Configuration reference](../surfaces/gateway.md#configuration-reference)).

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

4. **Check the file, then start in the background** ([coddy serve and the daemon](../operate/serve.md)).

   ```bash
   coddy serve --dry-run   # checks the token against the Bot API and names the bot
   coddy serve --daemon    # or -d
   coddy serve status
   ```

   `--daemon` detaches and leaves a dispatcher behind that restarts the worker whenever it dies for any reason other than `coddy serve stop`. The command waits for the first worker before it returns, so a configuration that cannot start is reported in the terminal that typed the command. The record lives in `~/.coddy/serve.json`, the log in `~/.coddy/logs/serve.log`; `coddy serve restart` brings it back with the arguments it was started with. Under `systemd`, Docker or another supervisor that already owns process lifetimes, run `coddy serve` in the foreground instead and let that supervisor restart it. Run one process per bot token: Telegram hands each update to one long poll, so a second poller steals messages from the first. A bot and nothing else is `--http=false` or `httpserver.enable: false`.

5. **Open the same chat in the browser** ([The same session in the chat and in the browser](../surfaces/gateway.md#the-same-session-in-the-chat-and-in-the-browser)). With `httpserver.enable` on, which is the default, the same process serves the web UI at `http://127.0.0.1:12345/`. Chat conversations are sessions with a `gw_` prefix: they are listed in the session rail, a turn the bot is answering streams into a tab watching it, and a reply typed in the browser is in the chat's history on the next message. Permission prompts stay with the chat, and the gateway answers them itself so the bot works unattended - so give the process a workspace you can afford to let it change (`coddy serve --cwd DIR`, else the directory it was started from). Only one turn runs per session; a message that arrives while a browser turn is in flight gets a busy notice instead of interleaving. To reach the UI from another machine, bind `httpserver.host: "0.0.0.0"` and set a token as in the next recipe.

6. **Use it from the chat** ([Bot interaction model](../surfaces/gateway.md#group-chats)). In a private chat every message is answered. In a group the bot responds only when it is @mentioned, when a message replies to one of its own, or to `/clear`; under `admin` isolation it also ignores everyone outside `admins`. `/mode` and `/model` open inline keyboards for the session's mode and model, `/context` shows what fills the context window, and `/clear` starts a fresh session for that user or chat - the old bundle stays on disk and `coddy sessions list` still shows it. A changed token rebuilds the gateway in place and a flipped `enable` starts or stops it without a restart; when a tap seems to do nothing, raise one component: `coddy serve --log-level "info,gateway.telegram=debug"` ([Debugging a chat](../surfaces/gateway.md#debugging-a-chat)).

---
description: Messenger gateway architecture, session store, and Telegram adapter conventions
paths:
  - "external/gateway/**/*.go"
  - "internal/config/gateway.go"
---

# Messenger Gateway (`external/gateway`)

Built with **`-tags gateway.telegram`** (Telegram only) or **`-tags gateway`** (all adapters). Without these tags `coddy serve` is present in the binary but returns a "not compiled" error.

## Package layout

| Package | Role |
|---------|------|
| `external/gateway` | `Adapter` interface, `Hub`, `IncomingMessage`, `OutgoingMessage` |
| `external/gateway/access` | `CanAccess`, `EffectiveAccess`, `EffectiveIsolation` — ACL helpers |
| `external/gateway/sessionstore` | `Store`: maps stable chat/user keys to Coddy session IDs (`Get` mints, `Reset` replaces for `/clear`, `Bind` points a chat at an existing session for `/resume`); persisted to `gateway_sessions.json` |
| `external/gateway/proxyutil` | `BuildHTTPClient` — HTTP/SOCKS5 proxy support for outbound adapter requests |
| `external/gateway/telegram` | `Bot` (polling, dispatch, ACL), `Sender` (streaming output), `commands.go` (the inline keyboard for `/model`, the callback dispatcher, `callbackValue` for payloads over 64 bytes), `resume.go` (`/resume`: the session picker over `HandleSessionList`, the query matcher, the `resume:s:` / `resume:p:` callbacks), `isSettingsCommand` (`/model <id>`, `/think`, `/nothink`, `/reasoning`, `/agent`, `/plan`, `/ask` with `--once` / `--count=N` go to the session as a message and the manager takes them; `/permissions` does not, the bot approves its chat agent itself), `prompt.go` (what the model is told about answering here), `markdown.go` (md → Telegram format) |
| `internal/tgfake` (untagged) | The fake Bot API: every method the adapter calls, long-polled `getUpdates`, the `/sim/*` API and the chat page, `llmstub` for a scripted model. `cmd/tgfake` serves it; the adapter's polling feature runs it on httptest |

## Session store

`sessionstore.NewPersisted(path)` loads/saves a JSON map of key→session-ID on every mutation. The file lives at `$CODDY_HOME/sessions/gateway_sessions.json` (set in `external/gateway/start.go`). On restart the bot reloads the map so existing conversations continue where they left off. `/resume` writes the same map through `Bind`, so a chat moved to another session stays there across a restart; the session it left is not forgotten, because `/resume` is a switch the chat may reverse a moment later, while `/clear` ends a conversation and `ForgetLiveSession` belongs to it. A reserved `$last_model` entry holds the gateway's own last model pick: a fresh session (no transcript, no saved pick) starts on it — or on the alphabetically first model when nobody picked yet — via `applyInitialModel` in `commands.go`, called from `processMessage` and `ensureSession`.

`newID()` mints ids through `session.NewSessionID()`: a chat conversation is an ordinary Coddy session with an ordinary `sess_` id, so `GET /coddy/sessions` lists it beside the sessions started in a terminal or a browser.

## SessionRunner interface

Adapters call the session manager through `SessionRunner` (defined in `external/gateway/telegram/bot.go`). `session.Manager` satisfies this interface directly:

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

`/resume` resolves its choice against `HandleSessionList` and never hands the manager an id that listing did not return: `EnsureHTTPSession` creates a session for an unknown id, and a typo must not become an empty bundle.

## Telegram Sender streaming

`Sender` (`external/gateway/telegram/sender.go`) implements `acp.UpdateSender`:

- First text token → sends a new Telegram message and saves the message ID.
- Subsequent tokens → `editMessageText`, throttled to ~1.5 s to avoid Telegram rate limits.
- Tool execution in progress → replaces live message with "⚙️ toolname…" indicator; this line is NOT included in the final `Flush()` output.
- `Flush()` → replaces the live message with the final formatted text, converted to Telegram legacy Markdown via `markdown.go`.
- `RequestPermission` auto-approves the chat agent's own requests (the admin configured the bot deliberately). A subagent's request stamped below `bypass` is asked in the chat instead (`permission.go`: inline **Allow** / **Reject** buttons, `perm:<token>:<i>` callbacks answered only by the person whose session asked); the `Bot` is also a `DetachedPermissionBroker` offered to `serve.Runtime` (`gateway.Options.Prompts`), so a background subagent of a chat session asks there after the turn ended.

## Woken turns

A chat conversation is an ordinary session, so a background task its agent started with `notify_on_finish` wakes it when it ends. The `Bot` is an `agent.WakeSurface` of rank `WakeOwner`: `Start` offers it to `gateway.Options.Wakes` (`serve.Runtime` under `coddy serve`, which owns the process waker) once connected and withdraws it on stop. `RunBackgroundWake` (`wake.go`) takes a wake only for a session one of its chats is bound to (`store.KeyFor`, `sessionstore.ChatID`) and only while connected, and then runs the turn exactly like `processMessage`: the chat's own sender, the turn mirror, the surface prompt, `wake.RunOpts()` for the marker. The chat receives the note first - `Sender.SendSessionUpdate` turns `acp.BackgroundWakeUpdate` into a plain-text message, `🔔` plus `session.BackgroundWakeNote` - then the answer. A busy session returns `session.ErrSessionTurnBusy` untouched and says nothing in the chat: the waker asks again. Happy path: `features/gateway_telegram_wake.feature` (real manager, agent, pool and waker; `llmstub` with a tool rule; the fake Bot API).

## Answering through a surface

`prompt.go` holds what this adapter tells the model about answering into a Telegram chat: the legacy subset when `rich_messages` is off, and a shorter note about phone-sized answers when it is on. The bot passes it per turn as `session.PromptRunOpts.SurfaceSystemPrompt`; `internal/session` holds it on the state for the length of the turn (`SetSurfaceSystemPrompt`, cleared before the turn lock is released, never persisted) and `internal/agent` appends it to the system prompt after the hook context (`Agent.surfaceBlock`). A turn from another surface on the same session carries a different prefix and loses the cached one - the accepted cost of keeping messenger quirks out of the core.

## Markdown conversion

`markdown.go` is the gateway's **outbound rendering step**: the safety net under the prompt block above, because a model does not always comply. The transcript carries none of it, so a chat conversation is an ordinary session, and the next integration adds its own `prompt.go` plus its own renderer.

`convertMarkdown` does the work behind two entry points. `mdToTelegram` renders for `ParseMode="Markdown"` (ATX headings and `**text**` → `*text*`, `__x__` → `_x_`, bullet `* item` → `• item`, tables flattened, horizontal rules → a separator) and `mdToPlainPreview` renders for the live streaming message, which is sent with **no** parse mode and therefore drops the emphasis markers rather than showing them as punctuation. Fenced code blocks are set aside **before** any rule runs and restored afterwards, so a `**p` or a `# comment` inside a block is never rewritten; an unclosed fence (a truncated answer) is taken to run to the end. Capture groups are written `${1}`, not `$1`: an underscore is a word character, so `"_$1_"` names a group called `1_` and expands to nothing.

## Rich Messages (Bot API 10.1)

Enabled per-bot with `gateways.telegram.rich_messages: true` (`config.TelegramGatewayConfig.RichMessages`). When on, the agent's native Markdown is sent verbatim instead of being downgraded by `markdown.go`.

- `richmsg.go` (pure, table-tested): `buildRichMarkdown` (final message = tool `<details>` blocks first, each with its output via the `toolCall` type, then the answer verbatim) and `buildRichDraftMarkdown` (streaming preview + draft-only `<tg-thinking>` block while a tool runs). Rich mode has nothing to downgrade, so the answer goes out verbatim. The Sender captures tool args/results from `acp.ToolCallStatusUpdate`. `flushRich` retries with the answer alone if the combined message (answer + tool blocks) is rejected, so the reply is never lost.
- `richclient.go`: `inputRichMessage` type, `richParams`/`richDraftParams` builders, and `sendRichMessage`/`sendRichMessageDraft` issued via `bot.MakeRequest` (the `go-telegram-bot-api/v5` library has no native methods). `InputRichMessage` takes a `markdown` string — no hand-built `RichBlock` JSON tree.
- `Sender` carries a `richConfig{enabled, allowDraft, draftID}`. `allowDraft` is true only in private chats (drafts are private-only). `Flush()` finalizes via `sendRichMessage`; on error it falls back to the legacy formatted send so the bot never goes silent.
- No `editRichMessage` exists; drafts are ephemeral 30 s previews and need no deletion. `<tg-thinking>` (RichBlockThinking) may be used only in drafts.

## Logging

The adapter's logger arrives tagged with the `gateway.telegram` component (`internal/logger.Component`, applied in `external/gateway/start.go`; the hub itself is `gateway`), so `logger.levels` can raise one bot to `debug` while the rest of the process stays at `info`. Tag once, at construction - `Component` on an already-tagged logger prints two `component` attributes.

The whole command path logs at `debug`: `telegram: update` per arriving message or callback, `telegram: update ignored`/`update rejected` with a `reason` for every silent drop, `telegram: command`, the `model`/`context`/`resume` menus with the session they belong to (`telegram: resume query` for the words after `/resume` and how many sessions matched), and `telegram: callback` with the resolved value. A switch that lands is `info` (`telegram: model applied`, `telegram: session resumed`), matching `telegram: session cleared`; a failure is `warn`. Nothing in the adapter may log through `slog.Default` - a record that skips `b.log` misses the configured sink and carries no component. Operator guide: `docs/surfaces/gateway.md` (Debugging a chat).

Inline-keyboard payloads must survive the round trip. `callback_data` is capped at 64 bytes, so `callbackValue` sends a model id or a session id verbatim when it fits next to its prefix and a digest when it does not (never a truncated id, which resolves to nothing), and `resolveModelCallback` / `resolveResumeCallback` map the payload back against the configured models or the current session listing. The keyboard also outlives the process that sent it, so `handleCallback` calls `ensureSession` before configuring anything: after a restart the session is on disk, and the manager only configures live ones. A resume tap is dispatched before that call: it names the session the chat moves to, and loading the chat's current one first would mint a session for a chat that never spoke.

## Proxy

`proxyutil.BuildHTTPClient(setting)` reads `gateways.telegram.proxy` with `config.ParseProxySetting`, the parser `providers[].proxy` uses: an empty value or `inherit` returns `http.DefaultClient` unchanged, which follows `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY` of the process (never a direct connection, whatever an old description said); `none` returns a client with no proxy function; an http, https, socks5 or socks5h URL routes through that proxy (x/net/proxy handles socks5 and socks5h alike: the proxy resolves host names). The Telegram adapter passes `cfg.Proxy` to this function in `Start()`, and the `--dry-run` getMe probe builds the same route with `llm.HTTPClientForOptionalProxy`. The route against real proxy variables is tested in a child process (`TestBuildHTTPClientFollowsTheProcessEnvironment`): net/http reads them once per process and never proxies loopback, so a scenario on the fake Bot API cannot tell `none` from an unset value.

## Bot API origin

`Start()` reads `config.TelegramAPIBaseEnv` (`CODDY_TELEGRAM_API_BASE`, the variable the `--dry-run` probe honours too) and builds the library's endpoint template with `telegramAPIEndpoint` (`<origin>/bot%s/%s`; empty means api.telegram.org). It logs `telegram: api base override` at `info` when set. Tests set the unexported `Bot.apiBase` instead of the environment. `internal/tgfake` is the stand-in server that origin points at, in `go run ./cmd/tgfake` and in the polling feature; it is untagged and imports no Telegram library, so it must stay free of `tgbotapi` types. Operator guide: `docs/surfaces/gateway.md` (Debugging against a fake Bot API).

The poll names its `allowed_updates` (`subscribedUpdates`: `message`, `callback_query`) on every request. Telegram remembers the last subscription a bot asked for, and the library sends none by default, which inherits whatever a previous process left - a token once run under another framework with messages only drops every keyboard tap server-side (found on a real bot). Extend that list when the adapter starts handling another update kind; `tgfake` models the memory (`Options.AllowedUpdates`, `SetAllowedUpdates`) and the polling feature starts under a stale subscription.

## Tests

Every test in `external/gateway/telegram` that needs Telegram reaches it through `fakeapi_test.go`: `newFakeAPI(t, opts)` (or `openFakeAPI` for a godog world) serves `internal/tgfake` on httptest and hands back a `tgbotapi` client pointed at it; `userMessage` and `tap` put the person's side into the fake's chat, so the message a handler replies to and the keyboard a tap presses are ones the server knows. Assert on `fake.Calls(method)` (what the bot posted) and `fake.Chat(id)` (what the chat ends up holding). Do not hand-roll an `http.HandlerFunc` for the Bot API: a canned answer accepts what Telegram refuses (an edit of a message never sent, 65 bytes of `callback_data`, a 4097-character text, a reply to a message the chat does not hold, an answer to a callback query it never issued). When a test needs Telegram to behave in a new way - a new method, a new refusal - extend `internal/tgfake` with its own unit test; `Fault` (`Times`, `Contains`) covers refusals by method and by payload. The godog harnesses call `processMessage` / `handleCallback` directly to stay synchronous; only `bdd_polling_test.go` runs `Bot.Start`.

## Adding a new adapter

1. Create `external/gateway/<name>/bot.go` with `//go:build gateway || gateway.<name>`.
2. Implement `gateway.Adapter` (`Name() string`, `Start(ctx) error`).
3. Implement `acp.UpdateSender` (see `telegram/sender.go`).
4. Add `sessionstore.NewPersisted(storePath)` for key→session-ID persistence.
5. In `external/gateway/start.go`, append your adapter when its config is enabled.
6. Update the build constraint on `start.go` / `start_stub.go` to include the new tag.

## References

@docs/surfaces/gateway.md
@architecture.md
@internal/config/gateway.go

---
description: Messenger gateway architecture, session store, and Telegram adapter conventions
paths:
  - "external/gateway/**/*.go"
  - "internal/config/gateway.go"
---

# Messenger Gateway (`external/gateway`)

Built with **`-tags gateway.telegram`** (Telegram only) or **`-tags gateway`** (all adapters). Without these tags `coddy gateway` is present in the binary but returns a "not compiled" error.

## Package layout

| Package | Role |
|---------|------|
| `external/gateway` | `Adapter` interface, `Hub`, `IncomingMessage`, `OutgoingMessage` |
| `external/gateway/access` | `CanAccess`, `EffectiveAccess`, `EffectiveIsolation` — ACL helpers |
| `external/gateway/sessionstore` | `Store`: maps stable chat/user keys to Coddy session IDs; persisted to `gateway_sessions.json` |
| `external/gateway/proxyutil` | `BuildHTTPClient` — HTTP/SOCKS5 proxy support for outbound adapter requests |
| `external/gateway/telegram` | `Bot` (polling, dispatch, ACL), `Sender` (streaming output), `commands.go` (inline keyboards), `markdown.go` (md → Telegram format) |

## Session store

`sessionstore.NewPersisted(path)` loads/saves a JSON map of key→session-ID on every mutation. The file lives at `$CODDY_HOME/sessions/gateway_sessions.json` (set in `external/gateway/start.go`). On restart the bot reloads the map so existing conversations continue without re-sending the one-time initialization hint.

`KnownIDs()` is called at startup to pre-populate the `seenSessions` set (prevents re-injecting the Telegram formatting hint into existing sessions after a restart).

## SessionRunner interface

Adapters call the session manager through `SessionRunner` (defined in `external/gateway/telegram/bot.go`). `session.Manager` satisfies this interface directly:

```go
type SessionRunner interface {
    EnsureHTTPSession(ctx context.Context, sessionID string, defaultCWD string) (*session.State, error)
    HandleSessionPromptWithSender(ctx context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error)
    ForgetLiveSession(sessionID string)
    HandleSessionSetMode(ctx context.Context, params acp.SessionSetModeParams) error
    HandleSessionSetConfigOption(ctx context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error)
    Cfg() *config.Config
}
```

## Telegram Sender streaming

`Sender` (`external/gateway/telegram/sender.go`) implements `acp.UpdateSender`:

- First text token → sends a new Telegram message and saves the message ID.
- Subsequent tokens → `editMessageText`, throttled to ~1.5 s to avoid Telegram rate limits.
- Tool execution in progress → replaces live message with "⚙️ toolname…" indicator; this line is NOT included in the final `Flush()` output.
- `Flush()` → replaces the live message with the final formatted text, converted to Telegram legacy Markdown via `markdown.go`.
- `RequestPermission` auto-approves (the admin configured the bot deliberately).

## Markdown conversion

`markdown.go` converts standard Markdown to Telegram legacy format: ATX headers → `*bold*`, `**text**` → `*text*`, bullet `* item` → `• item`, tables → plain text, horizontal rules → separator. Fenced code blocks are preserved verbatim.

## Rich Messages (Bot API 10.1)

Enabled per-bot with `gateways.telegram.rich_messages: true` (`config.TelegramGatewayConfig.RichMessages`). When on, the agent's native Markdown is sent verbatim instead of being downgraded by `markdown.go`.

- `richmsg.go` (pure, table-tested): `buildRichMarkdown` (final message = tool `<details>` blocks first, each with its output via the `toolCall` type, then the answer verbatim) and `buildRichDraftMarkdown` (streaming preview + draft-only `<tg-thinking>` block while a tool runs). Rich mode prepends **no** formatting hint (unlike legacy `telegramFormattingHint`), so the first turn is identical to later ones — a hint-prefixed first message was suppressing replies. The Sender captures tool args/results from `acp.ToolCallStatusUpdate`. `flushRich` retries with the answer alone if the combined message (answer + tool blocks) is rejected, so the reply is never lost.
- `richclient.go`: `inputRichMessage` type, `richParams`/`richDraftParams` builders, and `sendRichMessage`/`sendRichMessageDraft` issued via `bot.MakeRequest` (the `go-telegram-bot-api/v5` library has no native methods). `InputRichMessage` takes a `markdown` string — no hand-built `RichBlock` JSON tree.
- `Sender` carries a `richConfig{enabled, allowDraft, draftID}`. `allowDraft` is true only in private chats (drafts are private-only). `Flush()` finalizes via `sendRichMessage`; on error it falls back to the legacy formatted send so the bot never goes silent.
- No `editRichMessage` exists; drafts are ephemeral 30 s previews and need no deletion. `<tg-thinking>` (RichBlockThinking) may be used only in drafts.

## Logging

The adapter's logger arrives tagged with the `gateway.telegram` component (`internal/logger.Component`, applied in `external/gateway/start.go`; the hub itself is `gateway`), so `logger.levels` can raise one bot to `debug` while the rest of the process stays at `info`. Tag once, at construction - `Component` on an already-tagged logger prints two `component` attributes.

The whole command path logs at `debug`: `telegram: update` per arriving message or callback, `telegram: update ignored`/`update rejected` with a `reason` for every silent drop, `telegram: command`, the `mode`/`model`/`context` menus with the session they belong to, and `telegram: callback` with the resolved value. A switch that lands is `info` (`telegram: model applied`, `telegram: mode applied`), matching `telegram: session cleared`; a failure is `warn`. Nothing in the adapter may log through `slog.Default` - a record that skips `b.log` misses the configured sink and carries no component. Operator guide: `docs/gateway.md` (Debugging a chat).

Inline-keyboard payloads must survive the round trip. `callback_data` is capped at 64 bytes, so `modelCallbackValue` sends a model id verbatim when it fits and a digest when it does not (never a truncated id, which resolves to nothing), and `resolveModelCallback` maps the payload back against the configured models. The keyboard also outlives the process that sent it, so `handleCallback` calls `ensureSession` before configuring anything: after a restart the session is on disk, and the manager only configures live ones.

## Proxy

`proxyutil.BuildHTTPClient(url)` handles http, https, socks5, socks5h. An empty string returns `http.DefaultClient` unchanged. The Telegram adapter passes `cfg.Proxy` to this function in `Start()`.

## Adding a new adapter

1. Create `external/gateway/<name>/bot.go` with `//go:build gateway || gateway.<name>`.
2. Implement `gateway.Adapter` (`Name() string`, `Start(ctx) error`).
3. Implement `acp.UpdateSender` (see `telegram/sender.go`).
4. Add `sessionstore.NewPersisted(storePath)` for key→session-ID persistence.
5. In `external/gateway/start.go`, append your adapter when its config is enabled.
6. Update the build constraint on `start.go` / `start_stub.go` to include the new tag.

## References

@docs/gateway.md
@architecture.md
@internal/config/gateway.go

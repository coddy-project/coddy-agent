# Plan: the web UI as a Telegram Mini App (issue #320, PR #373)

Status: design record written 2026-10-04 on `codex/telegram-mini-app` (PR #373)
after merging `main` (8b6f68a9); cross-review round 1 approved it with changes,
which sections 4 and 5 now carry (section 9). Deviations found while
implementing go to section 10.

## 1. What the issue asks

Issue #320: the web UI opened inside the Telegram mobile app as a Mini App

1. does not know it is in Telegram;
2. does not fit the Mini App window: the content overflows and stretches;
3. opens the mode and model menus at the bottom of the screen, where they
   cannot be used.

Acceptance: the environment is detected at load; on a phone the layout fits
the window with no horizontal scroll or overflow; the mode and model menus
open in a usable place; both themes work; ordinary mobile and desktop
browsers behave as before.

The operator asked for more than the issue: what else the change should do,
which configuration it needs, how `tgfake` can emulate a Mini App for testing,
and how to keep everything Telegram-specific in the Telegram package so that a
binary built without the gateway tag loses nothing and breaks nothing.

## 2. What PR #373 does

Three commits by hijera (the change, a merge of `main`, a fix that keeps the
keyboard inset on the docked composer), merged with `main` again here:

- `index.html` stores `location.search + location.hash` in
  `window.__coddyTelegramLaunchURL`, then loads
  `https://telegram.org/js/telegram-web-app.js?63` synchronously in `<head>`;
- `ui/telegramMiniApp.ts` (`initTelegramMiniApp`, called from `main.tsx`)
  decides Mini App mode from a non-empty `Telegram.WebApp.initData` or a
  non-empty `tgWebAppData` / `tgWebAppVersion` / `tgWebAppStartParam` in the
  launch URL, sets `data-telegram-mini-app="true"` on `<html>`, writes
  `--coddy-telegram-viewport-height`, `--coddy-telegram-stable-height` and four
  `--coddy-telegram-safe-*` variables, follows `viewportChanged`,
  `safeAreaChanged`, `contentSafeAreaChanged` and window/visual-viewport
  resizes, and calls `ready()`;
- one `@media (max-width: 1199px)` block in `styles.css`: `overflow-x: clip` on
  `html`, the shell takes the visible height, the top bar and the docked
  composer respect the safe area, the docked composer is lifted by
  `max(--coddy-keyboard-inset, 100dvh - stable height)`, and every
  `.mode-menu--sheet` (mode, model, reasoning, permission, environment, folder,
  branch) becomes a panel centred in the visible height;
- six vitest cases, a paragraph in `docs/surfaces/web-ui.md` and in
  `DESIGN.md`, and a 390px screenshot of the centred mode menu.

## 3. How far it fixes the issue

| Criterion | State |
| --- | --- |
| Detected at load | Yes, from launch markers or `initData`. |
| Fits the window | Partly: the composer and the shell follow the visible height; the slash and `@` sheet, the context sheet and the dialogs do not, and the transcript's last lines stay under the lifted composer of a half-open app. |
| Mode and model menus usable | Yes, centred in the visible height. |
| Both themes | Yes for Coddy's themes; Telegram's own colours are ignored. |
| Ordinary browsers unchanged | **No**: every browser now loads a script from `telegram.org` before the page can render. |

Found by reading the change against the Telegram documentation
(`core.telegram.org/bots/webapps`, `core.telegram.org/api/web-events`), the
SDK it loads, and the stacked shell's CSS:

- **P1. A third-party script for every visitor.** The script is parser-blocking
  in `<head>`, so where `telegram.org` is slow or blackholed (some corporate
  networks, some countries, an air-gapped LAN) the page stays blank until the
  connection times out - in an ordinary browser that never sees Telegram. Every
  page load reports the visitor to `telegram.org`; the SDK logs its
  `[Telegram.WebView] > postEvent` lines to the console in every browser, and
  inside any iframe it posts `iframe_ready` to the parent with target `*` and
  reloads itself when the parent says `reload_iframe`. The file behind `?63`
  can change under a released binary. Inside Telegram the WebView reaches
  `telegram.org` directly, not through the MTProto proxy the app may use, so
  where only the app gets through the Mini App does not start.
- **P2. `overflow-x: clip` on `html`.** The stacked shell's own comment
  forbids it: a non-visible overflow on `html` stops `body`'s `overflow-y`
  from passing to the viewport, `body` becomes a scroll container, and the
  sticky chat title stops sticking. Nothing needs the clip: since #340 nothing
  in the shell is wider than the page.
- **P3. The top inset.** The override of `--coddy-mobile-top-inset` sits on
  `.shell`, drops the environment banner's height that the `:root` value adds,
  and does not reach what is portalled into `<body>` (the folder browser) or
  the banner itself (`--coddy-mobile-bar-h`). Safe areas are combined with
  `max`, while Telegram's content safe area is measured inside the device's
  (fullscreen: status bar plus Telegram's buttons), so the inset is their sum.
- **P4. The window is never configured.** No `expand()`: a Mini App opened
  from a button may start at half height, the state the issue was filed from.
  Vertical swipes stay on, so pulling the transcript down at its top collapses
  or closes the app. No BackButton: Android's back key closes the whole Mini
  App even while a sheet or Settings is open. Telegram's header keeps
  Telegram's colour over Coddy's background.
- **P5. Centred panels instead of sheets, and only some of them.**
  `DESIGN.md` makes every menu and picker of the stacked shell a bottom sheet;
  the change turns the composer's menus into centred dialogs and leaves the
  slash and `@` sheet (`.slash-menu--sheet`, its bottom set inline), the
  context sheet and the confirmation dialog at the bottom or centre of the CSS
  viewport. The cause was a sheet pinned below the visible part of a half-open
  app; lifting it by the hidden part, the rule the composer follows, fixes the
  cause and keeps the design. The transcript's tail spacer
  (`--chat-composer-reserve`) does not know about that lift either.
- **P6. The launch parameters stay in the URL.** The router ignores them, but
  the Docs reader and the environment switch keep the raw fragment, and
  `coddy_env_routes` in `localStorage` can end up holding the signed
  `tgWebAppData`.
- **P7. Nothing on the bot side.** An operator wires the Mini App by hand in
  @BotFather, and a chat has no way to open its own session in the web UI,
  although the session is an ordinary one the web UI already shows live.
- **P8. Telegram Web cannot sign in.** web.telegram.org runs a Mini App in a
  cross-site iframe. The sign-in cookie is `SameSite=Strict`
  (`external/httpserver/auth_login.go`), so it never travels there: the form
  accepts the password and the next request is a 401. Nothing says why.
- **P9. Tests and evidence.** No happy-path feature, no browser check, and
  `tgfake` cannot open a Mini App (a `web_app` button is refused with
  `BUTTON_TYPE_INVALID`, `setChatMenuButton` answers 404, the token is not
  kept, so `initData` cannot be signed). The screenshot is an emulation in
  Russian, while the documentation is captured in English.
- **P10. Documentation.** Nothing tells an operator how to make the web UI a
  Mini App: HTTPS, sign-in, the menu button.

Side findings in the same area, fixed in this change: `docs/surfaces/gateway.md`
and the gateway rule name `external/gateway/start.go` / `start_stub.go`, which
are `serve_gateway.go` / `serve_stub.go`; `cmd/coddy/serve.go`
`gatewayFingerprint` lists the Telegram fields by hand, so a field added to the
config would not rebuild the bot on a reload; the `/resume` and subagent
permission harnesses answer the Bot API with a hand-written handler instead of
`tgfake`, against the gateway rule.

## 4. Decisions

### D1. No script from telegram.org: a bridge of our own

The Mini App protocol is small and documented (`core.telegram.org/api/web-events`):
a Mini App posts events through `window.TelegramWebviewProxy.postEvent(type,
json)` in the mobile and desktop apps, `window.external.notify(json)` in the
oldest Windows client, or `window.parent.postMessage(json, '*')` from the
iframe of a web client; the client answers by calling
`window.Telegram.WebView.receiveEvent(type, data)` (older builds:
`window.TelegramGameProxy.receiveEvent`, `TelegramGameProxy_receiveEvent`) or,
in an iframe, with a `message` from the parent. Coddy needs a dozen of those
events, so `ui/telegram/bridge.ts` implements exactly that: one `postEvent`,
one `on(type, handler)`, and the receivers, installed only once a launch is
detected. In a frame it posts `iframe_ready` and honours `reload_iframe` as the
SDK does, and it hears a message only from `window.parent`, only as the JSON
envelope `{eventType, eventData}` with a non-empty `eventType`; everything
else, `set_custom_style` included (it would inject the parent's CSS), is
ignored. Ordinary browsers run none of it and load nothing new; `index.html`
goes back to what `main` has. Version gates follow the Bot API version the
launch names (`tgWebAppVersion`): the back button and the background colour
from 6.1, the header colour by value from 6.9, the swipe behaviour from 7.7,
the bottom bar colour from 7.10; the safe areas exist from 8.0, and asking an
older client for them is harmless, as the SDK does unconditionally.

### D2. Launch parameters: read once, kept for the tab, removed from the URL

`ui/telegram/launch.ts` reads the parameters Telegram puts into the fragment
(`#tgWebAppData=...&tgWebAppVersion=...`, or after a fragment the button URL
already had, `#<route>?tgWebAppData=...`, the form the SDK's parser accepts)
and the query (`tgWebAppStartParam`, and the fallbacks of older clients). A
launch is a non-empty `tgWebAppVersion` or `tgWebAppData`; a start parameter
alone is not, since anyone can type one into an address. `ui/telegram/capture.ts`
is the first import of `main.tsx` and runs it at import, before anything else
can read or rewrite the fragment. The launch is kept in `sessionStorage`
(`coddy.telegram.launch`), so a reload inside the Mini App - pull to refresh,
`reload_iframe`, the SPA's own reloads - is still a Mini App; every access to
the storage is guarded, and a browser that refuses it keeps the launch for the
page only. The URL goes back to the SPA's route alone with
`history.replaceState` before the router reads it (P6). `initData` stays in
that one place for a server that may validate it one day; the SPA never
treats it as an identity.

### D3. The Mini App window

At start, in this order: the theme (D5) and the CSS variables from what the
launch says; `web_app_request_viewport`, `_safe_area`, `_content_safe_area`,
`_theme`; `web_app_expand` (a chat needs the height, and the half-open state is
where the issue came from); vertical swipes off (`web_app_setup_swipe_behavior`,
so pulling the transcript does not fold the app; the header still does); the
header, background and bottom bar colours from the theme's canvas tokens
(`--coddy-canvas-gradient-top` for the header, `--coddy-canvas-gradient-bottom`
for the other two, solid `#rrggbb` in every theme), sent again whenever
`data-theme` changes; `web_app_ready` last, so Telegram drops its placeholder
over a page that already has its colours. Events: `viewport_changed` sets the
visible height and, when `is_state_stable`, the stable height;
`safe_area_changed` and `content_safe_area_changed` set the insets, top and
bottom being the sum of the two (P3); `theme_changed` goes to D5. Until the
first `viewport_changed` the heights come from `visualViewport` and
`innerHeight`, as in the PR.

**BackButton.** Telegram's back button stands for Escape, the key that already
"undoes one step" everywhere (`nav/railEscape.ts`). It is shown while a
conversation is open, a screen of the rail is open, or a modal layer is in the
DOM (the scrims and dialogs the module knows by class; a layer it misses only
leaves Telegram's Close in place, which is what happens today). Pressing it
dispatches a cancelable Escape at the focused element, and every layer that
answers the key answers the button through the handler it already has - every
layer but the question card. For the card an unclaimed Escape means "skip the
question", which sends the agent an answer; a navigation button sends none,
and the question is shown again when the conversation is opened again. So a
listener on `document` in the bubble phase, added just before the synchronous
dispatch (it runs after the rail's) and removed in `finally` right after it,
sees whether anything claimed the key; if nothing did, it claims it
(`preventDefault` and `stopPropagation`), the card on `window` never sees it,
and the module leaves the conversation for the start screen through the
router's own `setSessionHashInLocation("")`. A dialog that stops the key on
`window` in the capture phase (`ConfirmDialog`, `stopImmediatePropagation`)
answers the press, and the listener is gone before the next Escape a keyboard
types. With the button hidden Telegram shows Close, and Android's back key
closes the app from the start screen with nothing open.

### D4. Sheets stay bottom sheets; what a half-open app lifts

`DESIGN.md` makes every menu and picker of the stacked shell a bottom sheet. In
a Mini App the sheet keeps that shape and is lifted by the part of the WebView
Telegram does not show, `--coddy-telegram-hidden-bottom` = layout height minus
stable height (never below zero), or by the keyboard inset, whichever is more
(`--coddy-telegram-lift`), and capped to the visible height under the top bar.
Lifted that way: `.mode-menu--sheet`, and on the start screen
`.slash-menu--sheet` and `.context-breakdown-menu--sheet`. Over the docked
composer both pickers are placed from the composer's card, which is lifted
already; on the start screen they sit at the bottom by their class, which is
what the lift moves. The context sheet set an inline `bottom: 0` there, which
no stylesheet can move, so `ContextBreakdownPopover.tsx` sets an inline bottom
only over the docked composer, as the slash picker always has. The
confirmation dialog and the image viewer end their backdrops at the lift, so
what they centre is visible.

The docked composer keeps its `bottom` on the keyboard inset (`styles.css`) and
grows its padding by what Telegram hides beyond the keyboard. Its height is the
transcript's reserve (`ChatScreen`), so the transcript's tail grows with it and
a transcript parked at its newest message follows, through the code that
already keeps it there when the usage banner arrives; nothing new in
`ChatScreen`. The WebView keeps its size when Telegram shows less of it, so no
`resize` fires: after a change of the hidden part the module dispatches one,
and what measures itself against the window (the sheets over the docked
composer) measures again. Drawers and docks (History, Settings, Scheduler,
Tasks, Docs, Swarm) keep their bottom: their head and close button are at the
top, in sight, and with `expand()` and swipes off a half-open app is one the
user pulled down on purpose.

The `overflow-x: clip` on `html` goes (P2). The top inset is set where the
stacked shell sets it, on `:root`, by overriding `--coddy-mobile-bar-h`, so
the banner term, the portalled dialogs and the banner itself follow (P3).

### D5. The theme follows Telegram until the user picks one

With no `coddy_ui_theme` cookie, a Mini App starts on `light` or `dark` by the
brightness of Telegram's `bg_color` (the SDK's rule) and follows
`theme_changed`, reading the cookie again on every change: a pick in Settings
→ Appearance writes it and wins from then on, in the same session, in
Telegram and outside it. The module applies the theme with `applyUiTheme`,
after `bootstrapUiThemeFromCookie` in `main.tsx` (before it, the bootstrap
would overwrite it), and writes nothing. The picker follows `data-theme`
already. The theme is applied synchronously before `load` and before
`web_app_ready`, so Telegram's placeholder covers the switch.

### D6. Opening a chat's own conversation

A conversation in a chat is an ordinary session, already live in the web UI.
The bot's link to it carries the session in the query, `?session=<id>`,
because the fragment belongs to Telegram's launch parameters; the launch module
turns it into the SPA's session route (`#/s/<id>`, encoded by the router's own
helper) and drops it from the URL. It is read in any browser, since a group
gets the link as an ordinary button that opens the browser; only the alphabet
of a session id is taken (letters, digits, `_` and `-`, up to 256, as the
server's `ValidateSessionID`), and anything else opens the start screen.
Inside a launch, a start parameter that is a session id
(`t.me/<bot>?startapp=sess_...`, for a bot with a main Mini App in
@BotFather) is read the same way.

### D7. Configuration

```yaml
gateways:
  telegram:
    mini_app:
      url: https://coddy.example.com/   # the public https address of this coddy serve
      menu_button: true                  # the bot's menu button opens the web UI (default)
```

- `url` set with `menu_button` on takes the bot's menu button on every start
  (D8). `url` empty (the default) or `menu_button: false` leaves the button to
  @BotFather; the bot touches it then only to put back a button it set itself.
- `url` must be absolute and `https`, or `http` on a loopback host (the offline
  stand; Telegram itself refuses `http`), with no fragment (the fragment is
  Telegram's) and no user info. It is checked only while the gateway is
  enabled, like the rest of the block, and the error names
  `gateways.telegram.mini_app.url`.
- `menu_button` is a `*bool`, true when absent.
- No key for the web UI's own address elsewhere: nothing else needs it, and
  the HTTP server cannot know the address a TLS proxy publishes it under.
- The block goes through the JSON mirror, the settings form, the schema, the
  example config and the `configure-coddy` skill, like every key.
- `gatewayFingerprint` moves from `cmd/coddy/serve.go` into the gateway
  package's untagged `serve.go` (`gateway.Fingerprint`) and is built from the
  whole Telegram block, so a key added later rebuilds the bot on a reload
  without anyone remembering to list it, and from the keys of the HTTP server
  that decide whether the bot may advertise the web UI (D8): a sign-in set up
  from the settings screen is how a withheld menu button appears.

### D8. The bot offers the Mini App, and only one that asks for sign-in

`external/gateway/telegram/miniapp.go`, behind the gateway tag:

- **Who may see it.** The bot's default menu button is shown to everybody who
  opens a private chat with the bot, whatever `default_access` says, and the
  web UI may run with no sign-in at all. So the bot hands out the address -
  menu button and `/app` - only when the web UI of this process asks for a
  credential (a sign-in or a token, from the file, a flag or the environment)
  or the operator said otherwise with `httpserver.allow_insecure: true`. The
  answer comes from `cmd/coddy/serve.go`, which knows every source of
  credentials, as `gateway.Options.WebUI` (`WebUIAccess`: `Gated`,
  `OpenByChoice`, `Open`, or `Elsewhere` when this process does not serve the
  web UI, which the bot cannot check and only logs). When the web UI is open the
  bot logs why, puts back a menu button it set before, and `/app` says what is
  missing; `coddy serve --dry-run` warns the same way and asks the address for
  the web UI.
- **Menu button.** On every start, with `url` set, `menu_button` on and the web
  UI allowed, the bot's default menu button (`setChatMenuButton` without
  `chat_id`, label "Coddy") opens `url`. The first time, the bot reads the
  button it replaces (`getChatMenuButton`) and keeps it with what it set, per
  bot, in the gateway store (reserved entries next to `$last_model`). When the
  button is no longer wanted, the bot puts back what it replaced (the commands
  when that was nothing of the operator's), but only if the button still opens
  the address it set; a button the operator changed in @BotFather in the
  meantime is left alone and forgotten.
- **`/app`.** Answers with one button, "Open in Coddy", that opens the chat's
  own session (`url?session=<id>`, built with `net/url`, so a `url` with a
  query of its own keeps it), or the start screen when the chat has none yet;
  the session is only looked up, never created. In a private chat it is a
  `web_app` button; Telegram allows those only there, so a group gets an
  ordinary `url` button to the same address. Without `url` the command says
  which key turns it on. It passes the access checks every command passes,
  joins `setMyCommands` and `/help` only while `url` is set, and counts as
  addressed to the bot in a group.
- The library the gateway uses (`go-telegram-bot-api/v5`) has no Mini App
  types, so the calls go through `MakeRequest` with JSON of our own, the way
  `richclient.go` sends Rich Messages.

### D9. Sign-in stays as it is

Signing in with Telegram's `initData` (HMAC-SHA256 under the bot token) would
spare the password, but it is a new way into an agent that runs commands, and
it does not belong in this change:

- `httpserver` has no hook for it: the session store and the login policy are
  unexported, and the only mode is `password`;
- `default_access` is `all` by default, so "a user the bot talks to" is not an
  allowlist; a Telegram sign-in needs its own (admins by default);
- it would still not work in Telegram Web, where the cookie cannot travel
  (P8), without loosening the cookie's SameSite and the same-origin checks the
  CSRF defence rests on.

A Mini App on a phone or a desktop runs in a top-level WebView, where the
password sign-in and its cookie work as in a browser. Proposed as a follow-up:
`gateways.telegram.mini_app.sign_in` naming who may sign in (admins by
default), a method the gateway offers through `serve.Runtime` the way it offers
its permission prompts, `initData` checked against the token and an
`auth_date` window, a session in the same store.

What this change does about P8 is generic and small: when the server accepts
the password but the next `/coddy/auth/me` still has no session, the sign-in
screen says that the browser did not keep the sign-in in this frame and offers
to open Coddy in a tab of its own. Any third-party frame gets the same honest
answer; nothing in it is Telegram's.

### D10. Where the code lives, and a build without the gateway

- **SPA:** everything in `external/ui/src/ui/telegram/` - `capture.ts` and
  `launch.ts` (D2), `bridge.ts` (D1), `miniApp.ts` (D3, D5), `backButton.ts`
  (D3), `telegram.css` (D4) and their tests. `telegram.css` is imported by
  `miniApp.ts`, which `main.tsx` imports after `styles.css`, and every rule in
  it starts with `html[data-telegram-mini-app="true"]`, so it wins whatever the
  order; its contract test holds both. Outside the folder: `main.tsx` (the
  capture import and the call after the theme), the generic sign-in note of D9
  (`auth/SignInScreen.tsx`, two strings per dictionary), and
  `chat/ContextBreakdownPopover.tsx` (D4). `layoutGridCss.test.ts` now reads
  every stylesheet under `src`, so the grid covers `telegram.css` too. The
  SPA's Mini App mode needs nothing from the server, so it works the same
  against a binary built without the gateway (an operator can point a
  @BotFather button at it by hand).
- **Go:** the config block in `internal/config` (untagged, like every gateway
  key), `gateway.Fingerprint` and `WebUIAccess` in the untagged
  `external/gateway/serve.go`, `webUIAccess` next to the banner's auth summary
  in `cmd/coddy/serve.go`, the dry-run check in `internal/dryrun`, everything
  else in `external/gateway/telegram` behind `gateway || gateway.telegram`. No
  new HTTP route, nothing in `httpserver`.
- **Checked:** `go build` and `go vet` with no tags, `http`, `http ui`,
  `gateway.telegram`, and the full set; `make test`; the CI matrix.

### D11. tgfake emulates a Mini App

- **Bot API:** `web_app` on inline buttons (private chats only; `https`, or
  `http` on a loopback host, otherwise the error Telegram gives; exactly one
  action per button); `setChatMenuButton` / `getChatMenuButton` for the default
  and per chat; `/sim/state` and `/sim/chat/{id}` name the menu button. Reply
  keyboards stay unmodelled: nothing in Coddy sends one, and a Mini App opened
  from one gets no launch data at all.
- **The token:** the one of the latest Bot API call when `--token` is not
  given, so the stand needs no new flag to sign `initData`.
- **`POST /sim/webapp/launch`** `{chat_id, user_id, username, first_name, url,
  start_param, color_scheme, platform, version}` returns the launch URL and
  `initData` the way Telegram builds them for an inline or menu button:
  `query_id`, `user`, `auth_date`, `start_param` when given, `hash` = hex
  HMAC-SHA256 of the data-check-string under HMAC-SHA256(key "WebAppData",
  token). `signature` (Ed25519 under Telegram's key) cannot be produced and is
  left out. The signing is pinned to a vector computed outside the package.
- **The chat page** opens a Mini App from a `web_app` button or the menu button
  in a phone frame beside the chat. The WebView is an iframe and the page is
  its Telegram, the way web.telegram.org is: it answers the requests of D3,
  shows Back when the app asks for it and sends `back_button_pressed`, paints
  its header with the colour the app sets, and has controls for the size,
  expanded or half open (the iframe keeps the full height and the frame clips
  it, as on a phone), light or dark (`theme_changed`), fullscreen insets
  (`safe_area_changed`, `content_safe_area_changed`) and an Android-style
  keyboard (the WebView shrinks). It logs every event both ways and keeps them
  in `window.__tgfakeMiniApp` for scripts.
- **Same site or not.** The page and a Coddy on the same host are one site to
  a cookie (ports do not count), so the frame signs in the way a phone's
  WebView does. The page opened on `localhost` with Coddy on `127.0.0.1` is a
  cross-site frame, the way Telegram Web is, which is where the sign-in note
  of D9 shows.

## 5. Tests

- **Go, config:** `mini_app.url` validation table (https, loopback http,
  fragment, user info, relative, empty), `MenuButtonEnabled`, the JSON mirror
  round trip, the settings form, `TestDocsConfigSchemaMatchesStructs` through
  the schema.
- **Go, gateway:** `features/gateway_telegram_mini_app.feature` (the bot sets
  its menu button to the web UI; `/app` opens the chat's session; a bot that no
  longer offers a Mini App puts its menu button back), run against `tgfake`;
  unit tests for the link (with a query of its own), the group fallback, the
  start screen when the chat has no session and no session minted for it, the
  refusal without `url`, the command list and `/help`, the menu button (taken,
  put back to what it replaced, left alone when changed in @BotFather, never
  touched without `url`, not remembered when refused, withheld and taken back
  for an open web UI), `/app` withheld for an open web UI; the store keeps the
  set and the replaced button per bot and out of the session lookups;
  `gateway.Fingerprint` moves when any field of the Telegram block or any of
  the HTTP keys of D8 moves; `webUIAccess` for every source of credentials.
- **Go, dry run:** an open web UI is warned about at the line of
  `mini_app.url`; the address answers with the web UI, answers something else,
  or cannot be reached.
- **Go, tgfake:** `features/tgfake_mini_app.feature` (a `web_app` button and
  the menu button open a launch whose `initData` checks out against the
  token, checked by a test-side implementation of the documented algorithm),
  the fixed vector, unit tests for the refusals Telegram gives, the menu button
  round trip, the token, the launch options and the page.
- **SPA:** vitest for `launch` (fragment and query forms, persistence, URL
  clean-up, the session link in and out of a launch, an id outside the
  alphabet, storage that throws), `bridge` (each transport, the receivers, the
  parent-only iframe messages, `set_custom_style`), `miniApp` (the start
  sequence and its order, version gates, events to CSS variables, the sum of
  the insets, colours on a theme change, theme without a cookie, with one, and
  a pick in the middle of a session, the resize after a change of the hidden
  part), `backButton` (visibility, a claimed and an unclaimed Escape, the
  question card untouched, a dialog that stops the key on `window` and the
  keyboard Escape after it), the CSS contract of `telegram.css`, and the
  sign-in note; `features/web_ui_telegram_mini_app.feature` binds them as the
  happy path; `index.html` loads nothing from another origin.
- **Browser:** `npm run check:telegram` (`scripts/telegram-mini-app-check.mjs`):
  the real `coddy serve` (`TAGS="http ui gateway"`) and `tgfake` with the
  scripted model; the bot answers, `/app` gives a button, the page opens it in
  its phone frame, and in the half-open state - entered while the transcript
  is parked at its newest message - the composer, the mode sheet, the context
  sheet on the start screen and the last message are inside the visible part,
  Back closes the sheet, the sticky title sticks, nothing scrolls sideways; the
  cross-site frame shows the sign-in note. Local, like `check:transcript`.
- **Builds:** the tag sets of D10.

## 6. Documentation

- `docs/surfaces/web-ui.md`: the Mini App section rewritten (what the UI does
  inside Telegram, no third-party script, the half-open rules, Back, the
  theme, the deep link, Telegram Web and sign-in), with English screenshots
  captured through `tgfake`.
- `docs/surfaces/gateway.md`: a Mini App section (HTTPS behind a TLS proxy,
  sign-in on, `mini_app.url`, the menu button and @BotFather, `/app`, security
  notes), `/app` in the commands table, the Mini App panel in *Debugging
  against a fake Bot API*, the stale `start.go` names.
- `docs/reference/config.md` (generated), `config.example.yaml`, the
  `configure-coddy` skill, `DESIGN.md` (the Telegram Mini App rules),
  `docs/plans/telegram-mini-app.md` (this record).
- `.claude/rules/gateway.md`, `.claude/rules/ui-spa.md` and their `.cursor`
  mirrors, the map rows of `AGENTS.md` for the gateway, `tgfake` and the SPA.

## 7. Order of work

1. This record; cross-review round 1; corrections.
2. `tgfake`: Mini App support with its tests.
3. Config: the block, its mirrors and checks, `gateway.Fingerprint`.
4. Bot: menu button and `/app` with the feature.
5. SPA: `ui/telegram/` (launch, bridge, window, theme, back), `telegram.css`
   replacing the block in `styles.css`, `index.html` back to `main`, the
   sign-in note, tests and the feature.
6. The browser check, screenshots, documentation.
7. The side findings: stale names, the two harnesses on `tgfake`.
8. `make test`, `make lint`, `make docs-check`; cross-review round 2;
   corrections.

## 8. Out of scope

- Signing in with Telegram (D9), proposed as a follow-up.
- Fullscreen mode (`requestFullscreen`): the content safe area is handled, the
  request is not made.
- A per-chat menu button that always opens that chat's session: one call per
  session change and stale buttons to clean when the URL goes; `/app` does the
  job.
- Telegram's main and secondary bottom buttons, haptics, cloud storage.

## 9. Cross-review, round 1

Four reviewers on the plan (b30c5629), blind to each other: Coddy on
`codex/gpt-5.6-sol` (needs rework), Coddy on `devin/swe-2` and Cursor `auto`
(approve with changes), Coddy on `neuraldeep/qwen3.8-27b-noreason` (cut off
at `max_tokens` twice, its findings taken from its own list). The orchestrator
verified each finding against the code and the Telegram documentation and
decided: approve with changes. The changes, now in sections 4 and 5:

- **high:** the menu button is shown to everybody who opens the bot, and the
  web UI may run open; the bot advertises only a web UI that asks for a
  credential, or one the operator opened on purpose (D8, D7 fingerprint);
- the context sheet's inline bottom on the start screen (D4,
  `ContextBreakdownPopover.tsx`);
- the lift has to reach the transcript and the sheets over the docked
  composer: the composer grows its padding, so the reserve follows, and a
  `resize` announces the change (D4);
- the session link in any browser, held to the session id alphabet, the start
  parameter only inside a launch (D6);
- the back button and the question card said precisely, the listener removed
  in `finally` (D3);
- guarded storage (D2); the iframe rules written out (D1); the grid test and
  the CSS order (D10); the menu button's ownership, with the replaced button
  put back (D7, D8); its label and the link built with `net/url` (D8); a fixed
  vector for the signature and no reply keyboards (D11); the same-site and
  cross-site frames of the stand (D11); the theme read again on every change
  (D5).

Rejected, with the evidence in the review: a session id in a group message as a
leak (one operator account sees every session anyway), the query in proxy logs
(every API path carries the session id already), a fingerprint without
`EffectiveToken` (kept, no defect either way), Back skipping the question (an
accidental hardware back would answer the agent), loading the SDK only inside
a launch (it keeps the half of P1 that happens inside Telegram), the sum of the
safe areas (the documentation measures the content area from the top of the
content, and the sum errs on the safe side).

## 10. What implementing changed

The plan held; three things went differently, all found by the live check
(`npm run check:telegram`), which drives the stand of D11 in a real browser.

- **The lift over the docked composer is a block, not padding.** D4 grew the
  composer's padding by the hidden part. `ChatScreen` measures the reserve
  from the content box of `.chat-bottom-inner`, so padding on `.chat-bottom`
  was never seen and padding on the inner element lies outside the box it
  observes: the composer rose and the transcript's tail stayed under it. The
  lift is now an empty block after the composer column's content,
  `.chat-bottom:has(.composer-wrap-docked) .chat-bottom-inner::after`, as tall
  as the hidden part beyond the keyboard. It is content, so the observer sees
  it, the reserve grows and a transcript parked at its newest message follows,
  still with nothing new in `ChatScreen`.
- **`resize` only when the hidden part moves.** The first application of the
  viewport runs before React mounts and dispatches nothing; later
  `viewport_changed` events dispatch one only when
  `--coddy-telegram-hidden-bottom` changed, so a keyboard, which the window
  reports itself, or a repeated stable height does not make every sheet
  measure again.
- **The stand's own defects.** The loading veil of the phone frame took its
  `display: flex` over `[hidden]` and caught every click; the sheet positions
  were first compared in page coordinates rather than the frame's; a run that
  failed left its servers on their ports. The veil hides, the check measures
  inside the frame, refuses ports that are busy and exits when it is done.

## 11. Cross-review, round 2

The implementation (8b6f68a9..a2d17e68) in two briefs, the Go half and the
SPA half, to Coddy on `codex/gpt-5.6-sol`, Coddy on `devin/swe-2` and Cursor
`auto`; Coddy on `neuraldeep/qwen3.8-27b-noreason` got the production code
alone, since it stalls on long briefs, and finished the SPA half only when
its session was continued after the output limit cut it off. Every reviewer
said approve with changes, and so did the orchestrators after checking each
finding against the code, the Telegram documentation and the source of
`telegram-bot-api`. Fixed:

- **medium:** Back did not reach the composer's slash and `@` pickers or the
  image viewer, which heard Escape only with the focus inside them; a click
  on Telegram Web's Back leaves the frame's focus on `body`, so the press
  left the conversation instead. Both now listen through `useEscapeCloses`,
  as the SPA's rule for menus and dialogs says;
- **medium:** a `getChatMenuButton` that failed on the first takeover lost the
  button set in @BotFather for good; the bot now leaves a button it cannot
  read alone until the next start;
- an operator's button on the bot's own address never came back (told apart
  from the bot's by its label now, and not put back to advertise an open web
  UI); the fingerprint rebuilt a bot with no Mini App on a new web UI
  password (the HTTP keys count only with `mini_app.url`); tgfake showed the
  bot's menu button in groups and answered `getChatMenuButton` for a group's
  id, where the Bot API reads `chat_id` as a user; the description of
  `menu_button` and a sentence about where the launch data is kept.

Rejected, with the evidence in the reports: the menu button synced only at
start (the fingerprint covers the keys of the gate, and the manager publishes
the configuration before it tells the supervisor), origin checks on the
frame's messages (only `window.parent` is heard and the events only present,
which is how the SDK filters too), the safe area requested from clients older
than 8.0 (the SDK sends both requests unconditionally), and the observer of
the back button watching `body` without its subtree (every layer is a portal
into `body`).

Open: the hidden part is the WebView's height minus the stable height, which
assumes that a half-open Mini App keeps its full height with the lower part
off the screen, as Telegram's documentation of `viewportHeight` says. The
stand encodes the same assumption, so only a half-open app on a real Android
or iOS client settles it.

Done in the same branch while the round ran: the `/resume` and subagent
permission harnesses moved onto tgfake, a tap on the current model no longer
re-sends an identical menu (Telegram refuses it), the long-model scenario that
could not fail, an MCP test that left its stub running for an hour, and the
godog steps of the web UI reading one Vitest run per file (the package went
from 950 s to 214 s on a loaded machine).

## 12. Code scanning

Semgrep's `wildcard-postmessage-configuration` flagged the one alert this
change added: the bridge posted its events to Telegram Web's frame with the
target origin `"*"`, as the SDK does. It now addresses the parent's origin
(`parentOrigin` in `bridge.ts`): the browser's record of it
(`location.ancestorOrigins`), else the origin of the page the frame was
loaded from (`document.referrer`), else `https://web.telegram.org`, and it
takes a message from `window.parent` at that origin only. Firefox has no
`ancestorOrigins`, so a parent there that sends no referrer is taken for
Telegram Web, which is the parent a Mini App has in that browser.


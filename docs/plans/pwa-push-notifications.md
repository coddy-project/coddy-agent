# Plan: notifications with no tab open (issue #508, level two)

Status: not scheduled. The maintainers do not plan notifications for a closed app for now; a
contributor who wants them can take this plan and open a pull request, and section 8 lists what
the maintainers still have to decide. Level one - the installable web UI and the notifications
of an open tab - is implemented on `feat/pwa-notifications`
(`features/web_ui_installable_app.feature`, `DESIGN.md` *Installable app and notifications*,
`docs/surfaces/web-ui.md` *Notifications*). This record plans what reaches the person when no
tab of Coddy is open: Web Push for the installed web UI, and the Telegram bot for the people
who use Coddy from the Mini App or live in Telegram anyway.

## 1. Where level one stops

The notices of level one are decided and shown by a tab:

- a tab hears `turn_ended`, `subagent_permission` on `GET /coddy/events` and the permission and
  question frames of the turn streams it reads, and `AttentionTracker`
  (`external/ui/src/ui/pwa/attentionTracker.ts`) keeps those of the chats it follows: the one on
  screen and the ones it sent a prompt to;
- the service worker (`external/ui/public/sw.js`) shows a notice only while no window of the app
  has the focus, and a tag once;
- with every tab closed nothing is announced, and the Telegram Mini App has no notifications at
  all: Telegram's WebView on Android has no Notification API, and the Mini App's page is gone the
  moment the person closes it.

Two facts of the server shape the rest of this plan:

- the server has no record of *who* follows a session. A turn does not carry who started it, and
  the auth session's subject (the account name of the sign-in form, `telegram:<id>` of a Mini App
  sign-in, nothing for a bearer token) is not kept anywhere a turn can read it;
- the events the server publishes do not say what a notice needs. `turn_ended` carries no outcome
  (`internal/session/turn_active.go` releases a counting registry), and a permission request of a
  top-level turn exists only on that turn's composer stream (`internal/permission` through the
  agent's sender); `session_question_pending` names the session and nothing else.

## 2. Goal

A person who started a turn, or follows a chat, learns that the agent finished or waits for them,
on the device they are using, when no tab of Coddy is in front of them: a desktop browser with
every tab closed, a phone with the installed web UI asleep, or Telegram with the Mini App closed.
One occurrence makes one notice per device. A tab that is open keeps doing what level one does.

## 3. The shared server part (phase 2a)

### 3.1 Attention events on the server

A new process-wide bus in `internal/session` (untagged): `AttentionEvent` with

- `Kind`: `turn_finished`, `turn_failed`, `permission`, `question`, `subagent_permission`;
- `SessionID`, `At` (the `at` of the matching `turn_ended`, so a tab and a push build the same
  tag), `Key` (the tool call id, the request id, `<child>:<tool call>`);
- `Title` (the session's title at the time), `Tool` (the request's tool title), `Error` (the
  first line of a failed turn's error, at most 140 characters);
- `Principals`: who the event is for (3.2).

Publishers: the turn release in the session manager, which learns the stop reason and the error
from the runner it already wraps; the permission gate and the question tool, at the point they
hand the request to the turn's sender; the detached prompt hub (`serve.Runtime`) for a background
subagent's prompt. `GET /coddy/events` keeps its frames as they are; the bus is for notifiers
inside the process.

### 3.2 Who follows a session

- The HTTP server puts the request's principal on every turn it starts (`session.PromptRunOpts`
  gains `Principal`): `user:<account>` for the sign-in form, `telegram:<id>` for a Mini App
  session (the subject that session already has), `token` for a bearer, `local` for an open
  server.
- The session keeps a small follower set in `session.json` (`followers`: principal and the time
  of its last prompt), written when a principal sends a prompt. A chat bound to a Telegram private
  chat counts that chat's user as a follower too.
- An event goes to the followers of its session. The turns of a scheduler run and of a
  subagent's own session have no followers; their parent's events do.

### 3.3 Presence: is somebody looking

Push only what nobody sees. Tabs report attention through `POST /coddy/presence`
(`{device, attentive}`) on `visibilitychange`, `focus` and `blur`, and every 60 seconds while
attentive; the server keeps it per principal and device for 90 seconds. A device is a random id
the SPA keeps in `localStorage` (`coddy_device_id`), sent with the presence and with the push
subscription.

- A principal with an attentive device gets nothing pushed: a tab is in front of them.
- A device with a connected `GET /coddy/events` stream but no attention is served by its own tab
  (level one) and skipped by the push: one notice per device.
- Every other subscribed device of the followers gets the push.

Tests: unit tests of the presence table (expiry, several devices, a sign-out) and of the
follower set; a godog scenario in `features/` once a notifier consumes them.

## 4. Web Push for the installed web UI (phase 2c)

### 4.1 Protocol

- RFC 8030 (the push protocol), RFC 8291 (message encryption, `aes128gcm`), RFC 8292 (VAPID).
- A new untagged package `internal/webpush`, standard library only: `crypto/ecdh` for the P-256
  exchange, `crypto/hkdf` and `crypto/aes` GCM for RFC 8291, `crypto/ecdsa` for the ES256 VAPID
  JWT. About 300 lines; no third-party module, in keeping with the lean dependency set. The RFC
  8291 appendix has a worked example, which becomes the unit test.
- The VAPID key pair is generated on first use into `<home>/webpush.json` (mode `0600`) and
  served by `GET /coddy/push/key`. It stays stable: a new key invalidates every subscription.

### 4.2 Subscriptions

- `POST /coddy/push/subscriptions` with the browser's `PushSubscription` (`endpoint`,
  `keys.p256dh`, `keys.auth`), `device` and `lang`; `DELETE /coddy/push/subscriptions/{id}`;
  `GET /coddy/push/subscriptions` lists the principal's devices (for a list in Settings later).
  All behind the auth gate, `openapi.go` and `docs/reference/http-api.md` in step.
- Stored in `<home>/push-subscriptions.json`, keyed by a hash of the endpoint, with the principal,
  the device, the language and the last success. Signing out (`POST /coddy/auth/logout`) drops
  the device's subscription; a Telegram principal that stops being an admin, or a rotated bot
  token, drops that principal's subscriptions, as it ends their Mini App sessions today.

### 4.3 Sending

- The notifier consumes the attention bus (3.1) and the presence (3.3), builds the notice and
  POSTs it to each endpoint with `TTL: 3600`, `Urgency: high` for a permission or a question and
  `normal` for a turn's end, and `Topic` derived from the tag, so the push service replaces an
  undelivered notice instead of queueing both.
- Answers: `201` is a success; `404` and `410` delete the subscription; `429` backs off for
  `Retry-After`; `413` cannot happen with a payload under 1 KB, which is the budget.
- The payload is the level-one notice: `{v, kind, sessionId, key, title, body, url, env, lang}`,
  so the service worker shows the tag a tab would have shown and a duplicate on one device
  replaces itself. The body is worded on the server in the subscription's language from a small
  table of the five notices (English, Russian), held to the SPA's `notify.*` keys by a Go test
  that reads `en.ts` and `ru.ts`.
- Outbound requests go to Google's, Mozilla's or Apple's push service, whichever endpoint the
  browser handed over, with the process environment's proxy. Push services see the ciphertext,
  its size and its timing, nothing else; the body never quotes the answer.

### 4.4 The browser side

- `sw.js` gains a `push` handler that always shows what it got (Chrome requires a visible
  notification for every push with `userVisibleOnly: true`; Safari revokes a subscription after
  silent pushes), and a `pushsubscriptionchange` handler that subscribes again and replaces the
  old endpoint on the server.
- Settings → Appearance → Notifications gains a second switch, **Also when Coddy is closed**,
  enabled where `PushManager` exists, the worker is active and, on iPhone and iPad, the page runs
  as the installed app (`display-mode: standalone`). Turning it on asks the same permission,
  subscribes with the server's key and posts the subscription.
- Remote environments and a swarm relay: a subscription belongs to one origin's service worker
  and one server key, so phase 2c pushes for the page's own server only and hides the second
  switch elsewhere. Pushing for the nodes behind a relay means the relay holds the keys and the
  subscriptions and the nodes forward their attention events through the tunnel: a later phase.

### 4.5 Configuration

`httpserver.push.enable` (default `true`: nothing is contacted until a browser subscribes) and
`httpserver.push.subject` (the VAPID `sub`, a `mailto:` or `https:` address; Apple refuses
others). Steps 7 and 8 of the workflow rule: the schema, `config.example.yaml`, the field tables,
the `configure-coddy` skill, the site copy of the schema.

### 4.6 Tests

- `internal/webpush`: the RFC 8291 example round trip, the VAPID JWT verified with the public key.
- A godog scenario against a fake push service (`httptest`) that decrypts with the subscription's
  private key: a turn ends with no tab attentive, one message arrives and decrypts to the notice;
  with an attentive tab nothing arrives; a `410` removes the subscription.
- `sw.js` `push` and `pushsubscriptionchange` handlers in the vitest suite, the way level one
  runs the script against a stand-in for the worker's scope.

## 5. The Telegram bot for the Mini App (phase 2b)

### 5.1 Why the bot

A Mini App gets no web notifications: its WebView on Android has no Notification API, and the page
is gone when the person closes it. The bot's own messages are what Telegram delivers to the
person's phone anyway, with Telegram's sound and badge. Today the bot speaks only in the chat
bound to a session - its own turns, the woken turns - and offers a detached subagent's prompt
there. A turn an admin starts from the Mini App runs through the HTTP server; when the admin
closes the Mini App before it ends, nothing tells them.

### 5.2 What the bot sends

- A notifier inside the Telegram subsystem consumes the attention bus (3.1). It takes the events
  whose followers include `telegram:<id>` of one of the bot's admins.
- Nothing is sent while the admin's Mini App is in front of them. The Mini App reports presence
  like a tab (3.3), and its attention follows Telegram's `visibility_changed` web event
  (`is_visible`; `telegram-web-app.js` turns it into `activated` and `deactivated`), which the
  bridge (`external/ui/src/ui/telegram/bridge.ts`) can hear without Telegram's script.
- Nothing is sent for a turn the chat itself ran: the chat already holds its answer.
- Otherwise the bot writes to the admin's private chat: one line with the chat's title and what
  happened, and a `web_app` inline button **Open in Coddy** on `url?session=<id>` (inline
  `web_app` buttons work in private chats, which is where these go). Messages of one session
  within a few seconds are merged, to stay inside Telegram's per-chat limit of about one message
  per second.
- A permission request is offered in the same message with the bot's existing buttons
  (`external/gateway/telegram/permission.go`, `chatPermissions.ask`): the first answer wins, in
  the chat or in the web UI, as for a detached subagent today. A question is not answered in the
  chat (the bot's `RequestQuestion` only shows the text); its message opens the Mini App.

### 5.3 The admin's choice

- On by default for the turns an admin started from the Mini App, off for everything else; the
  operator decides this default (8).
- `/notify on|off` in the private chat, and in the Mini App the switch of Settings → Appearance
  → Notifications becomes **Notify me in this chat** (`PUT /coddy/notifications/telegram`), kept
  in `gateway_sessions.json` per user id.
- A Mini App opened from a direct link (`t.me/<bot>/<app>`) may not be allowed to message the
  person. Turning the switch on then posts `web_app_request_write_access`; the client answers with
  `write_access_requested` (`status`: `allowed` or `cancelled`), and the switch stays off on
  `cancelled`.

### 5.4 A home screen shortcut

Telegram can put a shortcut to the Mini App on the phone's home screen
(`web_app_add_to_home_screen`, answered by `home_screen_added` or `home_screen_failed`;
`web_app_check_home_screen` and `home_screen_checked` for the state). It gives a person who uses
Coddy through Telegram the same app-like start the PWA gives a browser user. Optional, one button
in Settings → Appearance inside the Mini App.

### 5.5 tgfake and tests

- tgfake has to send `visibility_changed` to the Mini App it plays, answer
  `web_app_request_write_access`, and show `web_app` inline buttons it can tap. The change goes
  into tgfake, is released and bumped here (`docs/contributing/tgfake.md`).
- A godog scenario against tgfake: an admin sends a prompt from the Mini App, the Mini App goes
  invisible, the turn ends, the private chat receives one message with **Open in Coddy**; with the
  Mini App visible nothing arrives; a permission request is answered by a tap in the chat and the
  turn goes on.

## 6. Order of work

1. **2a** - the attention bus, the principal on a turn, the follower set, the presence endpoint.
   No behaviour a person sees changes; level one keeps working as is.
2. **2b** - the Telegram notifier. Smaller than Web Push: no cryptography, no third-party push
   service, and it covers the phones of the people who already use the Mini App.
3. **2c** - Web Push, the second switch, `sw.js` push handling.
4. **2d** - push for the nodes behind a swarm relay and for remote environments.

2b and 2c depend only on 2a and can go in either order or in parallel.

## 7. Limits that stay

- Web Push needs a secure origin, like the install; a server reached over plain http on a LAN gets
  neither.
- Push services are third parties: a self-hoster who wants none turns `httpserver.push.enable`
  off and keeps level one and the Telegram path.
- On iPhone and iPad, Web Push reaches only the web UI added to the home screen (iOS 16.4 and
  later), and the person has to turn the switch on from inside that app.

## 8. Decisions for the operator

- Default of the Telegram notifications: on for Mini App turns (proposed), or off until
  `/notify on`.
- Default of `httpserver.push.enable`: on (proposed: the server contacts nothing until a browser
  subscribes) or off.
- The VAPID subject: the operator's `mailto:` from the config, or `https://coddy.dev` as the
  default.
- Whether 2b or 2c goes first.

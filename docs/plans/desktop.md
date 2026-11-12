# Coddy as a desktop application

Design record for `coddy desktop` and the `desktop` build tag: a window on this machine that holds
several chats as tabs, over the web UI the binary already serves.

Revised after a cross-review of the first draft. What that review changed is recorded at the end,
because two of the mechanisms the first draft leaned on do not exist the way it assumed.

## Why

The web UI is already an installable PWA (`external/ui/public/manifest.webmanifest`, `display:
standalone`, the service worker and the notifications of `external/ui/src/ui/pwa/`), so a browser
can already give it an icon and a frameless window. What it cannot give is a window that the
operator starts the way they start any other program, that holds several conversations side by side,
and that goes away when they close it. That is what this adds.

Two things are deliberately **not** the goal: a second rendering stack (the window shows the SPA
that already exists), and a second way to run the agent (the window is a client of an ordinary
serve pipeline).

## Shape

Three pieces, each small, and the interesting one is in the middle.

1. **The command.** `coddy-desktop`, a binary of its own, starts the HTTP surface on a remembered
   loopback port and opens a window on it.
2. **The shell.** A page the server answers at `/desktop/`: a tab strip over one `<iframe>` per
   chat. This is where tabs live, and where a later split screen will live.
3. **The window.** Either a Chromium-family browser in app mode (`--app=`), or a native window
   around the system WebView. Both load the same shell page.

The shell being a page, not a native tab bar, is the decision the rest follows from. One
implementation of tabs serves both window backends, it is testable in a browser, and split screen
later is a layout change inside that page rather than a native API.

```
coddy-desktop
  ├─ goroutine: the serve pipeline, HTTP subsystem only
  │     GET /desktop/   ← the shell page, under the "/" catch-all
  │     GET /           ← the SPA, once per tab in an iframe, routed client-side to #/s/<id>
  └─ main goroutine: the window
        ├─ browser backend:  chrome --app=http://127.0.0.1:<port>/desktop/ --user-data-dir=...
        └─ native backend:   webview → the same URL        (tag: desktop.native, cgo)
```

The frame address is written `/` and not `/#/s/<id>` on purpose: a fragment never leaves the
browser. The tab navigates client-side, which is why `App.tsx` reading the hash once at boot
(`initialRoute`) is enough for a frame to open on the right session.

## The port is remembered, not ephemeral

`<home>/desktop/port` holds the loopback port this machine's desktop uses. First launch picks a
free one and writes it; every later launch reuses it.

An ephemeral port was the first draft's answer and it was wrong, because the origin includes the
port and `localStorage` is scoped to the origin. A new port every launch would discard the tab list
the shell is supposed to restore, and with it `coddy.tourSeen`
(`external/ui/src/ui/onboarding/tourState.ts`), so the first-run tour would replay in every tab on
every launch, plus `coddy_env`, `coddy_env_tokens`, `coddy_env_routes`
(`external/ui/src/ui/env/remoteEnv.ts`) and `coddy_draft_sessions_v1`
(`external/ui/src/ui/sessions/draftSessions.ts`). Cookies are host-scoped rather than port-scoped,
which is why the theme survives a moving port and nothing else does.

A remembered port answers three questions at once:

- **the tab list survives a relaunch**, which is the point of tabs;
- **readiness needs no new server API.** The launcher knows the address before it starts anything,
  so it dials it until it accepts or the pipeline reports a failure. The first draft wanted the
  bound address back out of `httpserver.Serve`, which cannot give it: `opts.OnServer(s)` fires some
  sixty lines before `srv.ListenAndServe()` (`external/httpserver/serve_http.go`), and
  `httpx.NewServer` keeps only the requested address string, never the listener;
- **a second launch is a focus, not a fight.** If the remembered port is already serving a desktop,
  the second `coddy desktop` starts no pipeline at all and only opens a window. That is what the
  operator means by starting the app twice, and it sidesteps the Chromium profile singleton, which
  would otherwise hand the `--app=` window to the running instance, exit at once, and have the
  second launcher read that exit as "the window closed".

When the remembered port is taken by something that is not a Coddy desktop, the launcher picks
another free port and records it, saying so: one launch loses its tab list, which beats refusing to
start.

## The shell page

Served under the `"/"` catch-all, not as a route of its own. `external/httpserver/spa_embed_ui.go`
owns `"/"` and is where the `/desktop/` prefix is answered, because `isProtectedPattern`
(`external/httpserver/auth.go`) exempts exactly `""` and `"/"`, the sign-in routes and the browser
extension's two. A registered `GET /desktop/` would be protected, so with a sign-in form configured
the window would open on a bare `401` with no way to present a password - the form the SPA shows is
reachable only because the SPA is that catch-all. Serving the shell there gives it the SPA's
treatment, which is correct: the page carries no data, and every byte it draws still comes from the
gated `/coddy/sessions`.

The page itself is `external/desktop/shell`: one HTML file, one stylesheet, one script, embedded
with `go:embed`, no React and no vite entry. It is a separate package from the launcher so that
`external/httpserver` can import it without the cycle the launcher would create.

What it does:

- draws a tab strip and one `<iframe src="/#/s/<id>">` per tab, the inactive ones hidden rather
  than unmounted, so a tab keeps its transcript, its scroll position and its stream;
- reads `GET /coddy/sessions` for the titles and writes them with `textContent`, never `innerHTML`:
  a title is operator and model text drawn into a credentialed same-origin document;
- opens a tab, closes one and walks them by pointer (the `+` button, a tab, its `×`), and by
  `Alt`+`T` / `Alt`+`W` / `Alt`+`1`..`9`. The chords are `Alt` and not `Ctrl` because a Chromium
  `--app=` window owns `Ctrl`+`W` and `Ctrl`+`T` itself and will not let the page cancel them -
  under this lifetime `Ctrl`+`W` would close the application and stop the agent;
- delivers those chords from a focused chat, which is the normal state. A `keydown` inside an
  iframe is dispatched in that frame's document and never reaches the parent, so the shell attaches
  its listener to each frame's `contentDocument` on every `load`. That is allowed because the frame
  is same-origin, and it is not cooperation from the SPA: the SPA is not changed;
- remembers the open tabs in `localStorage`, which the remembered port makes meaningful;
- follows the theme the SPA records in the `coddy_ui_theme` cookie, over a handful of its own
  tokens, so the strip is not a bright band over a dark chat.

**What iframes cost.** Each tab is a full SPA instance, so memory multiplies; the streams do not,
because `external/ui/src/ui/chat/sharedServerEvents.ts` holds one `GET /coddy/events` for the whole
origin through a `SharedWorker`, falling back to a Web Locks election where there is none. The
native backend would be the first consumer of that fallback and no test drives it there yet.
Less obvious and worth saying: instances on one origin also share one `localStorage`, and nothing
in the SPA listens for the `storage` event, so `coddy_env` (one global key holding the selected
environment) and `coddy_draft_sessions_v1` (one list each instance rewrites whole) are
last-writer-wins across the window. The alternative - one SPA instance whose tabs only switch
`#/s/<id>` - costs no memory and loses every tab's state on every switch, which is the one thing a
tab is for. Iframes, then, with both costs on the page of the surface.

## The command

A binary of its own, `coddy-desktop`, not a verb of `coddy`. The operator asked for it that way and
it is the better split: `coddy serve` is a server that outlives every client, this is a client whose
server lives exactly as long as its window, and keeping them apart keeps the desktop surface out of
the binary that runs on servers and in containers, where no window can open. It also costs nothing
in `coddy`: no subcommand, so no `printUsage` entry, no man page, no completions.

```
coddy-desktop [--window auto|browser|native] [--browser PATH]
              [--config PATH] [--home DIR] [--cwd DIR] [--log-level LEVEL] [-t]
```

- **Default.** Binds the remembered loopback port and opens the window on `/desktop/`.
- **`--window`.** `auto` takes the native window when the binary has `desktop.native` and the
  system can open one, else app mode. It never falls through to the plain system opener, because
  the lifetime depends on waiting for a process and that rung has none: `platform.OpenBrowser`
  hands the URL to an already-running browser and returns in milliseconds, and
  `platform.LocalBrowserAvailable` is a guess its own doc comment forbids anything from depending
  on - it answers true on Android whenever `termux-open-url` is on `PATH`, and true unconditionally
  on Windows and macOS. With no waitable backend, the launch prints the URL and serves until a
  signal, the way `coddy serve` does.
- **`-t` / `--test-config`** behaves as on `cli`, `acp` and `serve`: the same `config.RunCheck`,
  nothing started.
- **`--remote` and `--dry-run` are not in the first cut.** `--remote` needs the one-time ticket of
  `external/browser/panel.go` to be adopted before a window can reach a token-only server at all (a
  navigation carries no bearer header), and `--dry-run` lives in `cmd/coddy` behind `runServeDryRun`
  and the serve flag set, neither of which a second main package can reach without extracting them.
  Both are additions, not corrections.

**Which subsystems run.** The HTTP surface, and nothing else, whatever `config.yaml` enables. The others are not merely redundant beside a running `coddy serve`,
they break: `internal/serve/supervisor.go` cancels the whole run on one subsystem's error, so a
second swarm relay that cannot bind 12346 takes the window down at startup; two Telegram pollers on
one token make the Bot API answer 409 and one bot goes deaf; two schedulers fire every cron job
twice. The binary carries none of their tags either, so `serve.Resolve` would refuse the
configuration by name. It prints one line naming what it left to `coddy serve`, and the settings
form of this binary does not draw those sections (`httpserver.Options.UnbuiltSections`) - which is
display only, since those sections stay in the document a save writes back.

**Lifetime.** The window owns the main goroutine and the pipeline runs behind it; closing the
window cancels the pipeline and the process ends. The window is not a `serve.Subsystem` because a
subsystem is something the supervisor starts, stops and rebuilds in place on a config reload, that a
`PUT /coddy/config` can switch off, and whose stopping never ends the process. A window is none of
those. The HTTP surface behind it still goes through a `Supervisor`, so a configuration saved from
the window's own settings screen reaches the server as it does under `coddy serve`; its address is
the launcher's and never the file's, so the one change a running server cannot adopt cannot happen
here, and `sup.Restartable` is false, since nothing would start this process again.

**Authentication.** The window asks for nothing (`httpserver.Options.NoAuth`, set only here): it
binds loopback for the person who started the application, and a sign-in form in a window they
opened themselves proves nothing. `--auth` keeps whatever the configuration carries, and then the
form is reachable because the shell is served under the catch-all, as above. The cost is stated on
the page: any program on this machine can reach the port, which is the bargain `coddy serve` makes
on loopback by default.

**And nothing in the configuration is edited to achieve any of this.** The first implementation had
`ApplyConfig` turn the other subsystems off in the loaded document, and that was a bug with teeth:
`configapi.Backend.Live` is `s.activeCfg`, so the settings screen inside the window draws the live
configuration, and a save from that screen writes what it drew. A theme changed in a desktop window
would have written `swarm.enable: false`, `gateways.telegram.enable: false` - and, once the window
stopped asking for credentials, the operator's sign-in account - to their own `config.yaml`. The
window runs one surface because `serve.Resolve` consults only the descriptors it is handed and it is
handed one, whose `Enabled` is unconditionally true (the window *is* that surface, so
`httpserver.enable: false`, set for a node that only polls a messenger, must not leave it with
nothing to open on). What the configuration enables elsewhere is only read, and named once on
startup (`SurfacesLeftToServe`).

**Why the browser backend needs a profile directory.** `--app=URL` against the operator's running
Chrome hands the window to that instance and the process we started exits at once, so we could not
tell when the window closed, and the agent's cookies would land in their everyday profile. A
dedicated `--user-data-dir` under `<home>/desktop/profiles/<browser>` makes the window a process of
its own that we can wait on, and keeps the session cookie out of the browser they read mail in. It
costs disk, which the page says. The profile is per browser rather than per run, which is what
makes the second-launch handoff above work out. A profile that does not exist yet is given one
preference before its first window (`seedProfile`): `translate.enabled: false`. The window's content
is Coddy's interface in the operator's own language and a chat is whatever they type, so Chrome
reads the page as foreign and anchors a translate bubble over the tab strip on every launch;
`--disable-features=Translate,TranslateUI` does not stop it in Chrome 157, and the preference does.

## Build tags

`desktop` carries everything that is pure Go and compiles anywhere: the launcher, the shell page,
the browser backend. It is in no CI job's way and needs nothing installed.

`desktop.native` carries the cgo window and only that. It is separate because the system WebView
needs development packages at build time (`webkit2gtk-4.1` on Linux) and cgo enabled, which the
test matrix, `make check-windows` and the cross-compiled release builds do not have. It is not in
this change at all, so `native_stub.go` carries `//go:build desktop` with no partition against an
implementation; the file that adds one takes the exact negation of
`desktop && desktop.native && cgo && <platforms>`, because written as `desktop && !desktop.native`
a build with the sub-tag and `CGO_ENABLED=0` would hold neither file.

| Tag | Holds | Needs |
|---|---|---|
| `desktop` | the shell page, the launcher, the app-mode browser backend | nothing |
| `desktop.native` | the WebView window | cgo, `webkit2gtk-4.1` (Linux), WebView2 (Windows), nothing (macOS) |

`cmd/coddy-desktop` is built with `http ui desktop` together, which `make desktop` is the one place
that spells. A build without them gets `main_stub.go` (`//go:build !(desktop && http)`), which names
the target that builds it, the way `cmd/coddy/cli_stub.go` does for its surface; without that file
the package would have no Go files at all and `go test ./...` fails on such a package when it is
named, which the test matrix does.

### What ships with it

Nothing yet, and that is the operator's decision to start with the Linux build: `desktop` stays out
of `FULL_TAGS`, so the `coddy` binary, the container image and the packages carry no desktop
surface, and `coddy-desktop` is in no release artefact. `make desktop` is how it is obtained, and
the documentation says so. Adding it to the Linux release archives means `install.sh`, the archive
layout, `coddy update` and the `.deb` / `.rpm` contents, each with its own failure mode; it is the
next change, not this one, and the macOS artefacts (the cask, the formula) are untouched because
the window is not ported there.

Two places therefore do move, and only to keep the surface covered by the gates that would
otherwise never compile it: `TEST_TAG_SETS` gains `desktop`, `http,desktop` (the combination that
serves the stub) and `http,ui,desktop`, `make lint` gains a pass over `http,ui,desktop`, and
`make test` runs the desktop packages plus the shell spec of `external/httpserver` after its main
run. `make check-windows` is deliberately not touched: the window is not ported there, so there is
nothing of this change for it to vet.

## Scope

Split at `desktop.native`, and the reason is reviewability rather than size.

**Part one**, this change: the binary, the shell under the catch-all, the browser backend, the
remembered port, the tag wiring in the places that need it, the tests and the documentation in both
languages. Every line of it is pure Go or TypeScript, builds on every runner and is covered by the
existing matrix. The window is ported to Linux only (below).

**Part two** is `desktop.native`: the `go.mod` dependency, the cgo constraint partition, the `GOOS`
decision and whatever CI proves it. It puts a dependency in `go.sum` for every build, ships in no
artefact and is compiled by no job, so merged together part one could not be reverted without it and
a reviewer could not check the only half CI never builds.

**Linux only, by the operator's decision.** The launcher, the shell and the port are plain Go and
TypeScript and run anywhere; what is per-platform is finding and opening a browser, and signing the
result. On macOS a browser is a bundle under `/Applications` whose executable is inside it, on
Windows a path found through the registry, and a window on either wants a signed application before
Gatekeeper or SmartScreen opens it without a warning - none of which a cross-compile proves. So
`browser_linux.go` holds the candidate list, `browser_unported.go` (`desktop && (!linux || android)`)
holds none, and a run there reports that it has no window to wait on and serves with its address
printed. `CODDY_DESKTOP_BROWSER` still opens a window on any platform, which is the honest escape
hatch. Android satisfies the `linux` constraint and is excluded by name: Termux has no browser to
start this way.

## Files

Part one, as built:

```
external/desktop/                       //go:build desktop
  launch.go            Options, Run: the remembered port, readiness, the lifetime, the
                       second-launch handoff, and ApplyConfig
  backend.go           the window backends and the auto selection
  browser.go           app mode: the search, the argv, the profile and its one seeded
                       preference, waiting on the process
  browser_linux.go     the candidate list            //go:build desktop && linux && !android
  browser_unported.go  no candidates, and why        //go:build desktop && (!linux || android)
  native_stub.go       the native window is not built in; names the tag that carries it
  port.go              <home>/desktop/port, the probe, the readiness dial
external/desktop/shell/                 //go:build desktop
  shell.go             go:embed + Handler() + Prefix + MarkerHeader
  index.html  shell.css  shell.js
external/httpserver/
  spa_embed_ui.go      the /desktop/ prefix inside the "/" catch-all
  desktop_shell.go     //go:build http && ui && desktop      - the handler
  desktop_shell_stub.go //go:build http && !(ui && desktop)  - nil, so "/" behaves as today
cmd/coddy-desktop/
  main.go              //go:build desktop && http  - flags, config, the HTTP subsystem, Run
  main_stub.go         //go:build !(desktop && http)
external/ui/scripts/desktop-check.mjs   the browser check of the shell
```

`cmd/coddy/serve.go` is **not** touched. The plan's `serveRun` extraction was for a verb of
`coddy`; a separate binary cannot call into that main package anyway, and the HTTP subsystem it
needs is one descriptor it builds itself (`serveHTTP` in `cmd/coddy-desktop/main.go`) over the
exported `serve.Runtime`, `serve.Resolve` and `serve.NewSupervisor`. The duplication is one
descriptor; the alternative was extracting `subsystems()` and everything it closes over into a
package of its own, which is a refactor of `coddy serve` in a change about a window.

Tag lists: `Makefile` only (`DESKTOP_TAGS`, the `desktop` target, `TEST_TAG_SETS`, the `lint` and
`test` passes). `FULL_TAGS`, `Dockerfile`, the workflows, the Homebrew templates, `README.md`'s
build commands and `AGENTS.md` are untouched, because the shipped set did not change.

## Tests

Red first, in this order, and this is what was written.

- **`features/desktop_shell.feature`**, the page, driven by
  `external/httpserver/bdd_desktop_shell_test.go`: a server built with `http,ui,desktop` answers
  `GET /desktop/` with the tab strip and one frame **with a sign-in form configured**, which is the
  regression test for the gate, and marks itself with the header a launcher probes for, while the
  session list it draws its titles from stays behind that gate.
- **`features/desktop_window.feature`**, the launcher, driven by
  `external/desktop/bdd_desktop_window_test.go` (`package desktop_test`): the port it writes down
  and comes back to, the browser it opens on the shell of exactly that port with a profile of its
  own, the window closing ending the run, a second launch serving nothing of its own and opening
  the desktop already running, and `ApplyConfig` leaving only the HTTP surface and naming what it
  turned off.
  The browser is this test binary re-executed, as `internal/hooks/hooktest` does for hook processes.
  It cannot reproduce the Chromium profile singleton by construction, which the test file says in a
  comment so the next reader does not take the coverage for more than it is; the hand-over scenario
  asserts on the probe instead, and its fake exits as soon as it has recorded the window it was
  asked for, which is what the real browser does there.
- **Unit tests** beside the code: the profile folder's name, the window's argv, the one seeded
  preference and that an existing profile is never rewritten, the browser the caller or the
  environment names and the refusal of one that is not there, the unported platform falling through
  as `errNoBrowser` with the platform in the message, the port (fresh, reused, unreadable, taken by
  a stranger), the probe telling a desktop from any other server, `ApplyConfig` with everything on
  and with nothing on, an unknown `--window` value, the native window naming its tag, a run with no
  home or nothing to serve, and the no-window path printing the address and serving.
- **`npm run check:desktop`** (`external/ui/scripts/desktop-check.mjs`), in the shape of
  `check:transcript` and `check:files`: against the real binary with a scripted model, a chat in a
  tab and its title on that tab from `GET /coddy/sessions`, a second tab with both frames alive, a
  chord typed inside a chat reaching the strip, closing a tab, and **restarting the binary** to see
  the tabs come back on the same port - a reload cannot see what a moving port would have broken,
  only a relaunch can. The window is a script that sleeps, so the binary serves while the script
  drives the page; what the window is started with is the feature spec's business.
- `TEST_TAG_SETS` gains `desktop`, `http,desktop` (the combination that serves the stub) and
  `http,ui,desktop`. `desktop.native` is in no matrix: it needs system packages no runner has, and
  part two has to say what does cover it.

## Documentation

`docs/surfaces/desktop.md` and its `docs/ru/` translation, both in `nav.yaml`, with a screenshot of
the window with two tabs (Dark, 1280) taken by the browser check itself. A row in `README.md`'s
surfaces table, the `desktop` tag in the reference table of `docs/contributing/build.md` and a
section there on `make desktop`, in both languages. A **Desktop shell** section in `DESIGN.md` for
the tab strip, since it is a surface with a visual contract that is not the SPA.

The man page, the completions and `topLevelCommands` are **not** touched: there is no new `coddy`
subcommand, and `coddy-desktop` ships in no package yet. Both follow the change that puts it into
the release archives.

No `config.yaml` key in this change, so no schema, no `configure-coddy`, no site publish. The
window is started by a command with flags, and the one thing that has to survive a restart - the
port - is state under `<home>`, not configuration. When something else does (a remembered window
size, a default backend, open at login), that is a `desktop:` block and the schema chain with it.

## What is deliberately left out

- **Split screen.** Named by the operator as the next step, not this one. The shell keeps its tabs
  in one layout container so that change is CSS and a second visible frame.
- **A tray icon, a global hotkey, autostart.** None of them is what a tab is, and each is a
  platform API of its own.
- **A credential for `--remote` against a token-only server.** The ticket mechanism exists; adopting
  it is its own change.
- **Signing and notarisation.** They arrive with the first published native build, not before.
- **The window in the container image.** Deliberately absent, as above.
- **`frame-ancestors`.** Nothing in the tree sets `X-Frame-Options` or CSP `frame-ancestors` on the
  SPA today, so any page can already frame a loopback Coddy; this design neither creates nor worsens
  that, and tightening it is a separate decision that has to account for canvas responses
  deliberately carrying `frame-ancestors *` (`internal/canvas/serve.go`), since two CSP headers
  intersect rather than override.

## Open questions

1. **Which platforms the native window claims**, in part two. It can be written for all three, but
   only Linux can be proven here: cgo plus `webkit2gtk-4.1` is what the development machine has,
   the Windows and macOS CI jobs build without cgo, and `desktop.native` is excluded there by the
   `cgo` constraint. Linux-only in v1 is the honest reading; all three with the others marked
   untested is the useful one.
2. **`webview_go` in `go.mod` for every build**, in part two. ~500 lines of MIT bindings with no
   transitive dependencies, but it enters `go.sum` for everyone and `make security`'s govulncheck
   starts scanning something no CI job compiles. If that is unacceptable the native backend belongs
   in a module of its own. Wails v3 is the alternative and is worse here: a much larger dependency
   and its own build tool, and still no native tab strip, since each Wails window is one WebView.
3. **How many tabs before it hurts.** Each is an SPA instance. A number has to be measured and
   written on the page rather than guessed.

## What the cross-review changed

Four reviewers (Devin swe-2, NeuralDeep qwen3.8-flash-next-ru, Codex gpt-6.1-sol, Cursor auto) read
the first draft; the findings below were verified against the tree before being accepted.

- `GET /desktop/` as a registered route would be **behind the auth gate**, so with a sign-in form
  the window opened on a bare 401. The shell moved under the `"/"` catch-all.
- **The bound port could not be reported**: `OnServer` fires before the listener exists and
  `httpx.NewServer` never exposes one. Together with the `localStorage` loss, this replaced the
  ephemeral port with a remembered one, which removed the need for the readiness callback and the
  listener change entirely, and incidentally fixed the second-launch handoff.
- `coddy desktop` would have started **every subsystem `config.yaml` enables**, and one failed bind
  cancels the whole run.
- The **tag list is eleven places, not three**, and the Homebrew formula and `check-windows` were
  both missed.
- The `desktop.native` **constraint partition had a hole** at `CGO_ENABLED=0`, and the
  `gateway.telegram` precedent it cited is an either-or, not an addition.
- `auto`'s plain-browser rung has **no lifetime**, and the Android paragraph had it backwards.
- Keyboard chords **do not cross an iframe boundary**, and `Ctrl+W` in an app window is the
  browser's.
- The **scope is two pull requests**, split at `desktop.native`.

Rejected after checking: that the service worker could cache `/desktop/` (`public/sw.js` has no
fetch handler); that a missing `SharedWorker` in WebKit breaks the stream (there is a Web Locks
fallback); that a fresh iframe might ignore `#/s/<id>` (`initialRoute` is a boot snapshot); that the
SPA is served with frame-blocking headers (nothing sets them); and that two pipelines over one
`--home` corrupt state (the console, `coddy acp` and `coddy serve` already share one).

## What the operator's decisions changed

After the cross-review and before the code, two decisions narrowed the shape:

- **a binary of its own, `coddy-desktop`**, rather than a `coddy desktop` verb. It removed the
  `serveRun` extraction from `cmd/coddy/serve.go`, the man page and completions work, and every
  shipped-tag-list edit but the Makefile's, since `desktop` no longer has to be in `FULL_TAGS` for
  the surface to exist. It added one HTTP subsystem descriptor in the new main package, and a
  coverage gap that the `TEST_TAG_SETS` entries, the extra `lint` pass and the extra `test` lines
  close;
- **Linux first, the other platforms unported**, rather than one list per platform written blind.
  `browser_darwin.go` and `browser_windows.go` are not in the tree; `browser_unported.go` says what
  each of them will have to answer and why a cross-compile cannot prove it. The binary still builds
  everywhere and `CODDY_DESKTOP_BROWSER` still opens a window.

Both are reversible: the verb could be added over the same package later, and a platform is a file
beside `browser_linux.go` plus a test on a runner of that platform.

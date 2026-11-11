# Desktop window

`coddy desktop` opens Coddy as an application on your own machine: a window with a strip of tabs,
one chat per tab, over the same web interface `coddy serve` serves. It is a verb of the ordinary
binary, which the release builds carry, and it runs on Linux, macOS and Windows.

![The desktop window with two tabs, a chat in front](../assets/desktop/desktop-tabs-dark-1280.png)

*Two chats as tabs: the one in front is named after its conversation, `+` opens another.*

## What it is

The window holds the web interface you already know. Every tab is that interface, loaded again, so
a chat behind a tab keeps its transcript, its scroll position and its stream of events while you
work in another one. The agent, the sessions and the configuration are the same: the window is a
client of an ordinary HTTP surface, started on a loopback port of this machine.

What the window adds over opening a browser:

- it starts as a program, from a launcher or a terminal, with no address to type;
- it holds several conversations side by side as tabs, and comes back to them after a restart;
- it is a window of its own, with no address bar, bookmarks or other tabs around the chat;
- it keeps the agent's session cookie out of the browser you read mail in.

How the parts fit together, from the launch to the chat in the frame:

![How the desktop window works: the launcher, the page in the window and the chats in its frames](../assets/diagrams/desktop-window.png)

*One process serves the web interface on a remembered loopback port and opens a browser window on
it; the page in that window is a tab strip over one frame per chat, and it drives the window
through routes of its own.*

## Getting it

Every release binary carries the window, so an installed Coddy already has it:

```bash
coddy desktop
```

From a checkout, `make desktop` is `make build` with the shipped tag set:

```bash
make desktop        # build/coddy with tags: http ui scheduler memory cli gateway swarm metrics browser desktop
./build/coddy desktop
```

The surface costs 0.44 MiB in a binary that already carries the HTTP server and the web interface,
which is why it is a verb rather than a second program to install and update, and why every build
has it - the release archives, the packages, the Homebrew formula and the container image alike.

It needs a Chromium-family browser installed - Chrome, Chromium, Edge, Brave, Vivaldi or Yandex
Browser - which it starts in application mode. Firefox has no application mode and is not used.

### The Linux package

`coddy-desktop` is a package of its own beside `coddy`, and it carries no program: it installs the
menu entry (`/usr/share/applications/coddy-desktop.desktop`) and the icon it names, so Coddy
appears in the application launcher and starts from there with no terminal, and it depends on
`coddy`, whose `desktop` verb is the window. A server that only runs `coddy serve` installs the
agent alone.

```bash
make deb-desktop    # dist/coddy-desktop_<version>_linux_<arch>.deb
make rpm-desktop    # the same as an .rpm
```

Like the agent's package it enables nothing, creates no system account and writes nothing under
`/etc`; the window's state is the invoking user's `~/.coddy`, and **Open at login** writes into
that user's own autostart folder. A browser is a recommendation rather than a dependency - any of
the Chromium family does, and `CODDY_DESKTOP_BROWSER` names another.

## Running it

```
coddy desktop [--window auto|browser|native] [--browser PATH] [--frame] [--auth]
              [--config PATH] [--home DIR] [--cwd DIR] [--log-level LEVEL] [-t]
```

| Flag | What it does |
| --- | --- |
| `--window` | which window to open: `auto` (the default), `browser` for application mode, `native` for the window around the system WebView, which is not built in yet |
| `--browser` | the browser to open the window with, by name on `PATH` or by path. Also `CODDY_DESKTOP_BROWSER` |
| `--config`, `--home`, `--cwd` | as on `coddy serve`: the configuration file, the agent home, the workspace new chats open in |
| `--frame` | keep the system title bar and borders. Without it, on an X11 session, the strip is the title bar |
| `--log-level` | `debug`, `info`, `warn` or `error`, overriding the configuration |
| `--auth` | ask for the credential the configuration carries. Without it the window asks for nothing (below) |
| `-t`, `--test-config` | check the configuration file and exit, starting nothing |

The desktop runs the same site `coddy serve` runs. If a service for the same agent home is already
running, the window connects to that process over a private local channel and starts no second
session manager, scheduler or gateway. The service keeps running when the window closes. This also
works when the public HTTP listener is disabled. Restart an older background service that does not
yet offer the channel before opening the window.

Otherwise the desktop starts the surfaces the configuration enables - the public HTTP surface, the
Telegram and Pachca bots, the swarm relay, the scheduler, the exporter and the browser endpoint -
beside the window's loopback surface. A surface that cannot start stops alone and says so in the
log; the window and the rest keep running. The configuration on disk is not rewritten.

## Tabs

| Action | How |
| --- | --- |
| New tab | `+` after the tabs, or `Ctrl`+`T` |
| Close a tab | its `×`, a middle click on it, or `Ctrl`+`W` for the one in front |
| Switch | click a tab, or `Ctrl`+`1` ... `Ctrl`+`9` |
| Next, previous | `Ctrl`+`Tab` and `Ctrl`+`Shift`+`Tab`, which wrap at both ends |
| Reorder | drag a tab to its new place in the row |
| Reload the chat in front | `Ctrl`+`R`, which reloads that tab and leaves the others alone |
| Walk a long row | the two arrows beside the window's own buttons, which appear once the tabs are wider than the bar |

A middle click on **New chat**, a conversation in **History**, or a rail item such as **Settings**,
**Scheduler**, **Docs** or **Swarm** opens that route in a new tab of this window instead of another
browser window. `Ctrl`+click and `Cmd`+click do the same. A new chat opened this way starts in the
folder of the chat you were reading; before a chat has started, it uses the folder picked on the
start screen. The route of every tab is restored after a restart, including an open rail screen.
A new tab shows its start screen immediately while that folder is resolved. If a chat does not
finish loading, the window tries its address again twice before leaving it for `Ctrl`+`R`.

The chords work while you are typing in a chat, and some of them take a detour to get there. A
browser window in application mode answers `Ctrl`+`T`, `Ctrl`+`W`, `Ctrl`+`R` and `Ctrl`+`Tab`
itself - with a new browser window, by closing the application, by reloading the whole strip and by
walking its own tabs - and no page can cancel any of them. On an X11 session the launcher takes
those keys at the X server, with a grab on its own window, and hands them to the tab strip;
`Ctrl`+`Tab` and `Ctrl`+`Shift`+`Tab` are two grabs rather than one, because a grab matches the
modifiers exactly. `Ctrl`+`1` ... `Ctrl`+`9` need none of that, since an application window has no
tabs of its own to switch to and gives the digits to the page. `Alt` stands beside `Ctrl` for every
letter and digit chord, and on a Mac so does `Cmd`: that is what a Wayland or a macOS session -
where the keys cannot be taken from the browser - leaves you with. `Alt`+`Tab` and `Cmd`+`Tab`
always belong to the window manager and never switch these tabs. A chord is read off the physical
key, so a Russian or any other layout answers the same keys as a US one.

A row longer than the bar scrolls, and the tab you switch to is always brought into sight, so the
chords stay usable however many chats are open.

A tab is named after the conversation behind it, which the window reads from the session list. A
chat that has not started yet is `New chat`.

Ahead of the name each tab carries the state of its chat, the same mark the History list uses:

| Mark | What it means |
| --- | --- |
| A filled dot, pulsing | the chat is working: a turn is running, or a background task is still in flight |
| A question mark, named **Permission required** | the chat is waiting for a permission |
| A question mark, named **Question pending** | the chat is waiting for an answer |
| A red dot | the last turn of that chat failed |
| A filled dot in the accent | the chat finished while you were in another tab |
| A hollow ring | the chat is finished |

A turn sent in any tab marks that tab as working immediately; the ordinary session-list poll
then takes over and clears the mark when the work finishes.

The window keeps a tab: closing the last one opens an empty chat instead of leaving the window
blank. The strip's close button minimises the window when **Show the tray icon** is enabled and
the icon is present: the application and the surface behind it keep running, and clicking the icon
or picking **Show the window** brings the window back. With the tray off, or when the icon could
not be shown, the same button closes the application and stops the surface. **Quit Coddy** in the
tray's menu always ends the application, whichever state the switch is in. Turning **Show the tray
icon** off from the tray's own menu restores a minimised window before removing the icon, so the
restore path is not lost with it.

Each tab is a full instance of the web interface, so memory grows with the number of tabs about as
it would with that many browser tabs. The event streams do not multiply: every instance of one
origin shares a single connection.

## The window itself

On an X11 session the window opens without the system frame, and the tab strip is its title bar.
On macOS, on Windows and on Wayland the window keeps the frame its desktop draws, and the strip is
a strip of tabs: everything below is what an X11 session adds. The strip
carries, from left to right: the menu of the desktop, the tabs, `+`, the empty space that drags the
window, the two arrows that walk a row too long for the bar, and minimise, maximise and close at the
right edge. The corners are rounded while the window floats and square while it fills the screen.

| Action | How |
| --- | --- |
| Move the window | drag the empty part of the strip |
| Resize the window | drag an edge or a corner; the pointer shows the direction |
| Maximise or restore | double-click the empty part of the strip, or the middle button at the right |
| Minimise, close | the buttons at the right edge |
| The desktop menu | the button at the left edge of the strip |

A window the window manager has pinned - maximised, or tiled against an edge with `Meta`+`Left` -
is let loose when you start dragging the strip, and put back under the pointer, the way dragging a
title bar works everywhere. The keyboard instructions of your desktop apply to this window as they
do to any other.

The first window of a browser profile opens as a landscape rectangle, 1280 by 820, rather than the
tall one a Chromium application window takes by default. After that the browser remembers where you
left it, and Coddy does not override that: a window moved or resized comes back where it was.

### In the task bar

The window is Coddy's, and the desktop is told so twice over. It carries the
window class `coddy-desktop`, which is what a task bar matches against the
application entries it knows, so an installed Coddy shows its own icon and its
own menu there rather than the browser's - without it the class is derived
from the address the window opens, the task bar finds the browser's entry, and
a right click offers the browser's recent downloads and a new incognito
window. And it carries Coddy's mark as its own icon, at 48 and 128 pixels, for
a machine with no entry installed: a browser gives an application window the
page's favicon at 16 pixels and nothing larger, which a task bar then blurs up
to its own size.

Both are X11. On Wayland, on macOS and on Windows the window is the browser's
as far as the system is concerned, and the task bar draws what the browser
tells it.

### Recent folders in the application menu

On Linux every launch writes this user's own application entry,
`~/.local/share/applications/coddy-desktop.desktop`, naming the binary that
wrote it. That is what puts Coddy in the launcher and in the task bar's menu
without a package installed, and it carries the folders of this home's recent
chats as its actions - up to six, newest first. A right click on the icon
offers them, and picking one opens a new chat in that folder: the row runs
`coddy desktop --new-chat --cwd <folder>`, which hands the folder to the
window already open rather than starting a second one.

A row names the folder and the one above it (`coddy-agent/docs`), because that
is what tells two checkouts of one repository apart; the whole path is in the
command. The folder a row opens also becomes the one the next new chat starts
in, the same as picking it on the start screen.

The strip wears the theme the chats are in. It reads the colours off the chat in front, so all nine
themes of the web interface reach the bar, the tabs and the state dots, and a theme changed in
**Settings** reaches the strip without a reload.

![The same window in the Light theme: the strip, the tabs and their dots in the theme's colours](../assets/desktop/desktop-tabs-light-1280.png)

*The strip in the Light theme, the same window as above.*

## The desktop menu

The button at the left edge of the strip opens the window's own settings. They are the machine's,
not the page's, and live in `<home>/desktop/settings.json`, so they survive a launch that had to
take another port.

![The desktop menu open under the button at the left edge of the strip](../assets/desktop/desktop-menu-dark-1280.png)

*The menu of the window, opened from the left edge of the strip.*

| Entry | What it does |
| --- | --- |
| **New chat** | the same as `+` |
| **Open at login** | Linux: writes `coddy-desktop.desktop` into this user's autostart folder (`$XDG_CONFIG_HOME/autostart`, else `~/.config/autostart`). macOS: a launch agent, `~/Library/LaunchAgents/dev.coddy.desktop.plist`. Windows: a value under this user's `Run` key. Nothing is installed system-wide and nothing needs administrator rights |
| **Show the tray icon** | publishes Coddy's mark in the system tray as a StatusNotifierItem on the session bus. A click on it brings the window back from behind another window or from the taskbar |
| **Start minimised** | the next launch opens the window minimised, which is usually what a window opened at login wants |
| **Show the system frame** | gives the window back the title bar and borders your desktop draws. The strip then stops drawing minimise, maximise and close, since the frame carries them. An X11 session only: elsewhere the frame is always there |

A switch moves only once the launcher has done the thing: a tray this session has no tray for, or
an autostart folder that cannot be written, leaves the switch where it was and says why on the row.

The tray icon carries the same menu. A right click on it opens the rows above, with two more on
top - **Show the window**, which brings it back from behind another window or from the taskbar, and
**Hide the window**, which puts it away again - and **Quit Coddy** at the bottom, behind a line of
its own: with the window hidden in the tray, the icon is the only way out of the application. The
menu in the strip has no such row, because the window's own close button is an arm's length from
it. The two menus are drawn from one list on the Go side
(`shell.MenuRows`), so a row added to the strip's menu appears on the icon as well; the icon speaks
`com.canonical.dbusmenu`, which is the menu protocol a StatusNotifierItem names and every current
tray draws. **New chat** picked from the icon brings the window back first, since a chat opened
behind a hidden window is one nobody asked for.

Where a window cannot be driven at all - a Wayland session, where a program may not move or
undecorate a window it does not own - the window keeps the system frame and the strip draws no
buttons of its own. Everything else is the same.

## Links out of Coddy

A link to another site - the documentation, an issue, whatever the model wrote - opens in the
browser you actually use, through your desktop's own opener. Clicked inside a tab it would
otherwise load that site into the frame, where it would sit under a tab strip with no address bar,
no back button and no way out but closing the tab. Coddy's own addresses are not links out and
stay in the window.

Only `http` and `https` are handed over. The window refuses every other scheme, because the opener
passes what it is given to whichever application claims it and a link in a chat is text a model
wrote.

## Notifications

The window raises the system notifications the web interface raises. Turn them on in **Settings -
Appearance - Notifications**, and a chat that ends a turn, asks for a permission or asks a question
while no window of Coddy has your attention puts a notice on your desktop; clicking it brings that
chat up. The rules are the web interface's own, described under
[Notifications](web-ui.md#notifications).

What the window adds is that it does not ask. A fresh browser profile is told the answer before its
first window, so turning the switch on does not open the browser's permission bubble over the tab
strip. The grant names this window's address and nothing else, and it holds because the port does.
A launch that had to take another port gets a grant that no longer matches, and the browser asks
once.

## The port it remembers

The first launch takes a free loopback port and writes it to `<home>/desktop/port`; every later
launch serves on the same one. The port is what a browser scopes a page's stored state to, so
keeping it is what lets the window come back to the tabs you left open, and the first-run tour stay
seen.

Two consequences:

- if something else has taken that port, the launch takes another one and says so. That launch
  starts with no tabs, and the new port is what the next one uses;
- starting the application while a window is already open does not serve a second time. The second
  launch sees the desktop already running and asks it for a window.

The browser's profile for the window lives in `<home>/desktop/profiles/<browser>`, one per browser.
It is what makes the window a process of its own, and what keeps the agent's cookie out of your
everyday profile. It costs the disk a fresh browser profile costs, a few tens of megabytes.

## Signing in

The window asks for nothing. It binds loopback for the person who started the application, and a
sign-in form in a window they opened themselves proves nothing, so `coddy desktop` serves without a
credential whatever the configuration carries - a bearer token, a sign-in account, both.

**The configuration on disk is untouched.** The same `config.yaml` still signs in the public
surface this process and `coddy serve` run, and the settings screen inside the window shows the
real values, so saving from there does not take the operator's account off disk.

Two things follow. Any program running on this machine can reach that port and drive the agent,
which is the same bargain `coddy serve` makes on loopback by default. And if you want the
credential back - a shared machine, a second user account - `--auth` asks for whatever the
configuration configures, and the sign-in form appears inside the window.

Every other surface the configuration enables runs beside the window, as above, and that too is a
decision of this process rather than an edit: the document stays as written, and the credential the
public surface asks of a visitor from elsewhere is the one it holds.

A remote environment sees the desktop window as a separate browser origin,
`http://127.0.0.1:<port>`, where the port is recorded in `<home>/desktop/port`. On the remote
server, enable `httpserver.cors` and list that exact origin in `httpserver.cors.allowed_origins`.
For a swarm relay, configure `swarm.cors` on the relay instead. The alternative
`allow_loopback: true` admits every loopback page in the browser, not only Coddy's window; use it
only when that broader trust is intended, and protect the remote API with a credential. Starting
the desktop does not change a remote server's CORS policy.

## Linux, macOS and Windows

The window opens on all three. What differs is how much of it the system lets Coddy drive:

| | Linux (X11) | Linux (Wayland) | macOS | Windows |
| --- | --- | --- | --- | --- |
| The window, tabs, settings | yes | yes | yes | yes |
| No system frame, the strip as the title bar | yes | no | no | no |
| Rounded corners, drag by the strip, its own buttons | yes | no | no | no |
| `Ctrl`+`T`, `Ctrl`+`W`, `Ctrl`+`R`, `Ctrl`+`Tab` taken from the browser | yes | no | no | no |
| `Alt` (and `Cmd` on a Mac) for the letter and digit chords | yes | yes | yes | yes |
| Tray icon | yes | yes | no | no |
| Open at login | autostart entry | autostart entry | launch agent | `Run` key |

Where a chord cannot be taken from the browser, `Alt`+`T`, `Alt`+`W`, `Alt`+`R` and the digit
chords do the same thing, and on a Mac `Cmd` stands beside them. `Alt`+`Tab` and `Cmd`+`Tab` stay
with the window manager.

The browser is looked for where each system keeps one: on `PATH` on Linux, in `/Applications` and
`~/Applications` on macOS, under Program Files and the user's local application data on Windows.
`CODDY_DESKTOP_BROWSER` names one by path for a machine that keeps its browser somewhere else:

```bash
CODDY_DESKTOP_BROWSER="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" coddy desktop
```

Two things to know before you use it on macOS or Windows. Neither Gatekeeper nor SmartScreen has
anything to warn about here, because the program you start is the browser already installed on the
machine - but `coddy` itself is a console program, so on Windows the console window it was started
from stays behind the window. And without a window of its own to drive, the strip draws no window
buttons and the menu offers no frame switch: your desktop's own title bar carries both.

If no Chromium-family browser is found at all, the launch prints the address and serves until you
stop it, so the interface is still reachable in an ordinary browser.

## See also

- [Web UI](web-ui.md) - the interface the window holds
- [coddy serve and the daemon](../operate/serve.md) - the long-running process for everything else
- [Building from source](../contributing/build.md) - build tags and what each one carries

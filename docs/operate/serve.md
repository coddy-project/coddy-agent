# Running Coddy as a daemon

`coddy serve` runs every subsystem the configuration enables - the HTTP API and the
embedded web UI, the messenger gateway, the swarm relay, the cron scheduler - in one
process over one session manager. Which of them start is decided by `config.yaml`; see
the [configuration reference](../reference/config.md) and the per-surface guides
([HTTP API](../reference/http-api.md), [gateway](../surfaces/gateway.md), [swarm](swarm.md),
[scheduler](scheduler.md)).

The subsystems share more than the manager. A background task the agent started wakes it
when it ends (`notify_on_finish`, on by default) whichever subsystems run: the process owns the
waker, and hands each woken turn to the Telegram chat bound to the session, else to the
HTTP server, else runs it through the manager itself
([Background tasks](../features/background-tasks.md#under-coddy-serve)).

This page is about keeping that process running and keeping it current.

## In the foreground

```bash
coddy serve
```

The process holds the terminal, prints what it started, and stops on Ctrl-C. This is the
form to use under `systemd`, `supervisord`, Docker, or anything else that already owns
process lifetimes - those supervisors restart the process themselves, and stacking a
second one under them only hides failures from the first. On Linux, `coddy serve install`
sets up that systemd unit for you, the packaged one or one it writes (next section).

## As a systemd user service on Linux

```bash
coddy serve install                      # once, as the user the service is for
journalctl --user -u coddy.service -f    # its log
coddy serve uninstall                    # take it away again
```

On Linux with systemd, `coddy serve` can run as a *user* service: it belongs to one
account, runs with that account's permissions and its `~/.coddy`, comes back after a
crash and, with [lingering](#after-logout-and-at-boot), keeps running after logout and
starts at boot. `coddy serve install` puts it in place and `coddy serve uninstall` takes it
away. Both run as the user the service is for, never under `sudo`.

On macOS and Windows there is no systemd, and both commands say so; `coddy serve --daemon`
is the route there. On Linux, what `install` has to do depends on how Coddy was installed:

| Installed with | The unit | What `install` does with it |
|---|---|---|
| the `.deb` or `.rpm` | `/usr/lib/systemd/user/coddy.service`, installed and **not enabled** | enables it for your account |
| the install script, a release archive, Homebrew on Linux, a local build | none | writes `~/.config/systemd/user/coddy.service` for the binary you ran `install` with, then enables it |

The package enables the unit for nobody: which accounts on a machine run a server is
for each of them to decide, and the message printed at installation says so. The
install script installs no unit and ends by pointing at `coddy serve install` the same
way.

Either way, `install`:

1. checks `~/.coddy/config.yaml` the way [`--test-config`](../reference/cli.md) does, and
   stops on an error;
2. refuses while a `coddy serve --daemon` runs for the same home, because both would bind
   the same port;
3. puts the unit in place (above), plus a drop-in
   `~/.config/systemd/user/coddy.service.d/coddy-install.conf` with the `PATH` of the shell
   you ran it from (a shell with no `PATH` keeps the drop-in an earlier run wrote);
4. creates `~/Coddy`;
5. runs `systemctl --user daemon-reload`, `enable` and `restart`, waits two seconds, and
   reports whether the service stayed up, with the command that shows its log.

Running `install` again is safe, and it is how the service picks up a change: after
`coddy update` or a package upgrade, after the binary moved, after your `PATH` changed.
It rewrites what it wrote, restarts the service, and never overwrites a unit or a drop-in
it did not write.

### What the service runs with

- **Working directory `~/Coddy`.** The working directory of `coddy serve` is the
  workspace of every session opened without one: a web UI chat started before a folder
  was picked, a scheduler job, a Telegram conversation. The service gets a folder of its
  own, so what the agent writes lands there and not next to the configuration and the
  provider keys in `~/.coddy`. The unit creates the folder when it is missing. A session
  opened on a project folder works in that folder as usual.
- **The agent home `~/.coddy`.** The service reads `~/.coddy/config.yaml` and
  `~/.coddy/.env`. The user manager does not inherit your shell's environment, so a
  `CODDY_HOME` exported in `.bashrc` does not reach it; `install` says so when it sees one.
- **The `PATH` of your shell.** A user manager starts services with a bare system
  `PATH`, and an agent that cannot find the `go`, `node` or `python` your terminal finds
  fails at the first build. The drop-in hands the service the absolute entries of the
  `PATH` `install` ran with.
- **The journal.** Output goes to `journalctl --user -u coddy.service`, and to a file as
  well when `logger.file` says so.
- **Restarts.** `Restart=on-failure` brings back a process that crashed. A configuration
  change that moves a listen address - the web UI port, changed from its own settings
  screen - ends the process with status 75, and systemd starts it again on the new
  address: the unit sets `CODDY_SERVE_ROLE=service`, which tells `coddy serve` that
  something will ([Restarting itself](#restarting-itself)).
- **Stopping.** systemd gives `coddy serve` 40 seconds after `SIGTERM` to finish a turn
  in flight, then kills it.

Anything else - another working directory, more environment, a flag - goes into a
drop-in of your own, which `install` and `uninstall` leave alone:

```bash
systemctl --user edit coddy.service
```

### After logout and at boot

A user manager runs while its user has a session, so the service stops when you log out
of the last one and starts again at the next login. To keep it running without a session
and start it at boot, an administrator enables lingering for the account once:

```bash
sudo loginctl enable-linger <user>
```

`install` prints that line when lingering is off. Coddy never changes it.

### Controlling the service

systemd owns the service. The `coddy serve status | stop | restart` verbs belong to
`--daemon`, and when they find the systemd service instead they say so:

```bash
systemctl --user status coddy.service
systemctl --user restart coddy.service
systemctl --user stop coddy.service       # until the next login, or the next boot
```

Do not run `coddy serve --daemon` for the same account next to it: both would bind the
same port. `install` refuses while the daemon runs, and `--daemon` over `~/.coddy` refuses
while the service runs (over another home it only warns, since that one may listen
elsewhere). The daemon verbs also mention a service that is enabled but stopped, which
comes back at the next login.

`install` and `uninstall` talk to the user manager of the account over its bus. A shell
opened with `su` or `sudo -u` keeps the session of the caller, so the manager is not
reachable from it and both commands say so; log in as the user (ssh, a desktop session,
`machinectl shell <user>@`) instead.

### Removing the service

```bash
coddy serve uninstall
```

stops and disables `coddy.service`, removes the unit and the drop-in `install` wrote, and
reloads the user manager. The packaged unit stays where the package put it, disabled. A
unit or a drop-in you wrote yourself is disabled and kept. `~/.coddy` (configuration,
sessions) and `~/Coddy` (workspace) are not touched.

Run it before removing the `.deb` or `.rpm`. The package removal takes the unit file away
but cannot reach into each account's `~/.config`, so an account that enabled the service
keeps it enabled, and the service keeps running the deleted binary until it stops.
`coddy serve uninstall` from another installation of Coddy still clears it; without one,
the removal prints what does, to run as that user:

```bash
systemctl --user disable --now coddy.service
rm -f ~/.config/systemd/user/coddy.service.d/coddy-install.conf
```

## In the background

```bash
coddy serve --daemon      # or -d
```

The command detaches and returns. What it leaves behind is a **dispatcher**: a process
that runs the subsystems in a **worker** process and starts a new worker whenever that
one goes away for any reason other than being told to stop. A panic that escaped a
surface, an out-of-memory kill, a listener that died with the network - all of them end
the same way, with a fresh process built from the configuration and the binary that are
on disk right now.

```
coddy serve 1.0.19 is running in the background
  pid     4711
  config  /home/you/.coddy/config.yaml
  log     /home/you/.coddy/logs/serve.log
```

The command waits for the first worker before it returns, so a configuration that cannot
start - a port somebody else holds, a subsystem this binary was not built with - is
reported in the terminal that typed the command rather than only in a log nobody is
tailing yet. The dispatcher keeps retrying in that case; it is up, and it says so.

State lives under the agent home:

| Path | What it is |
|------|------------|
| `~/.coddy/serve.json` | the dispatcher's record: pid, version, config, log, the arguments it was started with, and the worker it currently has |
| `~/.coddy/logs/serve.log` | everything both halves write, appended across restarts |

### Restart pacing

A worker that fails is restarted after **1 s**, then 2, 4, 8, up to **30 s**. The wait
goes back to 1 s once a worker has stayed up for a **minute**, so a bad hour last week
does not slow down a recovery today.

The dispatcher never gives up. A dependency that is down for a day is retried every 30
seconds until it comes back; something that needs a person will still be broken when
that person looks, with the reason in the log.

### Controlling it

```bash
coddy serve status
coddy serve stop
coddy serve restart
```

All three take `--home DIR` (or `CODDY_HOME`) to name which installation they are about.

`status` answers two different questions, because a dispatcher that is up says nothing
about whether anything is being served:

```
coddy serve 1.0.19 is running
  pid     4711
  since   2026-09-10T11:26:03+03:00
  config  /home/you/.coddy/config.yaml
  log     /home/you/.coddy/logs/serve.log
  args    -H=0.0.0.0 -swarm=true
  worker  4713
```

`restart` brings the daemon back with the arguments the record kept, so a daemon started
with `-H 0.0.0.0 --swarm` comes back as that daemon rather than as a default one. They
are recorded in their canonical `-name=value` form, which is why `status` shows them
that way rather than as they were typed.

`stop` stops the worker first and gives it up to 30 seconds to finish what it is doing,
so a turn that is still generating is not cut off mid-sentence.

## Checking the configuration first

```bash
coddy serve -t            # or --test-config; --home and --config pick the file as for a start
```

The flag checks the file this command would load against the published JSON Schema and the
loader's rules, prints every problem with its line and how to fix it, and exits with status 1
when there are errors. Nothing starts and nothing is written, so it belongs in a deploy script
right before `coddy serve restart`. The report format and what counts as a warning are
described in [config.md](../getting-started/configuration.md#checking-the-file-from-the-command-line).

```bash
coddy serve --dry-run     # the same check, then probe what the file points at
```

`--dry-run` runs that check and then, when the file is clean, probes the world it describes:
the subsystems the configuration and the typed flags enable (a surface this binary was not
built with is reported, not started), the listen addresses they would bind (a port another
process holds is named together with the line that set it), every provider's model list,
the configured models against it, the executables of stdio MCP servers, the Telegram bot
token against the Bot API, the relays in `swarm.join` and the upstreams a relay mounts, and
the directories and files the configuration names. Exit status 1 when a probe fails. Alone
it prints only the problems and one status line; `coddy serve --dry-run --test-config` prints
the config check report and every probe. The report and its rules are described in
[config.md](../getting-started/configuration.md#dry-run-probing-what-the-file-points-at).

## Picking up a configuration change

The running process watches the file it loaded. A change made **outside** it lands the
same way a save from the settings screen does:

- `coddy providers login neuraldeep` in another terminal adds the provider and its
  models, and the model picker in every open browser has them within a couple of
  seconds;
- an operator edits `config.yaml` by hand;
- a deployment drops a new file in.

What happens next depends on what moved:

| Change | Effect |
|--------|--------|
| models, providers, skills, permissions, most settings | the live configuration is swapped; `GET /coddy/events` carries `config_reloaded` and open clients re-read (see [the SPA notes](../surfaces/web-ui.md)) |
| the Telegram token, the scheduler's directory or timeout | that subsystem alone is rebuilt in place |
| a subsystem's `enable` | it is started or stopped |
| a listen address (`httpserver.host` / `port`, `swarm.host` / `port`) | under a dispatcher the process restarts on the new address; in the foreground it is logged as needing a restart |

Everything else a surface reads once when it is constructed - the relay's own
credentials and TLS, the `swarm.join` registrations - still needs a restart you ask for,
`coddy serve restart` or Ctrl-C and up again. Only the address is picked up on its own,
because it is the one an operator changes from the screen that the address is serving.

A file that is unparsable, or gone for a moment while an editor writes it, leaves the
running configuration alone and is reported in the log. Comments and key order are not
settings, so an edit that only moves those changes nothing and announces nothing.

Flags outrank the file, on a reload as much as at startup. A daemon started with
`--gateway` or `-H 0.0.0.0` keeps them when somebody else saves an unrelated setting.

### Restarting itself

A listen address is the one change no running process can adopt: the listener is what
the caller is talking through, and moving it under them would drop the request that
asked for the move. With a dispatcher behind it the worker exits asking to be replaced,
and the replacement binds the new address - which is how an operator moves the port of
the very server whose settings screen they are typing into.

The exit status for that request is **75** (`EX_TEMPFAIL`), so a supervisor that knows
nothing about Coddy reads it the way it was meant: this run is over, another one is
worth starting. A foreground `coddy serve` only exits for it when it is told something
will start it again, through `CODDY_SERVE_ROLE=service` in its environment; otherwise it
logs that a restart is due and keeps the old listener. The unit `coddy serve install` uses
sets that variable, plus `SuccessExitStatus=75` and `RestartForceExitStatus=75`, so the
restart is not recorded as a failure. A unit or a supervisor of your own needs the
variable too.

## Which form to use

| Situation | Form |
|-----------|------|
| Linux with systemd, a server that should outlive your login | `coddy serve install` (a systemd user service) |
| a laptop, a dev box, a shell on a machine without systemd | `coddy serve --daemon` |
| `supervisord`, `runit` | `coddy serve` in the foreground with `CODDY_SERVE_ROLE=service`, and let them restart it |
| Docker, Kubernetes | `coddy serve` in the foreground as PID 1; the orchestrator restarts the container |

The packages ship a user unit that they do not enable, and no system unit: Coddy's state
is per-user under `~/.coddy`, so a system daemon would need a home and a configuration
nobody can edit. See [installation](../getting-started/install.md).

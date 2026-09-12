# Running Coddy as a daemon

`coddy serve` runs every subsystem the configuration enables - the HTTP API and the
embedded web UI, the messenger gateway, the swarm relay, the cron scheduler - in one
process over one session manager. Which of them start is decided by `config.yaml`; see
the [configuration reference](../reference/config.md) and the per-surface guides
([HTTP API](../reference/http-api.md), [gateway](../surfaces/gateway.md), [swarm](swarm.md),
[scheduler](scheduler.md)).

This page is about keeping that process running and keeping it current.

## In the foreground

```bash
coddy serve
```

The process holds the terminal, prints what it started, and stops on Ctrl-C. This is the
form to use under `systemd`, `supervisord`, Docker, or anything else that already owns
process lifetimes - those supervisors restart the process themselves, and stacking a
second one under them only hides failures from the first.

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
worth starting. Under `systemd` that is `RestartForceExitStatus=75` alongside the
ordinary `Restart=on-failure`.

## Which form to use

| Situation | Form |
|-----------|------|
| a laptop, a dev box, a shell on a server | `coddy serve --daemon` |
| `systemd`, `supervisord`, `runit` | `coddy serve` in the foreground, and let them restart it |
| Docker, Kubernetes | `coddy serve` in the foreground as PID 1; the orchestrator restarts the container |

The packages ship no service unit on purpose: Coddy's state is per-user under
`~/.coddy`, so a system daemon would need a home and a configuration nobody can edit.
See [installation](../getting-started/install.md).

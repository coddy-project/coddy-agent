# Working with remote nodes

**Goal.** Drive a node that sits behind a relay, from the console, from an editor and from the browser, the way you drive a local `coddy serve`: prompts, permission prompts, sessions, the model picker. And know what stays on the node, what travels, and which credential opens what.

**Environment.** A relay with at least one node, for example the stand of [A relay and its nodes in Docker](swarm-relay-and-nodes.md) at `http://127.0.0.1:12346` with the client token in `$T`. The general remote guide is [Remote mode](../operate/remote.md); this page is the swarm-specific part of it.

## 1. What a mount is

The relay mounts every node under `/swarm/nodes/<name>/`, and a mount carries the node's whole API: `/v1/*`, `/coddy/*`, the read-only swarm routes, and further `/swarm/nodes/*` hops. It is a plain base URL with a path prefix, which is exactly what `--remote` accepts, so every client that already speaks to a `coddy serve` speaks to a node through the relay without changes. The relay authenticates to the node on your behalf with the node's own token; you hold the relay's client token only.

## 2. The console

```bash
export T=change-me-client
coddy --remote http://127.0.0.1:12346/swarm/nodes/node-a --remote-token "$T"
```

The startup banner says `remote: <url>`; the session, its working directory and the model catalog are `node-a`'s. `-p` for a one-shot answer, `-c` to continue that node's latest session, and `/resume` operate on the node's session list:

```bash
coddy -p "List the files in the workspace." --remote http://127.0.0.1:12346/swarm/nodes/node-a --remote-token "$T"
coddy -c --remote http://127.0.0.1:12346/swarm/nodes/node-a --remote-token "$T"
```

A node behind a second relay is one longer path, nothing else: `--remote http://127.0.0.1:12346/swarm/nodes/inner/swarm/nodes/node-c` ([A chain of relays](swarm-multi-hop.md)).

Before typing anything, the dry run tells whether the path is right and the node answers:

```bash
coddy --dry-run --remote http://127.0.0.1:12346/swarm/nodes/node-a --remote-token "$T"
```

The token can also come from `CODDY_REMOTE_TOKEN`, and a relay address you use often can be named once in your own `config.yaml` under `httpserver.remotes` and then addressed by name; the token is never stored in that file ([Naming a remote](../operate/remote.md#naming-a-remote)).

## 3. An editor

`coddy acp` takes the same two flags, so Zed, VS Code, Obsidian or a script drive the node from the editor's composer, with the node's modes, models, permission prompts and skills:

```bash
coddy acp --remote http://127.0.0.1:12346/swarm/nodes/node-a --remote-token "$T"
```

Configure the editor to run that command line instead of a bare `coddy acp` ([Editors](../surfaces/editors.md)).

## 4. The browser

Open the relay itself, `http://127.0.0.1:12346/`. A relay built with the `ui` tag (the published image is) serves the web UI at its own address; it asks for the client token and opens on the swarm map. Click a node: the History drawer lists that node's sessions, the composer shows its working directory and models, and a turn started there runs on the node. The **Swarm** entry stays in the rail while you are inside a node, so another node is one click away, and each node on the map says how many sessions it holds, how many turns are running and whether one of them is waiting for you.

From a web UI served by some other `coddy serve`, the relay is one more environment: the chip in the composer, **+ Add remote...**, the relay's address and the client token. The token stays in that browser.

![A permission prompt relayed through two relays](../assets/swarm/permission-two-relays-dark-1280.png)

*A permission prompt from a node behind two relays, answered in the browser*

## 5. What runs where

The model calls, the tools, the workspace, the MCP servers, the hooks and the subagents run on the node, with the node's providers and keys. The client renders the transcript and answers the prompts. A permission or a question the node raises travels back through every hop to the client that started the turn and is answered there, in the console, the editor or the browser alike.

Trust is decided on the node. A project MCP server, a hooks file or a subagent definition that arrives with a checkout on the node is approved on that host, with the CLI there (`coddy hooks trust ...`) or over the mount with the token (`POST /swarm/nodes/node-a/coddy/hooks/trust`); the local subcommands do not take `--remote` ([Security and trust](../operate/security.md)).

## 6. Credentials and encryption

One credential opens the fleet: whoever holds the relay's client token controls every node the relay reaches, transitively through every hop. There are no per-node client permissions in this version, so a client token is a fleet-wide door and is handled as one: minted per relay, rotated by restarting the relay with a new value, and never sent over plain HTTP outside a private network.

The relay speaks TLS when `swarm.tls` names a certificate and a key ([Encryption and proxies](../operate/swarm.md#encryption-and-proxies)); a node never needs TLS of its own, because it is reached only through its tunnel to the relay. Without TLS, reach the relay through an SSH tunnel and keep the address on loopback:

```bash
ssh -N -L 12346:127.0.0.1:12346 relay-host      # in one terminal
coddy --remote http://127.0.0.1:12346/swarm/nodes/node-a --remote-token "$T"
```

## What tends to go wrong

- **`--remote` is refused before connecting.** The target must be a bare origin plus a path prefix: no query string, no fragment, no credentials in the URL. The token goes into `--remote-token` or `CODDY_REMOTE_TOKEN`.
- **`401` on every request.** A node's own token was used where the relay's client token belongs. Through a mount you present the relay's token; the relay presents the node's.
- **The status dot in a browser stays red with a correct token.** The web UI was opened from another `coddy serve`, and that relay does not list the page's origin in `swarm.cors.allowed_origins`. Open the relay's own address instead, or allow the origin.
- **`/permissions` and `--permission-mode` are rejected.** The permission mode is the node's; a remote client cannot change it. `/mode` still picks agent, plan or ask per turn.
- **The transcript stops mid-turn after the laptop lost the network.** The turn keeps running on the node; `/resume` or `-c` shows the outcome once it ends, and an answer to a prompt the node has already withdrawn is ignored.
- **A second client gets `409` on the same session.** A session is one turn at a time; the first client holds the lock until its turn ends.

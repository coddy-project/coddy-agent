# A chain of relays

**Goal.** A second relay behind the first, with its own nodes, so that one client attached to the outer relay reaches and lists everything: the swarm as a tree, or a ring, of relays. This is how a fleet spread across networks is joined into one, each relay knowing only its direct children.

**Environment.** The stand of [A relay and its nodes in Docker](swarm-relay-and-nodes.md), running. Two services are added to it. The mechanics are in [Rings and routes](../operate/swarm.md#rings-and-routes).

## 1. The idea

A relay is a node from its parent's point of view. A process that runs a relay (`swarm.enable`) and also lists a parent in `swarm.join` registers into that parent exactly the way an agent does, with `kind: relay`. That is all chaining requires: no second mechanism, and no hop that knows how deep the chain goes.

## 2. The files

`inner.yaml`, a relay that joins the outer one. The `token` in its join entry is its own client token, because that is what the outer relay presents when it proxies a request into it:

```yaml
httpserver:
  enable: false

swarm:
  enable: true
  host: "0.0.0.0"
  port: 12346
  name: "inner"
  auth_token: "${INNER_CLIENT_TOKEN}"
  pairing_tokens: ["${SWARM_PAIRING_TOKEN}"]
  join:
    - url: "http://relay:12346"
      name: "inner"
      pairing_token: "${SWARM_PAIRING_TOKEN}"
      token: "${INNER_CLIENT_TOKEN}"
```

Two lines in `.env`:

```bash
INNER_CLIENT_TOKEN=change-me-inner
NODE_C_TOKEN=change-me-node-c
```

Two services in `docker-compose.yml`, next to the existing ones. `node-c` is an ordinary node whose relay is `inner`:

```yaml
  inner:
    image: ghcr.io/coddy-project/coddy-agent:latest
    command: serve
    environment:
      CODDY_HOME: /home/user/.coddy
      CODDY_CONFIG: /home/user/.coddy/config.yaml
      INNER_CLIENT_TOKEN: ${INNER_CLIENT_TOKEN}
      SWARM_PAIRING_TOKEN: ${SWARM_PAIRING_TOKEN}
    volumes:
      - ./inner.yaml:/home/user/.coddy/config.yaml:ro
      - inner_home:/home/user/.coddy
    depends_on: [relay]

  node-c:
    <<: *node
    environment:
      CODDY_HOME: /home/user/.coddy
      CODDY_CONFIG: /home/user/.coddy/config.yaml
      CODDY_CWD: /workspace
      RELAY_URL: http://inner:12346
      NODE_NAME: node-c
      NODE_TOKEN: ${NODE_C_TOKEN}
      SWARM_PAIRING_TOKEN: ${SWARM_PAIRING_TOKEN}
      OPENAI_API_KEY: ${OPENAI_API_KEY-}
    volumes:
      - ./node.yaml:/home/user/.coddy/config.yaml:ro
      - ./workspace/node-c:/workspace
      - node_c_home:/home/user/.coddy
    depends_on: [inner]
```

and two more named volumes, `inner_home` and `node_c_home`. The inner relay publishes no port: it is reached through the outer one.

## 3. Start and check

```bash
mkdir -p workspace/node-c
docker compose up -d
docker compose logs inner
```

The inner relay's log shows it listening, then `swarm: joining relay` towards `relay`, then `node-c` registering into it. From the outer relay, with the outer client token:

```bash
curl -s -H "Authorization: Bearer $T" http://127.0.0.1:12346/swarm/nodes
curl -s -H "Authorization: Bearer $T" http://127.0.0.1:12346/swarm/topology
```

The node list of the outer relay names `inner` with `"kind": "relay"`; the topology also carries `node-c` behind it, with a route of two hops, because a topology walk asks each child relay for its own children.

A mount composes by writing the path out, and each hop strips its own prefix:

```bash
curl -s -H "Authorization: Bearer $T" \
  http://127.0.0.1:12346/swarm/nodes/inner/swarm/nodes/node-c/v1/models
coddy -p "Say hello in one line." \
  --remote http://127.0.0.1:12346/swarm/nodes/inner/swarm/nodes/node-c --remote-token "$T"
```

The prompt answers from `node-c`, through two relays, with the outer relay's token only: the outer relay authenticates to `inner` with the token from `inner.yaml`, and `inner` to `node-c` with that node's own.

The aggregated list recurses the same way. A child relay is asked for its own aggregate rather than its node list, so a session on `node-c` shows up on the outer relay with `"node_path": ["inner", "node-c"]`:

```bash
curl -s -H "Authorization: Bearer $T" 'http://127.0.0.1:12346/swarm/sessions'
```

In the outer relay's web UI the map draws the inner relay as a box with `node-c` under it; entering `node-c` drives it through both hops.

## 4. A ring

A node, or a relay, may join two parents. Add a second entry to `swarm.join` and it registers into both; the outer relay then knows two ways to it. That is a ring, and it is allowed: routes come from a breadth-first walk, so the shortest one is reported and the others appear as `alternates` on the node's topology entry, which is where a client fails over when the short hop dies. A request carries the ids of the relays it passed through, so a walk around a ring terminates instead of spinning.

## What tends to go wrong

- **The inner relay registers, but a request through `/swarm/nodes/inner/...` answers 401.** The `token` in the inner relay's own join entry is not the inner relay's `auth_token`. The outer relay presents that value when it forwards into the inner one; they must be the same.
- **`node-c` registered into `inner`, but the outer relay's `/swarm/nodes` does not name it.** That is by design: a relay's node list is its direct children. `node-c` is in `/swarm/topology` and in `/swarm/sessions`, and reachable through the two-hop mount.
- **The outer relay's topology reports a warning for the inner one.** The inner relay is known but cannot be reached right now: its tunnel dropped, or its lease went stale after a restart of `inner`. It re-registers within a third of the lease; a warning that stays is a network problem between the two.
- **A mount path stops working after a few hops.** A hand-written path is capped at the same hop budget the fan-out uses, so a client cannot walk a ring indefinitely. Shorten the route: address the node from the relay closest to it.
- **A ring shows a node once, not twice.** Correct: the swarm dedupes a node by identity (`agent_uuid`), keeps the shorter route and lists the other as an alternate.

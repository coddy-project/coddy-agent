# A relay and its nodes in Docker

**Goal.** Several Coddy nodes in containers, one relay in front of them, one place that lists every session of the fleet, and a path to any node from your laptop. Nothing is exposed but the relay's port: the nodes dial out to it, so they need no published port and no inbound route.

**Environment.** Docker with Compose v2 on one machine (the same files work across machines once `RELAY_URL` points at a real address), the published image `ghcr.io/coddy-project/coddy-agent` (it carries the `swarm` tag), and a model the nodes can reach. The example gives the nodes an OpenAI key; any provider from [Configuration](../getting-started/configuration.md) works the same way, a local OpenAI-compatible server included. The background is in [Swarm](../operate/swarm.md); this page is the shortest path to a running one.

## 1. The files

Four files in one folder. Tokens are the whole security model of a relay ([Security](../operate/swarm.md#security)), so replace the three values in `.env` before anything leaves your machine.

`.env`:

```bash
SWARM_CLIENT_TOKEN=change-me-client      # what a person or a client presents to the relay
SWARM_PAIRING_TOKEN=change-me-pairing    # what a node presents to register
NODE_A_TOKEN=change-me-node-a            # each node's own bearer, handed to the relay on join
NODE_B_TOKEN=change-me-node-b
WORKER_TOKEN=change-me-worker
OPENAI_API_KEY=sk-...
```

`relay.yaml`, the relay's configuration. It runs nothing but the relay:

```yaml
httpserver:
  enable: false

swarm:
  enable: true
  host: "0.0.0.0"
  port: 12346
  name: "outer"
  auth_token: "${SWARM_CLIENT_TOKEN}"
  pairing_tokens: ["${SWARM_PAIRING_TOKEN}"]
```

`node.yaml`, shared by every node. A node is an ordinary `coddy serve` with a model, a bearer token on its own API, and a `swarm.join` entry. There is no `advertise_url`, so the node opens the connection to the relay itself and is driven back down it (the tunnel transport):

```yaml
providers:
  - name: openai
    type: openai
    api_key: "${OPENAI_API_KEY}"

models:
  - model: "openai/gpt-5.6-terra"
    max_tokens: 8192

agent:
  model: "openai/gpt-5.6-terra"

httpserver:
  enable: true
  auth_token: "${NODE_TOKEN}"

swarm:
  join:
    - url: "${RELAY_URL}"
      name: "${NODE_NAME}"
      pairing_token: "${SWARM_PAIRING_TOKEN}"
      token: "${NODE_TOKEN}"
```

`docker-compose.yml`:

```yaml
x-node: &node
  image: ghcr.io/coddy-project/coddy-agent:latest
  command: serve -H 0.0.0.0 -P 12345
  working_dir: /workspace
  depends_on: [relay]

services:
  relay:
    image: ghcr.io/coddy-project/coddy-agent:latest
    command: serve
    environment:
      CODDY_HOME: /home/user/.coddy
      CODDY_CONFIG: /home/user/.coddy/config.yaml
      SWARM_CLIENT_TOKEN: ${SWARM_CLIENT_TOKEN}
      SWARM_PAIRING_TOKEN: ${SWARM_PAIRING_TOKEN}
    ports: ["12346:12346"]
    volumes:
      - ./relay.yaml:/home/user/.coddy/config.yaml:ro
      - relay_home:/home/user/.coddy

  node-a:
    <<: *node
    environment:
      CODDY_HOME: /home/user/.coddy
      CODDY_CONFIG: /home/user/.coddy/config.yaml
      CODDY_CWD: /workspace
      RELAY_URL: http://relay:12346
      NODE_NAME: node-a
      NODE_TOKEN: ${NODE_A_TOKEN}
      SWARM_PAIRING_TOKEN: ${SWARM_PAIRING_TOKEN}
      OPENAI_API_KEY: ${OPENAI_API_KEY-}
    volumes:
      - ./node.yaml:/home/user/.coddy/config.yaml:ro
      - ./workspace/node-a:/workspace
      - node_a_home:/home/user/.coddy

  node-b:
    <<: *node
    environment:
      CODDY_HOME: /home/user/.coddy
      CODDY_CONFIG: /home/user/.coddy/config.yaml
      CODDY_CWD: /workspace
      RELAY_URL: http://relay:12346
      NODE_NAME: node-b
      NODE_TOKEN: ${NODE_B_TOKEN}
      SWARM_PAIRING_TOKEN: ${SWARM_PAIRING_TOKEN}
      OPENAI_API_KEY: ${OPENAI_API_KEY-}
    volumes:
      - ./node.yaml:/home/user/.coddy/config.yaml:ro
      - ./workspace/node-b:/workspace
      - node_b_home:/home/user/.coddy

volumes:
  relay_home:
  node_a_home:
  node_b_home:
```

Two details carry weight. The config file is mounted read-only over a named volume for the home directory, because the home is where a node keeps its sessions and the per-lease secret that lets it reclaim its name after a restart. And the nodes publish no port: everything reaches them through the relay.

## 2. Start it

```bash
mkdir -p workspace/node-a workspace/node-b
docker compose up -d
docker compose logs relay
```

The relay log ends with one `swarm node registered` and one `swarm tunnel established` line per node, within a few seconds of `serve` starting on them.

## 3. Check it

`/swarm/info` is open, everything else on the relay takes the client token:

```bash
export T=change-me-client
curl -s http://127.0.0.1:12346/swarm/info
curl -s -H "Authorization: Bearer $T" http://127.0.0.1:12346/swarm/nodes
curl -s -H "Authorization: Bearer $T" http://127.0.0.1:12346/swarm/topology
```

`node_count` is 2, the node list names `node-a` and `node-b` with `"transport": "tunnel"` and `"online": true`, and the topology draws both under the relay.

A node is mounted under `/swarm/nodes/<name>/`, so a node's own API is one path prefix away and the ordinary clients work unchanged:

```bash
curl -s -H "Authorization: Bearer $T" http://127.0.0.1:12346/swarm/nodes/node-a/v1/models
coddy --dry-run --remote http://127.0.0.1:12346/swarm/nodes/node-a --remote-token "$T"
coddy -p "Say hello in one line." --remote http://127.0.0.1:12346/swarm/nodes/node-a --remote-token "$T"
```

The dry run reports the models the node offers, and the one-shot prompt answers from `node-a`'s model. Afterwards the aggregated list shows that session with its route:

```bash
curl -s -H "Authorization: Bearer $T" 'http://127.0.0.1:12346/swarm/sessions'
```

Every row carries `node_path` (here `["node-a"]`) next to the session id, because ids are chosen per node and do collide.

In a browser, `http://localhost:12346/` is the relay's own web UI: it asks for the client token, then opens on the swarm map with both nodes. Click a node and the History drawer, the composer and the settings are that node's, with the relay in the middle and nothing aware of it ([The UI](../operate/swarm.md#the-ui)).

## 4. More nodes

A node without a `name` registers under its host name, which in Compose is the container id, so a service without `NODE_NAME` scales:

```yaml
  worker:
    <<: *node
    environment:
      CODDY_HOME: /home/user/.coddy
      CODDY_CONFIG: /home/user/.coddy/config.yaml
      CODDY_CWD: /workspace
      RELAY_URL: http://relay:12346
      NODE_TOKEN: ${WORKER_TOKEN}
      SWARM_PAIRING_TOKEN: ${SWARM_PAIRING_TOKEN}
      OPENAI_API_KEY: ${OPENAI_API_KEY-}
    volumes:
      - ./worker.yaml:/home/user/.coddy/config.yaml:ro
      - ./workspace/workers:/workspace
```

`worker.yaml` is `node.yaml` with the `name:` line removed. Then:

```bash
docker compose up -d --scale worker=3
curl -s -H "Authorization: Bearer $T" http://127.0.0.1:12346/swarm/nodes
```

Three more agents appear, named by their container ids. Workers that share a token and a workspace are interchangeable, which is what a scaled service is for; a node you address by name keeps its own service.

A node on another machine is the same `node.yaml` with `RELAY_URL` pointing at the relay's public address, and nothing to open on that machine: the tunnel is opened from its side.

## What tends to go wrong

- **The relay exits at startup with a message about binding off loopback without a client token.** `SWARM_CLIENT_TOKEN` is empty. A relay on `0.0.0.0` refuses to run open; `swarm.allow_insecure` overrides that for a lab and nothing else.
- **A node never appears.** Its pairing token does not match one of the relay's `pairing_tokens`; the node's log says the registration was refused. The token is compared exactly, spaces included.
- **A node appears, but a request through its mount answers 401.** The `token` in the node's `swarm.join` entry is not the node's own `httpserver.auth_token`. The relay presents that token when it proxies, so the two values must be the same.
- **`swarm tunnel established` never follows `swarm node registered`.** Something in front of the relay re-frames HTTP: a layer-7 proxy or an HTTP/2-only terminator. The tunnel needs a raw end-to-end connection ([Two transports](../operate/swarm.md#two-transports)); put the relay behind a TCP passthrough or give it TLS of its own with `swarm.tls`.
- **Two containers claim the same name.** Only the first keeps it: a name is proven by a per-lease secret the relay mints, not by the shared pairing token. Give each named service its own `NODE_NAME`, or drop the name and let the host name stand.
- **A node with `advertise_url` is refused.** In Docker the service name resolves into a private range, which the relay does not dial unless `swarm.allow_private_upstreams` names the host. Inside a Compose network the tunnel is the right transport anyway.
- **After `docker compose restart relay` the list is empty for a while.** The registry lives in memory; `/swarm/info` reports `registry_warming: true` until the nodes check in again, which they do within a third of the lease (30 seconds by default).
- **The models list is empty and a prompt fails.** The node has no provider it can reach: check `OPENAI_API_KEY` in its environment, or the address of a local server from inside the container.

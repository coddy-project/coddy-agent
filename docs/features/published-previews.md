# Published previews

The [preview server](preview-server.md) is for the moment you and the agent are iterating: a free port on loopback, the project's own files read on every request, an edit visible on reload. It assumes your browser is on the machine Coddy runs on.

Published previews are for the other situation. Coddy runs on a server, your people reach it through an HTTPS edge that already knows who they are, and a finished build - a report, a demo, a static site - has to reach somebody through that edge. A random port on loopback cannot be routed to, and publishing a port range to the Internet to work around that is not a reverse proxy configuration anyone wants to own.

So the channel is a second, separate one: Coddy runs a small read-only static origin on **one address that does not move**, over **one artifact root**, and `preview_publish` copies a directory of the project into that root and hands back the address your edge serves it under. The proxy authenticates the visitor and forwards to the origin; Coddy serves bytes and nothing else.

It is off until an operator turns it on, and turning it on changes nothing about `preview_server`.

## Publishing

Ask for it in your own words:

```text
Publish the built site so I can send the link to the team.
```

The agent calls `preview_publish`, which asks for your approval first - what it produces is readable by everyone your edge lets in - and answers with the address.

| Argument | Meaning |
|---|---|
| `path` | Directory to publish, relative to the session's working directory. Left out, the working directory itself. A file is accepted too: its directory is published and the returned address opens that file. |
| `name` | A short name, so publishing again replaces the artifact **at the same address**. That is what to reach for when somebody already has the link. Left out, every publish gets an address of its own. |
| `ttl_seconds` | How long it stays up. `0` keeps it until it is unpublished; left out, `tools.preview_publish.ttl_seconds` applies. |

`preview_unpublish` takes one down by its `name` or by the `id` in its address. A session can only replace or remove what it published itself: the id of a named artifact is derived from the session and the name, so two sessions asking for `site` get two addresses and neither can overwrite the other's.

The address always ends in a slash, so relative assets, ES modules, `fetch` and workers of the published page resolve inside their own artifact and nothing else.

## A copy, not a view

A published preview is a copy taken when you published it. The link keeps answering with what it answered with before, however much the branch moves afterwards - which is the point when somebody is reading it on the other side of the world. Publish again under the same name when they should see the new build.

The copy is taken under rules of its own:

- **Only the project.** The source has to be the session's working directory or inside it, symlinks resolved.
- **No dot-files.** `.git`, `.env`, `.coddy` and anything below them are never copied, and neither is a symlink that resolves to one, so they cannot be published by accident under a name that does not look like theirs.
- **No link out of the source.** A symlink to a file inside the source is copied as the bytes it names; one that points anywhere else, and one that names a directory, is left out - publish the built directory rather than one that links to its assets elsewhere. Everything left out is counted and named in the tool's answer.
- **Nothing half-published.** The copy is built in a staging directory and moved into place in one step. A publish that fails - a source over `max_bytes`, a full disk - leaves the previous artifact at that address exactly as it was, and leaves nothing new behind.
- **Bounded.** One artifact may hold up to `max_bytes` (100 MiB by default) and 20000 files.

## The origin

The static server is a subsystem of [`coddy serve`](../operate/serve.md), started when `tools.preview_publish.enable` is on. It serves the artifact root and refuses everything else:

- `GET` and `HEAD`; anything else is 405.
- The root itself is **never listed**: a visitor who has one artifact's address gets that artifact, not an index of everyone else's.
- A path with a dot segment (`..`, `.git`) is 404, whatever it would resolve to. Files are opened through an `os.Root`, so a link cannot lead out of the root either.
- An artifact whose lifetime has run out is 404 from the moment it expires, whatever the sweeper has got to; a sweep every minute then removes it from the disk.
- `Cache-Control: no-store`, so a republished artifact is never read from a stale cache.
- On a loopback bind, a request whose `Host` is not a loopback name, the configured host or the host of `public_url` is refused, which is the same DNS-rebinding guard the preview server has.

Moving the address takes a fresh process, the way the HTTP server's does; the root and the public address are picked up by a reload.

One artifact root belongs to one running origin. Nothing arbitrates between processes over who sweeps it and who writes into it, so do not point two `coddy serve` at the same directory.

## Configuration

```yaml
tools:
  preview_publish:
    enable: true
    host: 127.0.0.1                        # keep the origin private
    port: 8081                             # fixed: the proxy needs a stable upstream
    root: /var/lib/coddy/previews          # the only directory it serves
    public_url: https://coddy.example/preview/
    ttl_seconds: 86400
    max_bytes: 104857600
```

The bind is loopback by default. An edge on another host needs an address it can reach, and anything that is not loopback serves every published artifact to whoever can reach it: choose a private address and protect it with network policy or an authenticated tunnel. `0.0.0.0` is not a safe default here and Coddy does not pretend it is. The fields are listed in the [configuration reference](../reference/config.md).

## Behind your reverse proxy

Authentication, TLS, rate limiting and public routing stay with the proxy. Coddy's API token is never given to the static origin and never reaches the browser, and `/preview/*` must not fall through to the API upstream.

Caddy:

```caddyfile
coddy.example {
    handle /preview/* {
        forward_auth auth.internal:9091 {
            uri /api/verify
            copy_headers Remote-User
        }
        uri strip_prefix /preview
        reverse_proxy 127.0.0.1:8081
    }
    handle {
        reverse_proxy 127.0.0.1:12345
    }
}
```

Nginx:

```nginx
location /preview/ {
    auth_request /auth;
    proxy_pass http://127.0.0.1:8081/;
    proxy_set_header Host $host;
}
```

Traefik, as labels on the Coddy service:

```yaml
traefik.http.routers.coddy-preview.rule: Host(`coddy.example`) && PathPrefix(`/preview`)
traefik.http.routers.coddy-preview.middlewares: coddy-auth@file,coddy-preview-strip
traefik.http.services.coddy-preview.loadbalancer.server.port: "8081"
traefik.http.middlewares.coddy-preview-strip.stripprefix.prefixes: /preview
```

Three things to check in whichever you use: the authentication middleware runs **before** the static upstream, the proxy is the only thing that reaches `127.0.0.1:8081`, and `public_url` is the address with the prefix the visitor actually types - that is what the agent hands out.

A proxy that forwards the visitor's `Host` is answered because `public_url`'s host is on the allowlist; one that rewrites `Host` to the upstream is answered because the bind host is on it too.

## What it is not

- **Not a web host.** It serves static files with no authentication of its own. Everything about who may read a preview is a decision your edge makes.
- **Not a secret store.** An artifact is readable by everyone the edge admits. Publishing is a deliberate act, which is why it asks for approval every time.
- **Not `preview_server`.** That one is still there, still takes a free local port, still reads the project on every request, and is still what to use while you are working on the page with the agent.

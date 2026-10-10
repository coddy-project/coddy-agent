# HTTP requests

The `http_request` tool is the agent's curl. The model chooses the method, the query parameters and every header, sends a body of any kind or a workspace file, and reads the answer the way `curl -i` prints it. It is what the agent uses to call an API, to check the dev server it has just started on localhost, and to upload or download a file.

`webfetch` stays the tool for reading an article: it takes one public URL and returns the main text as Markdown. The two share one HTTP client - `webfetch` is `http_request` with a narrower contract, described [below](#webfetch-on-the-same-client).

Because a request the model shapes itself can change things somewhere else and can carry anything the agent has read, it goes through the permission gate: under `ask` the operator sees where the request goes and what it carries before anything is sent.

## A request and its answer

```json
{
  "method": "PATCH",
  "url": "https://api.example.com/items/7?verbose=1",
  "query": { "tag": ["red", "blue"] },
  "headers": { "Authorization": "Bearer ${TOKEN}", "X-Trace": "abc-123" },
  "json": { "name": "lamp" }
}
```

The request goes to `https://api.example.com/items/7?verbose=1&tag=red&tag=blue` with the JSON body `{"name":"lamp"}` and `Content-Type: application/json`. The answer:

```text
HTTP/2.0 200 OK
Content-Type: application/json
Date: Wed, 16 Sep 2026 12:00:00 GMT

{"id":7,"name":"lamp"}
```

![A finished http_request call in the web UI transcript](../assets/http-requests/http-request-answer-dark-1280.png)

*The call and its answer in the transcript: the arguments, then the status line, the headers and the body.*

A 4xx or 5xx status is an answer like any other, not a failed call: the model reads the status line and decides what to do. The call fails only when no answer arrived - the address did not resolve, the connection was refused, the certificate did not verify, the timeout ran out - when the body could not be saved to `output_file`, or when the arguments were refused before anything was sent.

## Arguments

| Argument | Meaning |
|----------|---------|
| `url` | Required. An absolute `http://` or `https://` URL; query parameters may be written in it. |
| `method` | Any method token, sent upper-cased: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`, `OPTIONS` or a custom one. Default `GET`, or `POST` when a body is given. |
| `query` | Query parameters appended to those already in `url`. A value is a string, a number, a boolean, or an array of those for a repeated name. |
| `headers` | Request headers by name. See [Headers](#headers). |
| `body` | Raw request body, sent as is. |
| `body_base64` | Binary request body, base64-encoded; `Content-Type: application/octet-stream` unless set. |
| `body_file` | A local file sent as the whole body; `Content-Type` follows its extension unless set. |
| `json` | Any JSON value, sent compacted with `Content-Type: application/json`. A string that itself holds a JSON object or array is sent as that document. |
| `form` | Fields sent as an `application/x-www-form-urlencoded` body; values as in `query`. |
| `form_data` | `multipart/form-data` parts in order: each has a `name` and either a `value` or a local `file`, with optional `filename` and `content_type`. |
| `output_file` | Save the response body to this path instead of returning it. |
| `follow_redirects` | Follow redirects that stay on the origin. Default `false`. See [Redirects](#redirects). |
| `proxy` | A proxy for this request, or `direct`. See [Proxy and certificates](#proxy-and-certificates). |
| `verify_tls` | `false` accepts any server certificate, like `curl -k`. Default `true`. |
| `timeout_seconds` | Bound on the whole exchange, reading the body included. Default 30, at most 600. |
| `permission_rationale` | Text shown at the top of the permission prompt. |

A request carries at most one payload: `body`, `body_base64`, `body_file`, `json`, `form` or `form_data`. Two of them are refused with an error naming both. Paths in `body_file`, `form_data[].file` and `output_file` are resolved against the session's working directory, and a file that does not exist fails the call before anything is sent.

A multipart body is laid out before it is sent, with the files as parts of known size, so the request carries an exact `Content-Length` - servers that refuse a chunked upload accept it - and the file contents are streamed rather than read into memory.

## Headers

A header named in `headers` replaces whatever the tool would have sent under that name: its own `User-Agent`, the `Content-Type` of the payload, the `Host` of the address, and a [default header](#default-headers) the operator configured. An empty value removes a header the tool would otherwise send: `"User-Agent": ""` sends none, and `"Content-Type": ""` leaves a JSON body without one.

Three headers are checked rather than passed through:

- `Content-Length` must equal the size of the body - leave it out and it is computed;
- `Transfer-Encoding` accepts only `chunked`, which sends the body without a length;
- `Host` cannot be removed, only replaced.

The tool sends no `Accept-Encoding` of its own. When a request asks for `gzip` or `deflate` itself, the body is decoded for reading and saved to `output_file` as it arrived.

## Default headers

Some headers belong to every request rather than to one call. A site behind a hosting front may serve its files to browsers only: `https://match3.drobek.online/app.webmanifest` answers the tool's own `User-Agent` with `415 Unsupported Media Type` and an HTML page, and a Chrome `User-Agent` with the manifest. An internal service may expect an `Accept` or a client header on every call. `tools.http_request.default_headers` names such headers once:

```yaml
tools:
  http_request:
    default_headers:
      User-Agent: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
      Accept: application/json
```

The tool builds the headers of a request in three layers, and a later layer wins:

1. the tool's own defaults: the `coddy-agent` `User-Agent`, and the `Content-Type` of the payload, which `default_headers` cannot take (see below);
2. `default_headers`;
3. the call's `headers`.

A header the call names replaces the configured one, however the call spells its name, and an empty value in the call removes it: `"User-Agent": ""` then sends no `User-Agent` at all. An empty value in `default_headers` leaves that header out of every call that does not set it, so `User-Agent: ""` stops the tool from introducing itself. Without the key the requests go out exactly as before.

`Host`, `Content-Type`, `Content-Length` and `Transfer-Encoding` are refused: the tool derives them from each call - its address, its payload and the payload's framing - and a call that needs another value sets it in its own `headers`. So are the hop-by-hop headers - `Connection`, `Keep-Alive`, `Proxy-Connection`, `TE`, `Trailer` and `Upgrade` - which describe one connection rather than the client; set for every request they break them (HTTP/2 refuses a request that carries `Upgrade`). `Proxy-Authorization` is refused too: an `https` request goes through a proxy as a tunnel and carries its headers inside it to the origin, so a proxy's credential set here would reach every destination; it belongs in the proxy address (`HTTPS_PROXY`, or a call's `proxy` as `http://user:password@host:3128`). A name has the shape of every header in use - letters, digits, `-` and `_`, starting with a letter - because it is also a segment of a config path, where a dot would split it and a number would read as a list position; a call can still send any other name in its own `headers`. Two spellings of one header and a value with a line break in it are refused as well. The loader refuses such a configuration at startup, and `coddy -t` names the header in its report.

A configured header acts as if the call had written it: an `Accept-Encoding: gzip` here, for example, gets every body decoded for reading and saved to `output_file` as it arrived.

Only `http_request` sends these headers. `webfetch`, the page an `@https://...` mention reads, and the requests to the model providers keep their own.

In the web UI the map is the **Default headers** block of **HTTP requests** on the **Tools and permissions** tab of Settings: a row per header, its name and its value, with **Add** under the rows.

![The default headers of http_request in the web UI settings](../assets/http-requests/http-request-default-headers-settings-dark-1280.png)

*The HTTP requests block of the Tools and permissions tab: the allowlist, then a row per default header.*

### What the prompt shows

The permission prompt lists the configured headers among the ones that go out and names them on a line of their own, so the operator can tell them from the headers the model wrote; a header the configuration leaves out with an empty value is named there too, as `User-Agent (not sent)`:

```text
GET https://match3.drobek.online/app.webmanifest
Headers:
  Accept: application/json
  User-Agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36
Headers from tools.http_request.default_headers: Accept, User-Agent
```

The prompt shows the configured value of a header that says who the client is or what it accepts - `User-Agent`, the `Accept` family, `Cache-Control`, `Pragma`, `DNT`, `Origin`, `Upgrade-Insecure-Requests`, and the `Sec-Ch-Ua` client hints and `Sec-Fetch-` metadata a browser adds. Any other configured header shows its name with `<redacted>` for the value: a credential can sit under any name (`X-Auth`, `X-Session`), and the prompt also reaches a Telegram chat, a notification hook and whoever looks at the screen. A header the model wrote into the call itself is shown as written, the way it stands in the transcript.

The headers are part of the configuration, and a grant does not record them: an approved or allowlisted destination gets its requests with whatever `default_headers` holds at the time. A prompt that waits across a restart of `coddy serve` is answered for the request it showed: when `default_headers` has changed by the time the answer arrives, the call asks again with the request it would send now instead of running under the old answer.

### Credentials

Every request of the tool carries these headers, to every destination the model reaches with it: the service you had in mind, a page a search turned up, an address a prompt injection asked for. An `Authorization`, a `Cookie` or an API key header set here goes to all of them, without a prompt under `bypass` or to an allowlisted destination. Keep credentials out of `default_headers` unless that is the intent, for example on a machine that talks to one service; otherwise let the call carry the credential, where the prompt shows it for the one destination it is meant for.

`config_get` shows the model which default headers are configured and never their values, the way it treats the header values of an MCP server - an empty one excepted, which says the header is left out; the file and the Settings screen show them. A value the model stages back as it read it, `<redacted>`, is refused rather than written over the header.

## The answer

The status line comes first, then the response headers in name order, a blank line and the body. Notes about what the body did not show follow at the end in brackets.

A body is shown as text when it is text: decoded from the charset its `Content-Type` declares (`windows-1251` arrives readable), or as UTF-8 when it is valid UTF-8 with no NUL bytes. Anything else is described instead of dumped:

```text
[binary body: 48213 bytes, image/png; pass output_file to save it]
```

At most 1 MiB of a body is read into the answer, with a note when it was cut, and what reaches the model is cut further by `tools.output_limits.default` - so a large or binary body belongs in `output_file`. A download is written next to its destination and renamed into place when it is complete, so a failed or oversized one (more than 1 GiB) never leaves a partial file under that name; the answer says `[body: saved 48213 bytes to /path/logo.png]`.

## Redirects

A redirect is not followed unless the call passes `follow_redirects: true`. The 3xx answer comes back with its `Location`, and the model decides whether to go there.

With `follow_redirects: true`, a redirect is followed only while it stays on the origin of the request - the same scheme, host and port - or upgrades `http` to `https` on the same host. A redirect to another origin is held: the answer is that redirect, with a note naming the address it pointed to, and reaching it takes a new call that goes through the permission gate on its own. An approval of one service is therefore never carried to another by the service itself. A `307` or `308` sends the body again, files included.

## Proxy and certificates

Without `proxy`, a request uses the proxies the environment of the Coddy process names (`HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY`), as curl does; a request to `localhost` or a loopback address is never proxied. `proxy` overrides that for one request:

- `http://host:port`, `https://host:port`, `socks5://host:port` or `socks5h://host:port` sends the request through that proxy, and credentials in the URL (`http://user:pass@host:3128`) are used to authenticate to it; `socks5h` resolves the destination's name through the proxy;
- `direct` ignores the environment's proxies and connects straight to the destination.

`verify_tls: false` accepts any server certificate - a self-signed dev server, an internal service behind a private CA. The prompt says so in capitals.

## Permissions

`http_request` is gated like `run_command`: whether it asks depends on the session's permission mode, on `tools.http_request.allowlist` and on what was approved earlier in the session.

| Mode | What asks |
|------|-----------|
| `bypass` | Nothing. |
| `accept_edits` | A request whose destination is not allowlisted or approved, or that carries something the destination's approval does not cover. |
| `ask` | The same, and also a request whose `output_file` was not approved: saving a response is a file write. |

The prompt shows the request as it would go out, not its JSON arguments:

```text
Upload the quarterly report

POST https://api.example.com/upload?v=2
Headers:
  Authorization: Bearer abc
  Content-Type: multipart/form-data; boundary=<generated>
  User-Agent: coddy-agent/1.0 (+https://github.com/EvilFreelancer/coddy-agent)
Body: multipart form, 20611 bytes
  doc: the file /home/me/project/report.pdf (20480 bytes)
  note = Q3
Proxy: http://10.0.0.2:3128
Saves the response body to /home/me/project/out/receipt.json

An always answer also approves the files it uploads, the proxy and the file it saves for requests to https://api.example.com.
```

![The permission card of an http_request upload in the web UI](../assets/http-requests/http-request-permission-dark-1280.png)

*The web UI card: the request as it would go out, the uploaded file, and one button per grant it can leave behind.*

Arguments the tool would refuse still ask, with the reason the call will fail at the end of the prompt. The headers the configuration adds are listed with the rest and named on a line of their own ([What the prompt shows](#what-the-prompt-shows)).

### What an answer approves

The dialog offers four choices:

- **Allow** - this request, once;
- **Always allow `https://api.example.com/upload`** - every later request to this address for the rest of the session, whatever its method and query (not offered when the address is the origin's root);
- **Always allow `https://api.example.com`** - every later request to anything on this origin;
- **Reject**.

Approving a destination does not approve what a later request to it carries, because those were never shown. An "always" answer records the destination together with what this request carried, and a later request is let through only when everything it carries was recorded too:

- a local file (`body_file`, a `form_data` file part) - approved for this origin; the same file sent to another origin, or another file sent here, asks again;
- a proxy - approved for this origin; another proxy asks again;
- an unchecked certificate (`verify_tls: false`) - approved for this origin;
- the `output_file` path - approved as that path, the way an "Allow always" on a file write is.

The grants live in the session bundle (`permission_grants.json`) and survive a restart of the session; a new session starts with none.

### The allowlist

`tools.http_request.allowlist` names destinations that never ask:

```yaml
tools:
  http_request:
    allowlist:
      - api.github.com               # a host, any scheme and port
      - "*.internal.example.com"     # its subdomains, not the domain itself
      - localhost:8080               # a host and a port
      - http://127.0.0.1:3000        # an origin
      - https://api.example.com/v1/  # an address prefix
```

An entry with a path covers that address and everything under it: `https://api.example.com/v1` covers `/v1` and `/v1/items` but not `/v10`, and `https://api.example.com/v1/` covers only what is under `/v1/`. `"*"` allows every destination. An entry that cannot match - an unsupported scheme, credentials, a query, a path without a scheme, a wildcard anywhere but the leftmost labels - is a configuration error, reported by `coddy -t` with its index.

An allowlisted destination also covers the files a request to it uploads and an unchecked certificate: the entry is the operator's own statement of trust in that service. It does not cover a proxy, which is a destination of its own and needs an entry of its own, nor `output_file` under `ask`, which follows the write policy.

### Private addresses

Unlike `webfetch`, `http_request` does not refuse localhost or private networks: reaching the service the agent is building is one of the reasons the tool exists. The permission gate is what stands between the model and those addresses, so under `bypass`, or with `"*"` in the allowlist, the model can reach anything the host can - the cloud metadata address `169.254.169.254` included, the same as `run_command` running `curl` could. Keep `bypass` for trusted environments, and allowlist the services an unattended run needs by name.

## webfetch on the same client

`webfetch` builds the same request `http_request` would for a plain `GET` of its URL and sends it through the same client, with a narrower policy:

- the address is checked against the SSRF guard - no localhost, `.local` names, private, link-local or metadata addresses, no credentials in the URL - and so is every redirect before it is followed;
- every redirect is followed, up to ten;
- the transport asks for gzip and decodes it;
- a non-2xx status is an error, the page is capped at 4 MiB and the answer is run through readability into Markdown;
- the [default headers](#default-headers) are not sent: the request carries the tool's own `User-Agent`.

It needs no permission because none of that can be steered by the model beyond the URL.

## Related

[Tools](../reference/tools.md) - every built-in tool, its arguments and its permission class;
[Security and trust](../operate/security.md#permission-modes-and-prompts) - permission modes and what each of them asks about;
[Web search](web-search.md) - `websearch`, and `webfetch` for reading a page;
[config.yaml reference](../reference/config.md) - `tools.http_request.allowlist`, `tools.http_request.default_headers` and `tools.output_limits`.

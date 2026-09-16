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

A header named in `headers` replaces whatever the tool would have sent under that name, `User-Agent`, `Content-Type` and `Host` included. An empty value removes a header the tool would otherwise send: `"User-Agent": ""` sends none, and `"Content-Type": ""` leaves a JSON body without one.

Three headers are checked rather than passed through:

- `Content-Length` must equal the size of the body - leave it out and it is computed;
- `Transfer-Encoding` accepts only `chunked`, which sends the body without a length;
- `Host` cannot be removed, only replaced.

The tool sends no `Accept-Encoding` of its own. When a request asks for `gzip` or `deflate` itself, the body is decoded for reading and saved to `output_file` as it arrived.

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

`http_request` is gated like `run_command`: whether it asks depends on `tools.permission_mode`, on `tools.http_request.allowlist` and on what was approved earlier in the session.

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

Arguments the tool would refuse still ask, with the reason the call will fail at the end of the prompt.

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
- a non-2xx status is an error, the page is capped at 4 MiB and the answer is run through readability into Markdown.

It needs no permission because none of that can be steered by the model beyond the URL.

## Related

[Tools](../reference/tools.md) - every built-in tool, its arguments and its permission class;
[Security and trust](../operate/security.md#permission-modes-and-prompts) - permission modes and what each of them asks about;
[Web search](web-search.md) - `websearch`, and `webfetch` for reading a page;
[config.yaml reference](../reference/config.md) - `tools.http_request.allowlist` and `tools.output_limits`.

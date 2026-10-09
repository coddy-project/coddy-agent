# Certificates and TLS: a field guide

Coddy speaks TLS in several places. **TLS is the transport's business and nothing else**: a listener presents a certificate, a client verifies it and may present one of its own, and a listener that was given a client authority refuses, at the handshake, a peer whose certificate does not chain to it. **Coddy reads no identity out of a certificate**: no name, no subject, no class, no budget. A certificate is not a credential; the credential is a token. Everything beyond the handshake (who may call what, per-client limits, address rules, revocation, audit) is governed where it is governed best: at the L7 reverse proxy in front of Coddy and in the infrastructure around it ([where the other risks go](#where-the-other-risks-go)).

**Coddy makes the certificates it needs itself**, with the Go standard library and no external tool: at installation, at update and on demand, with [`coddy tls`](#built-in-certificates-coddy-tls). Certificates of your own (a company authority, a purchased certificate, ACME) are the same setup with files named by hand: Coddy reads them and does not care where they came from ([your own certificates](#your-own-certificates)).

Everything here is **opt-in**. A relay or a node with no certificate settings speaks plain HTTP, which is right on a loopback address, behind a reverse proxy that terminates TLS, or inside a tunnel.

## The picture

Six legs use TLS. For each, one side **serves** (it has a certificate and a key) and the other **dials** (it checks the certificate it is shown, and may show one of its own).

| Leg | The server side | The dialling side | Keys (with the built-in certificates) |
|---|---|---|---|
| A borrowing Coddy to a node or a relay mount | `httpserver.tls` (node), `swarm.tls` (relay) | the provider row of type `coddy` | `providers[].tls_auto` (or `ca_file`, `client_cert_file`, `client_key_file`) |
| A node joining a relay | `swarm.tls` | the join entry | `swarm.join[].dial.auto` (or `dial.ca_file`, `cert_file`, `key_file`) |
| The relay to a node that registered itself over an address | `httpserver.tls` on the node | the relay | `swarm.node_tls.auto` (or `ca_file`, `cert_file`, `key_file`) |
| The relay to a node or a child relay it lists by hand | `httpserver.tls`, `swarm.tls` | the relay | `swarm.upstreams[].dial.auto` (or `dial.*`) |
| A browser to the web UI of a node or a relay | `httpserver.tls`, `swarm.tls` | the browser | the browser's own stores |
| The console and ACP with `--remote` | `httpserver.tls`, `swarm.tls` | `coddy --remote` | none: the system's roots only, and no client certificate |

What a **server** asks of its callers is set in the same block as its own certificate: `client_ca_file` is the authority that client certificates are checked against, and with it the handshake **requires** a certificate that chains to it (`require_client_cert: true` does the same with the built-in bundle). That is all a certificate does. A caller is let in by its **token**.

## What Coddy reads: the rules that decide everything

These are facts about the code, not preferences. Most trouble comes from one of them.

- **PEM files only.** A certificate file is PEM: the **leaf first**, then its intermediates (a "full chain"). A key file is one PEM private key: `PRIVATE KEY` (PKCS#8), `RSA PRIVATE KEY` or `EC PRIVATE KEY`. DER, PKCS#12 (`.pfx`, `.p12`), Java keystores and keys held in a hardware token or the operating system's store are **not** read. The built-in certificates are PEM already.
- **No passphrases.** A private key with a passphrase cannot be used: Coddy has nowhere to ask for it, and says `tls: failed to parse private key`. The built-in keys have none and are written with mode `0600`.
- **Key usages are enforced by the TLS stack.** A server checks that a client certificate has the `clientAuth` extended key usage (or none at all); a client checks that a server certificate has `serverAuth` (or none). A certificate from a public certificate authority is made for servers and, increasingly, has `serverAuth` only: it cannot be a client certificate.
- **The host name is checked against the SAN of the server certificate**, never the CN. A server reached by an address (`https://10.0.0.5:12346`) needs an **IP** entry in its SAN; one reached by a name needs that name (a wildcard covers exactly one label). The built-in server certificate carries the host name, `localhost`, the loopback addresses, the addresses of the interfaces and the bind and `advertise_url` hosts of `config.yaml`; `tls.names` or `coddy tls ensure --name` add more.
- **`ca_file` replaces the system's roots for that leg.** With it, only the authorities in the file are trusted for that connection; without it, the system's roots are. A file may hold several certificates, and a **self-signed certificate is its own authority**: listing the file of a self-signed server certificate in `ca_file` is how a client trusts it. The built-in `bundle.pem` is such a file: this machine's CA and the CAs it was told to trust.
- **The system's roots are the operating system's.** On Linux that is the distribution's bundle (`SSL_CERT_FILE` and `SSL_CERT_DIR` are honoured); on macOS the keychain's trust settings; on Windows the certificate store. A private authority installed there is trusted by every program that reads the system's store, which is why `ca_file` (or the built-in `tls_auto`) is the narrower choice.
- **There is no revocation check.** Neither a CRL nor OCSP is consulted: revocation is the proxy's or the infrastructure's business, and a certificate on this side ends with its dates. The built-in leaves last a year and are renewed 30 days before their end.
- **TLS 1.2 is the minimum.** A node's own listener serves HTTP/1.1 only, so that a shared call keeps a connection of its own ([the liveness bound](../features/shared-models.md#a-peer-that-vanishes)).

**When each file is read.** A listener's pair and its client authority are **startup state**: changing them, a renewed server certificate included, takes a restart of `coddy serve`. A dialling side's pair (`client_cert_file`, `dial.cert_file`, `node_tls.cert_file`, the built-in client pair) is read **at every handshake** (the two files are read each time and parsed again only when their bytes changed), so a renewed certificate is used by the next connection with no restart; a changed `ca_file` is picked up when the leg's transport is next built (a `coddy` row's next call; a join at its next start).

## Built-in certificates: `coddy tls`

Coddy makes the certificates it needs itself, with the Go standard library and no external tool (no `openssl`, no `mkcert`), under `$CODDY_HOME/tls` (mode 0700, keys 0600):

| File | What |
|---|---|
| `ca.crt`, `ca.key` | this machine's CA: ECDSA P-256, ten years, path length 0. **The CA key never leaves the machine.** |
| `server.crt`, `server.key` | the certificate of this machine's listeners: one year, `serverAuth`, carrying the host name, `localhost`, the loopback addresses, the address of each non-link-local interface, the bind and `advertise_url` hosts of the configuration and any `--name` |
| `client.crt`, `client.key` | the certificate this machine presents when it dials: one year, `clientAuth` |
| `trusted/*.crt` | the CAs of other machines this one was told to trust |
| `bundle.pem` | this CA plus every trusted CA: what a client's `ca_file` and a listener's `client_ca_file` point at |

```bash
coddy tls ensure            # make what is missing or ending; nothing to do when all is right
coddy tls status [--json]   # what is there, the names, the days left, what ensure would do
coddy tls renew             # issue both certificates again now (the CA stays)
coddy tls export > a-ca.crt # this machine's CA certificate, the one thing to give a peer
coddy tls trust a-ca.crt    # trust another machine's CA (a file, or - for stdin)
coddy tls issue client ci -o ./ci   # a client certificate for something that has no Coddy
```

`ensure` is idempotent and safe to run from anywhere, at once: it plans from what is on disk, applies the plan under a file lock with atomic writes (key first, certificate second), replaces a valid CA never (only `--force-ca`, or an expired one, does, and it says that every peer must then trust the new CA), and issues a leaf again only for a reason it names: missing, its key does not match, signed by another CA, expired or within 30 days of its end, or lacking a wanted name.

**Two machines trust each other without a private key moving**: on A `coddy tls export > a-ca.crt`, on B `coddy tls trust a-ca.crt`, and the same the other way; each side then verifies the other's server and client certificates against its `bundle.pem`. What trusting means: a CA you `trust` is trusted by **Coddy's own connections that use the bundle** (`auto: true`, `tls_auto`) and by nothing else, since the bundle is never added to the system's store or a browser's, but within those connections the holder of its key can vouch for any name, because the built-in CAs carry no name constraints. Trust the machines you would trust with the traffic, and prefer one CA per trust domain.

**Use it from the configuration** with `auto: true` on the block that names certificate files, and the files are filled in with these (a file you name wins, per key; nothing is written into your `config.yaml`):

| Block | `auto: true` fills |
|---|---|
| `httpserver.tls`, `swarm.tls` (a listener) | `cert_file`, `key_file` with the server pair; with `require_client_cert: true`, `client_ca_file` with the bundle, so the handshake requires a client certificate that chains to this machine's CA or a trusted one |
| `swarm.node_tls`, `swarm.join[].dial`, `swarm.upstreams[].dial` | `ca_file` with the bundle, `cert_file` and `key_file` with the client pair |
| `providers[].tls_auto: true` (type `coddy`) | `ca_file` with the bundle, `client_cert_file` and `client_key_file` with the client pair |

`tls.names` adds names Coddy cannot see (a proxy's name, a DNS alias, a public address) to the server certificate. `coddy serve` makes what a block asks for before it listens or dials, `coddy -t --dry-run` reports what is missing or ending, `coddy tls ensure` does it by hand, and a listener reads its pair at start, so a renewed server certificate takes a restart.

TLS here is the transport's business and nothing else: a certificate admits a peer at the handshake and is no credential, and Coddy reads no name out of it. Public certificates (ACME, an enterprise CA) are the same setup with the files named by hand. Everything else (who may call what, rate limits, allowlists, revocation, audit) belongs to the reverse proxy and the infrastructure: [where the other risks go](#where-the-other-risks-go).

## Scenarios with the built-in certificates

### One machine, a browser or a script

```yaml
httpserver:
  tls:
    auto: true      # the server pair of ~/.coddy/tls, made at start
```

A browser or a script that opens it must trust this machine's CA once: `coddy tls export > coddy-ca.crt`, then import that file as a trusted authority into the browser (Firefox: Settings, Certificates, Authorities; Chrome and the system: the operating system's certificate manager) or give it to `curl --cacert coddy-ca.crt https://host:12345/...`. Do not set `require_client_cert` on a node whose web UI people open: a browser has no client certificate unless you import one, and `coddy --remote` has no option to present one.

### A node and a borrowing Coddy

On the node (`httpserver.tls.auto: true`, `require_client_cert: true`, `shared_models.tokens`), and on the borrower (`providers[].tls_auto: true`), exchange the authorities once, in both directions, and restart the node (it reads its bundle at start):

```bash
node$      coddy tls export > node-ca.crt          # copy node-ca.crt to the borrower
borrower$  coddy tls export > borrower-ca.crt      # copy borrower-ca.crt to the node
borrower$  coddy tls trust node-ca.crt
node$      coddy tls trust borrower-ca.crt
```

The borrower then verifies the node's server certificate against its bundle and presents its own client pair, which the node's handshake accepts because the node trusts the borrower's CA. The borrower's token (a shared-model token, `httpserver.shared_models.tokens` on the node) is still what lets it list and call the shared models: a certificate alone is a `401`. Private keys never leave their machines. `coddy -t --dry-run` on the borrower reaches the node with the built-in files and tells what is missing.

### A relay, nodes and borrowers

```yaml
# the relay
swarm:
  tls: {auto: true, require_client_cert: true}   # nodes that join and borrowers present a client certificate
  node_tls: {auto: true}                         # towards nodes that registered themselves over an address
  clients:
    - {name: acme, scope: shared_models, nodes: ["nas02"], token: "${ACME_RELAY_TOKEN}"}
# a node
httpserver:
  tls: {auto: true, require_client_cert: true}
swarm:
  join:
    - {url: "https://relay.example:12346", name: nas02, pairing_token: "${PAIRING}", advertise_url: "https://nas02.example:12345", dial: {auto: true}}
```

Every machine of the setup trusts the CAs of the machines it talks to (`export` and `trust` between the relay, each node and each borrower), and each machine makes its own certificates. The end-to-end test of this page runs exactly this ([the tests that hold this page](#the-tests-that-hold-this-page)).

### Something with no Coddy: a script, a proxy

`coddy tls issue client NAME -o DIR` makes a client certificate for it, signed by this machine's CA, and writes `NAME.crt`, `NAME.key` (mode 0600) and `ca.crt` (the CA to trust) into `DIR`. Hand over `NAME.key` once, over a channel you trust; the CA key never leaves.

### Renewing and replacing the CA

`coddy tls ensure` (run for you at `coddy serve`, `coddy update`, `coddy serve install`) issues a leaf again 30 days before its end or when it lacks a name, and never replaces a valid CA. A CA reaches its end after ten years, or is replaced by `coddy tls ensure --force-ca`: every peer that trusted the old one must then trust the new one (`export`, `trust`), and every leaf is issued again with it. `coddy tls status` and `coddy -t --dry-run` say when something ends.

## Your own certificates

A certificate from a company authority, one you bought, one from ACME (Let's Encrypt), a Microsoft or Vault authority, `mkcert`: name the files in the same keys, and they win over `auto: true`:

```yaml
httpserver:
  tls:
    cert_file: /etc/coddy/server.crt     # the leaf first, then its intermediates
    key_file: /etc/coddy/server.key      # one PEM key, no passphrase
    client_ca_file: /etc/coddy/clients-ca.pem   # optional: the handshake then requires a certificate that chains to it
```

- **A server certificate** must carry the host or IP its callers dial in its SAN, and a client that does not trust its authority needs `ca_file` (or `tls_auto`, with the authority added by `coddy tls trust`).
- **A client certificate** needs the `clientAuth` usage: one bought for a web server is a server certificate and is refused. It goes in `client_cert_file` and `client_key_file` (`dial.cert_file`, `node_tls.cert_file`).
- **A key with a passphrase, a `.pfx` or a `.p12`** is converted on the machine that uses it, with `openssl pkey -in enc.key -out plain.key` and `openssl pkcs12 -in x.pfx -nodes`, and the plain file protected like any secret (mode `0600`, the account that runs Coddy, never in a repository or an image). On a service manager, prefer its credentials store.
- **ACME and public certificates** are best terminated in a reverse proxy that renews them by itself (Caddy, Traefik, nginx with an ACME module): Coddy then listens on loopback in plain HTTP behind it, and [the proxy does the rest](#where-the-other-risks-go). A direct listener reads its pair at start, so a renewal needs a restart.
- **Hardware tokens, smart cards, the TPM and the operating system's store** are not supported: a key must be a file.

## Checking it, and what the errors mean

```bash
coddy tls status                       # the files, the names, the days left, what ensure would do (--json for a script)
coddy -t --dry-run                     # every pair loads and has not expired; each provider, join and upstream is dialled with its own pair
# from outside, with openssl as an inspector only
openssl s_client -connect node.example:12345 -servername node.example -CAfile ~/.coddy/tls/bundle.pem \
  -cert ~/.coddy/tls/client.crt -key ~/.coddy/tls/client.key </dev/null 2>&1 | sed -n '/Verification/p'
curl --cacert ~/.coddy/tls/bundle.pem --cert ~/.coddy/tls/client.crt --key ~/.coddy/tls/client.key \
  -H "Authorization: Bearer $SHARED_TOKEN" https://node.example:12345/coddy/llm/models
```

| What you see | Cause and fix |
|---|---|
| `x509: certificate signed by unknown authority` (the dialling side) | The server's certificate is not signed by anything in `ca_file` or the bundle. Trust its authority: `coddy tls trust node-ca.crt`. |
| `x509: certificate is valid for A, not B` / `cannot validate certificate for 10.0.0.5 because it doesn't contain any IP SANs` | The name or address you dial is not in the server certificate's SAN. Add it: `tls.names`, or `coddy tls ensure --name 10.0.0.5` (it issues the server certificate again). |
| `remote error: tls: bad certificate` / `certificate required` (the dialling side) | The server requires a client certificate and the one shown was missing or refused: not signed by an authority the server trusts (`coddy tls trust` on the server, then restart), expired, or **without the `clientAuth` usage**. |
| A certificate handshakes and every call is a `401` | The chain is fine and **no token** was sent: a certificate is not a credential. Send the shared-model token, the relay client token or the main token. |
| A node answers, but the web UI does not load in a browser | `require_client_cert` (or `client_ca_file`) and the browser has no certificate. Do not require one on a node that people open in a browser. |
| `coddy --remote` fails with a certificate error | It has no `ca_file`: the authority has to be in the system's store, or `SSL_CERT_FILE` has to name it (Linux, BSD). With a client CA on the server it cannot connect at all. |
| `tls: failed to parse private key` | The key has a passphrase or is not PEM. Convert it (above). |
| `tls: private key does not match public key` | The two files are not a pair. Compare `openssl x509 -in c.crt -noout -pubkey` with `openssl pkey -in k.key -pubout`. |
| Everything worked until the renewal | A listener reads its pair at start: restart `coddy serve` after a renewal. A dialling side's pair needs no restart. |
| `coddy tls trust` says the file is a private key or not a CA | Trust takes a **CA certificate** (what `coddy tls export` prints), never a key and never a leaf. |

## For agents: Codex, Claude Code, Coddy and others

This section is addressed to an AI agent that is setting up certificates for a person. The person can paste the block below into their agent's instructions.

> **Instructions for an agent that configures TLS for Coddy.**
> 1. **Ask first which scenario this is**: who serves, who dials, whether a certificate of the person's own exists already, and whether browsers open the node. Do not pick an authority yourself.
> 2. **Prefer the built-in certificates**: `coddy tls ensure` makes the CA and both certificates with no external tool, `auto: true` (and `tls_auto: true` on a provider) uses them, `coddy tls export` and `coddy tls trust` exchange the authorities of two machines. Use files of the person's own only when they have them.
> 3. **Never read, print, paste or log a private key.** Refer to key files by path only. Do not `cat` them, do not put them in a commit, a ticket or this conversation. Keys are created, and stay, on the machine that uses them; distribute **certificates and authorities, never keys**.
> 4. **Coddy reads no identity out of a certificate**: do not design anything that depends on a name or subject in a certificate. What a certificate holder may do is for a reverse proxy; the credential is a token.
> 5. **Write the configuration, then run `coddy -t --dry-run --config <file>` and `coddy tls status`** and read every finding. **Never "fix" a certificate error with `insecure_skip_verify`**, never by trusting an authority system-wide when `ca_file` or `tls_auto` would do.
> 6. **Do not touch the operating system's trust store, the browser's store, a service unit, or anything that needs `sudo`, without asking.** Say what you will change and why.
> 7. **A listener's pair and client authority are read at start**: tell the person that a restart is needed, and do not restart a service that other people use without asking.
> 8. **Report the outcome with evidence**: the command run, the line it printed, and what remains the person's to do (copy a CA certificate to the other machine, import it into a browser, renew before the date).

For a Coddy session, the bundled `configure-coddy` skill carries the same rules and the same table of keys; an agent configuring Coddy through it follows them without being told.

## The tests that hold this page

What this page says about the TLS stack and about Coddy's certificate functions is held by tests, so a change that makes it false breaks a build:

- **The authority**, [`internal/pki`](../../internal/pki/pki_test.go): the planner as a table (what is on disk and what is wanted give the steps; a valid CA is replaced only by force or expiry, a leaf only for a named reason, a second run does nothing), a crash at every write that the next run repairs, eight runs at once ending with one CA, and two machines trusting each other over a real mutual-TLS handshake. [`features/tls_builtin.feature`](../../features/tls_builtin.feature) runs the commands (`ensure`, `export`, `trust`, `issue client`) and a run start with `auto: true`.
- **The models**: `p7-pki-plan` (Promela: the installer, an update, a service start and an operator at once, with a death at every write, the world changing under them and a reader loading the pair), `p7-gate` (Promela: the credentials that remain against every route, and the latch that no input makes a certificate change an authorization decision) and the Petri net of the lock, in [`docs/plans/remote-model-provider-models`](../plans/remote-model-provider-models/).
- **In process**, [`internal/netx/certscenarios_test.go`](../../internal/netx/certscenarios_test.go) runs each scenario as a real handshake over loopback with the functions Coddy uses: a self-signed client certificate that is its own anchor; a self-signed server certificate trusted through `ca_file`; a private authority issuing both ends; an intermediate; a certificate with only `serverAuth` refused as a client certificate; expired and not-yet-valid certificates; a key with a passphrase refused; a client pair rotated on disk and used by the next handshake with no restart.
- **End to end**, [`examples/tls/tls_e2e_builtin.py`](../../examples/tls/tls_e2e_builtin.py) (`make test-tls-e2e`, `examples/tls/test_tls.sh`; it needs only `python3`) boots real `coddy` processes on four machines' worth of state: `coddy tls ensure` everywhere, `export` and `trust` between them, a relay and a node over the built-in certificates both requiring a client certificate, the borrower reaching the node directly and through the relay with a token (a certificate alone is a `401`; no certificate, or one of a CA nobody trusts, gets no connection), the borrower's own client (`coddy -t --dry-run` with `tls_auto`), and a renewal accepted by the running node.

A TLS interaction this page does not hold: ACME, a Microsoft authority and a browser's certificate store need the real thing, and are described from the documentation of those systems and of the code above, not exercised here.

## Where the other risks go

Coddy does one thing with TLS: the transport. The listener presents a certificate, a client trusts an authority and may present a certificate of its own, and, with `client_ca_file`, the
handshake refuses a peer whose certificate does not chain to that authority. **Coddy reads no identity out of a certificate**: no name, no subject, no class, no budget. A certificate is not
a credential; the credential is a token. Every other security risk is governed where it can be governed best, at the **L7 reverse proxy** in front of Coddy and in the **infrastructure** around it.

| Risk | Where it is governed |
|---|---|
| Who, by certificate subject, may call which route | the proxy (nginx `ssl_verify_client` with a `map` on `$ssl_client_s_dn`, Envoy RBAC on the authenticated principal, Traefik, Caddy) |
| Rate limits per client, connection caps, slow-client protection | the proxy (`limit_req` keyed by the client's subject, for one) |
| IP allowlists, WAF rules, bot and abuse control | the proxy and the network (a firewall, a security group, a mesh policy) |
| Public certificates, ACME, an enterprise PKI, revocation lists, OCSP | the proxy, or files named by hand in Coddy's settings (it reads them and does not care where they came from) |
| The audit of who connected | the proxy's access log |
| Isolation of a node or a relay | the network: a private segment, a firewall, a mesh |
| What Coddy keeps | tokens (`auth_token`, the shared-model tokens, a relay client's token) and the slots and windows that do not read a certificate |

A minimal shape: the proxy terminates TLS with a public or enterprise certificate, requires and checks a client certificate, decides by its subject, limits per client and forwards plain HTTP to
Coddy on a loopback or private address. Coddy keeps its own token check behind it and ignores whatever header the proxy adds. These are starting points, not part of Coddy's test suite:

```nginx
# nginx: mutual TLS, a decision by subject, a limit per client, an SSE-friendly forward to Coddy on loopback
limit_req_zone $ssl_client_s_dn zone=coddy_clients:10m rate=30r/m;
map $ssl_client_s_dn $coddy_client_allowed {
    default 0;
    "CN=ops-laptop,O=Example" 1;
}
server {
    listen 443 ssl;
    server_name coddy.example.com;
    ssl_certificate        /etc/nginx/tls/coddy.crt;
    ssl_certificate_key    /etc/nginx/tls/coddy.key;
    ssl_client_certificate /etc/nginx/tls/clients-ca.pem;
    ssl_verify_client on;
    location /coddy/llm/ {
        if ($coddy_client_allowed = 0) { return 403; }
        limit_req zone=coddy_clients burst=5 nodelay;
        proxy_pass http://127.0.0.1:12345;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_buffering off;        # the completions stream is Server-Sent Events
        proxy_read_timeout 1h;
    }
}
```

```
# Caddy: mutual TLS in front of Coddy, streaming not buffered
coddy.example.com {
    tls {
        client_auth {
            mode require_and_verify
            trusted_ca_cert_file /etc/caddy/clients-ca.pem
        }
    }
    reverse_proxy 127.0.0.1:12345 {
        flush_interval -1
    }
}
```

Through a proxy the node sees the proxy, not the end client: the shared-model limits that Coddy keeps (a slot and a window per token) count per token, and the per-client limits belong to the proxy. A
relay and its nodes follow the same rule: put the proxy in front of the relay, and keep the nodes on a private network.

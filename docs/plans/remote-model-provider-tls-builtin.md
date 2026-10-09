# Plan: TLS at the network and HTTP level only, with a built-in PKI (phase 4b of the remote model provider)

**Status: decided and in progress (2026-10-09).** It replaces the first proposal of phase 4b, [`remote-model-provider-cert-policy.md`](remote-model-provider-cert-policy.md), which added *more* certificate identity to the
application (`swarm.tls.full_cert_names`) and so went the wrong way. That record stays as written; it is withdrawn here.

## 1. The requirement

The operator's requirement, in the operator's words:

> TLS support should be provided on network and HTTP level, it should not has implementation on application protocol level. Coddy agent should create all needed certificates during
> installation, update, on demand. It should use build in technics and mechanizms. Other security risks should be govern on L7 reverse proxy and infrastructure level.

Read as four rules:

1. **TLS belongs to the transport and to HTTP.** The listener serves HTTPS, a client trusts a CA and may present a certificate, and the handshake decides who is admitted. Nothing in Coddy's handlers, in its wire protocol or in its authorization **reads the identity of a certificate**.
2. **Coddy makes the certificates it needs** at installation, at update and on demand, so that a working TLS setup needs no `openssl`, no copied recipe and no hand-written files.
3. **Built in:** the Go standard library (`crypto/x509`, `crypto/tls`), Coddy's own commands, its own files under `$CODDY_HOME`. No external tool, no service, no download.
4. **Everything else is for the reverse proxy and the infrastructure:** authorization by a certificate's subject, per-client rate limits, IP allowlists, a WAF, public certificates with ACME, the audit of who connected. Coddy documents the split and the handover; it does not grow those features.

## 2. What is "application protocol level" in the code today

Inventory of the branch (`git grep -n -i 'cert_names\|CertificateNames\|sharedCertName'`):

| Where | What it does | Verdict |
|---|---|---|
| `httpserver.shared_models.cert_names`, `EffectiveCertNames`, the class `mtls` of `authGate`, `sharedCertName` (`external/httpserver/tls.go`), the limiter key `mtls:<name>`, the class `mtls` of the counters and of the OpenAPI document | a certificate name is a credential, a class, a budget | **removed** |
| `swarm.clients[].cert_names`, `SwarmClient.HasCredential`, the certificate branch of `principalOf` (`external/swarm/principal.go`), "both required" | a certificate name stands for a relay client | **removed**; an entry is a bearer entry |
| `netx.CertificateNames` (`internal/netx/servercert.go`) | reads the DNS and URI names of the peer's chain | **removed** |
| `check_shared.go`, `internal/dryrun`, `jsondto.go`, `ui_schema_relay.go`, the schema, the form, `configure-coddy`, the tests, `features/shared_models_mtls.feature`, `examples/tls` | everything that names or tests the above | **removed or rewritten** |
| `httpserver.tls`, `swarm.tls` (`cert_file`, `key_file`, `client_ca_file`), `netx.ClientCertTLS` (a CA and the handshake) | the listener's certificate and the admission of a peer by its chain | **kept**: transport level |
| `swarm.node_tls`, `swarm.join[].dial`, `swarm.upstreams[].dial`, `providers[].ca_file` / `client_cert_file` / `client_key_file`, the certificate loader (`p5-certloader`) | trust and the client certificate presented at a handshake | **kept**: transport level |
| tokens (`auth_token`, `shared_models.tokens`, `swarm.clients[].token`), slots, windows, counters | authentication and limits that do not read a certificate | **kept**: not TLS |

Consequence for `client_auth`. With no identity read, a verified-but-unused client certificate is a knob that does nothing and misleads the operator, so `client_auth: optional` goes: **a client CA means the handshake requires a verified certificate**
(`required` stays accepted and is the default with a CA; `optional` is refused with the reason). Who may hold a certificate is the CA's business; which holder may do what is the proxy's.

## 3. The built-in PKI

A package `internal/pki` (pure planner plus an atomic writer) and a command `coddy tls`.

**Files** under `$CODDY_HOME/tls/` (mode 0700; keys 0600):

| File | What |
|---|---|
| `ca.crt`, `ca.key` | this machine's CA: ECDSA P-256, 10 years, `CA:TRUE` with a path length of 0, key usage `certSign`; **the CA key never leaves the machine** |
| `server.crt`, `server.key` | the certificate of this machine's listeners: ECDSA P-256, 1 year, `serverAuth`, the SANs below |
| `client.crt`, `client.key` | the certificate this machine presents (a join, an upstream, `node_tls`, a `coddy` provider): `clientAuth`, 1 year |
| `trusted/*.crt` | the CAs of other machines this one was told to trust (`coddy tls trust`) |
| `bundle.pem` | this CA and every trusted CA: what `ca_file` and `client_ca_file` point to in the built-in mode |

**Names.** The server certificate carries this host's name, `localhost`, `127.0.0.1`, `::1`, the address of each non-link-local interface, the host of every configured bind address, `advertise_url` and `remotes` entry, and `tls.names` (extra DNS names and IPs). A change in that set is a reason to issue again.

**Planner.** `pki.Plan(state, wanted, now)` is a pure function from what is on disk (parsed, never trusted) and what is wanted to a list of actions: create the CA; reissue a leaf that is missing, unparsable, not signed by the current CA, within 30 days of its end, or has a different SAN set; rebuild the bundle; do nothing. It never replaces a CA that is still valid (`--force-ca` does, and says that every peer must trust the new one), and never deletes a certificate before its replacement is in place.

**Writer.** Every file is written to a temporary name in the same directory and renamed; a pair is written key first, certificate second; all under one advisory lock so that the installer, the update, the service start and an operator running `coddy tls` at once do not interleave. A reader that meets a half-replaced pair is refused by the certificate loader and succeeds at the next handshake (`p5-certloader`).

**Trust between machines without moving a private key.** Machine A runs `coddy tls export > a-ca.crt`; machine B runs `coddy tls trust a-ca.crt` (and the other way). Each side then verifies the other's server and client certificates against `bundle.pem`. `coddy tls issue client NAME -o DIR` makes a client certificate for a machine that has no Coddy (a script, a proxy) and prints where the CA to trust is.

**Commands.**

| Command | Does |
|---|---|
| `coddy tls ensure [--quiet]` | what the planner says; idempotent; the one every automatic path calls |
| `coddy tls status [--json]` | the files, the names, the expiry, the trusted CAs |
| `coddy tls renew` | reissue the leaves now |
| `coddy tls export` | the CA certificate on stdout |
| `coddy tls trust FILE\|-` | add a peer's CA |
| `coddy tls issue client NAME [-o DIR]` | a client certificate for something else |

**Where it runs, which is what "installation, update, on demand" means.**

| When | How |
|---|---|
| Installation | `coddy serve install` calls `ensure` (`serve.EnsureCertificates`, whatever the configuration says, so that a later `auto: true` finds them; a failure is a warning); the package's post-install message names `coddy tls ensure` (the package runs as root and the certificates are the invoking user's, so it makes none); the install script (in the site repository) runs `coddy tls ensure` after it places the binary - **the line for the site repository**: `"$BIN" tls ensure --quiet \|\| true`, recorded here and published with the release. The unit carries **no** `ExecStartPre`: `coddy serve` itself makes what a block asks for at its start, so a unit step would make a CA on a machine that never asked for one |
| Update | `coddy update` runs `coddy tls ensure --if-used` with the **new** binary after a plain install (`update.Options.AfterInstall`): a machine whose `$CODDY_HOME/tls` exists or whose configuration asks for the built-in mode gets its leaves renewed, any other gets nothing. Not on Windows (a helper installs after exit) and not under a package upgrade (root); both are covered by the next start |
| On demand | `coddy tls ...`; and `coddy serve`, `coddy acp` and the console call `ensure` at their start when a block asks for the built-in mode, and `coddy serve` again on every configuration reload |

**Configuration (as built).** One new key, `auto: true`, on the blocks that already name certificate files: `httpserver.tls` and `swarm.tls` (the listener: `cert_file` and `key_file` default to `server.*`; `require_client_cert: true` makes `client_ca_file` default to `bundle.pem`, so the handshake requires a certificate of the bundle; there is no `client_auth`), `swarm.node_tls`, `swarm.join[].dial` and `swarm.upstreams[].dial`
(`ca_file` defaults to `bundle.pem`, `cert_file` and `key_file` to `client.*`), and `providers[].tls_auto` for a `coddy` row; plus a top-level `tls.names` for extra names (hidden from the settings form: a deployment key). A file named explicitly wins, per key. The files are **derived at use**, never at load and never stored: `Config.HTTPListenerFiles`, `SwarmListenerFiles`, `NodeDialFiles`, `DialFiles`, `ProviderClientTLS` (`internal/config/tls_builtin.go`) fill what a block left empty from `<home>/tls`, so a save of the configuration keeps `auto: true` and no path (a test holds it). `config.EnsureBuiltinTLS` makes what a block asks for; it runs at the start of `coddy serve`, `coddy acp` and the console, before each `Run` of the HTTP and swarm subsystems, and on every configuration reload. `coddy --dry-run` reports "not made yet" once (and skips what would read the files) or the findings of `pki.Status` (missing, ending within 30 days, a key that does not match).
A public certificate (ACME, an enterprise CA) is the same setup with the files named by hand: Coddy reads them and does not care where they came from.

## 4. What goes to the reverse proxy and the infrastructure

The documentation states the split once and the other pages link to it ([`docs/operate/security.md`](../operate/security.md), [`docs/operate/certificates.md`](../operate/certificates.md)):

| Risk | Where it is governed |
|---|---|
| Who, by certificate subject, may call which route | the proxy (nginx `ssl_verify_client` with a map on `$ssl_client_s_dn`, Envoy, Traefik, Caddy) |
| Rate limits per client, IP allowlists, WAF, bot control, slow-client protection, connection caps | the proxy and the network |
| Public certificates, ACME, enterprise PKI, revocation lists and OCSP | the proxy, or certificate files named by hand |
| The audit of who connected | the proxy's access log |
| Isolation of a node or a relay | the network: a private segment, a firewall, a mesh |
| What Coddy keeps | tokens (`auth_token`, shared-model tokens, relay clients) and the slots and windows that do not read a certificate |

The documentation gives a minimal proxy example for the shared-model route and the relay (TLS termination, an mTLS check with the subject mapped to a header or a refusal, a rate limit), and says plainly that Coddy ignores the header.

## 5. Models and tests (the verification programme)

- **`p7-pki-plan`** (Promela): the installer, the update, the service start and an operator running `ensure` at once, with a crash at every step: a valid pair is never replaced by an invalid one, a leaf is never left signed by a CA that is gone, the CA key is replaced only by `--force-ca`, `ensure` is idempotent (a second run does nothing), the lock is never held after an exit, a reader never serves a mismatched pair (with `p5-certloader`).
- **`p7-gate`** (Promela, from `p5-gate`): the credentials that remain (main token, shared token, cookie, nothing, the handshake admission by CA) against every route; the certificate class and its latches (G1, G6, G8 to G12, `cert`) are gone, and a latch says that **no input makes a certificate change an authorization decision**.
- Unit tests: the planner (a table of states), the writer (crash points by an injected failure), SAN computation, `ensure` idempotence, the commands; `coddy -t` and `--dry-run` on `auto`.
- BDD `features/tls_builtin.feature`: `coddy tls ensure` makes a pair, `coddy serve` with `httpserver.tls.auto` serves HTTPS, a client with `ca_file` from the bundle succeeds, a client without it fails; `swarm.tls.auto` with `required` admits a node that has the client pair and refuses one that has not.
- End to end (`examples/tls`): the same with a real `coddy serve`, **with no `openssl`**, and a script of the proxy handover.
- Heavy runs on `relay-huron`, the CPU kept under 60%.

## 6. Stages

| Stage | Work | Commit |
|---|---|---|
| T0 | This plan; the earlier proposal marked withdrawn; the verification plan and the main plan updated | docs |
| T1 | Remove the certificate identity from the application: config, gate, relay principal, `netx`, dry-run, schema, UI schema, skill, tests, features, docs; `client_auth: optional` refused; first the tests (red), then the removal | code + docs (done, 855f4ec1) |
| T2 | `internal/pki`: planner, writer, lock, SANs, bundle, trust; unit tests | code (done, 78b40ccc) |
| T3 | `coddy tls`: commands, usage, man page, completions, `usage_test.go` | code (done, 42fa651b) |
| T4 | `auto` on the four blocks and the provider, `ensure` at the start of a run, `coddy -t` and `--dry-run`; schema, UI schema, skill, `config.example.yaml`, `make docs` | code + docs (done) |
| T5 | Installation and update: `coddy serve install`, `coddy update`, the package message, `--if-used` | code + docs (done) |
| T6 | The models `p7-pki-plan` and `p7-gate`, each cross-reviewed twice, on `relay-huron` | docs |
| T7 | Documentation: `certificates.md` rewritten around `coddy tls`, the split of section 4 with proxy examples, `security.md`, `swarm.md`, `shared-models.md`, `AGENTS.md`, the e2e without `openssl` | docs + examples |

## 7. Risks

- **A removal in a branch that documents it.** `cert_names` is described in the phase 3 plan, the main plan and several reports. Those records are not rewritten; each gets a line saying it was removed in phase 4b.
- **A CA per machine means a manual exchange of CA certificates.** It is the price of never moving a private key; `export` and `trust` are one command each, and a fleet CA (one machine issues for the others) is a later option, not this stage.
- **A leaf near its end on a service that never restarts.** `ensure` runs at start, at update and on a configuration reload, and `coddy -t --dry-run` and `coddy tls status` warn 30 days ahead. A dialling side reads its pair at each handshake, so a renewed client pair is used at once; a listener reads its pair at start (as it does for files named by hand), so a renewed server pair takes a restart, which the unit's `ExecStartPre` makes routine. A daily renewal inside a long-running `coddy serve`, with a listener that reads its pair per handshake, is not built (open question 3).
- **The install script is in another repository.** The line it needs is recorded here; the in-repository paths (`serve install`, the unit, `update`) make a working setup without it.

## 8. Open questions

1. Should a fleet CA (one machine issues server and client certificates for the others) be offered, or is the per-machine CA with `export` and `trust` enough for the first release?
2. Is `tls.names` enough for the extra names, or should the planner also ask the configured `advertise_url`s only? (Built as both: the bind hosts and the `advertise_url` hosts of the configuration, and `tls.names`.)
3. Should a long-running `coddy serve` renew its own leaf (a daily planner run, the listener reading its pair per handshake through the certificate loader), or is renewal at start, update and reload enough? (Not built: a year-long uptime without an update is the case.)

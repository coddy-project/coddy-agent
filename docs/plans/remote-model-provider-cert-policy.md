# Plan: the relay's client-certificate policy by class (phase 4b of the remote model provider)

**Status: WITHDRAWN (2026-10-09), replaced by [`remote-model-provider-tls-builtin.md`](remote-model-provider-tls-builtin.md).** The operator's requirement, TLS only at the network and HTTP level with no certificate identity in the application, makes the key proposed here (`swarm.tls.full_cert_names`) the wrong direction. The record of the standards review (section 4) still stands. Original status: proposed, nothing implemented. Recorded from the operator's requirement and a review of OWASP and CWE sources. Two things are to be confirmed
before stage C1: the reading of "session forwarding" (2) and the shape of the key (4). It is the follow-up of the open question left by `p5-gate` ([report](remote-model-provider-models/p5-gate.md), "Open for the operator").

## 1. Requirement and goal

**The operator's requirement.** A client must still be able to connect to the relay **without a certificate when sessions are not forwarded**. The operator asked which security
requirements stand against a certificate-less path under `client_auth: required`, so that the exception is a documented decision and not an accident.

**Goal.** One relay serves both: token-only borrowers of shared models (scoped clients) with no certificate, and a **privileged class** (the full token: sessions, tools, settings,
node secrets) that can be made to require a verified certificate **without** `client_auth: required`, which is all-or-nothing at the handshake.

**Non-goals.** Removing or changing `required`. Changing the rules of a `swarm.clients` entry (token only, certificate only, both). Changing the node's own listener rules. A certificate rule per route inside the full class.

## 2. The reading of the requirement

"Session forwarding" is read as **the full class working through the relay**: `swarm.auth_token` against `/swarm/nodes/<node>/...` (sessions, tools, settings, the secrets a node's
`GET /coddy/config` returns) and the relay's own routes, as opposed to a `swarm.clients` entry, which opens only the shared-model routes (the listing, the usage of one alias, the
completions and the probe's ping) of the nodes it lists. **To be confirmed.**

## 3. Current state (verified in the code and the documentation)

- `swarm.tls.client_ca_file` makes the relay ask for a certificate; `client_auth` is `optional` (the default with a CA) or `required` (every peer without a verified certificate is refused at the handshake, **nodes that join and browsers included**). (`internal/config/swarm.go`, `docs/operate/swarm.md`, Client certificates)
- `principalOf` (`external/swarm/principal.go`): the **full class is the full token and nothing else**. A certificate that rides with it changes nothing, and a certificate never opens the full class. A `swarm.clients` entry is a bearer entry, a certificate entry, or needs both.
- The relay "is a fleet-wide door": the holder of the full token controls every node it reaches, its sessions, tools and settings included (`docs/operate/swarm.md`, Security). Nothing makes that class need a certificate unless the whole listener does.
- A node's own `httpserver.tls.client_auth: required` binds its own listener. A request that reaches the node through the relay (a tunnel carries no client certificate; a dialled node sees the relay's `node_tls` certificate) never shows the end client's certificate to the node: the relay is the choke point (`docs/features/shared-models.md`, "Not through an intermediary").

## 4. What the standards say, and what they do not

No source found forbids a service from accepting a client that has no certificate. They require that **every path to a protected function is authenticated, by any strong enough means, and
that the protection is the same on every path**. The certificate is named as one means, and as the expected one only at the highest level for service-to-service traffic.

| Source | What it says (paraphrased) | Consequence here |
|---|---|---|
| CWE-306, Missing Authentication for Critical Function | Divide the product into anonymous, normal, privileged and administrative zones, require proven identity for the privileged ones, and check that secondary and "assumed private" channels require it too. | The basis for the zoning of section 5: weak authentication for the narrow zone, strong for the privileged one. It names no mechanism. |
| CWE-288 and CWE-420, alternate path or channel | A protected primary channel with an unprotected alternate one to the same function is a weakness; use the same protections on all channels, or funnel access through one choke point. | The sharp edge of the open question: a node that requires a certificate on its listener and is reached through the relay (tunnel) without one. Answer: the relay is the single choke point and its policy is the documented one (5.3). |
| CWE-287, Improper Authentication (parent of the two above); CWE-923, Improper Restriction of Communication Channel to Intended Endpoints | The general class: an identity claim is not verified, or the channel is not tied to the intended endpoint. | Background. |
| OWASP ASVS 4.0, 9.2.3 (L2, L3) | Encrypted connections to external systems that carry sensitive information or functions must be authenticated. | Any authentication on top of TLS satisfies it. |
| OWASP ASVS 5.0, 12.3.5 (L3 only) | Services that communicate inside a system must use **strong, replay-resistant** authentication of each endpoint, such as TLS client authentication; a service mesh is suggested for certificate management. | The one place where a certificate is close to a requirement: only at level 3, and for service-to-service traffic. A static bearer token is a poor fit for "resistant to replay". |
| OWASP ASVS 5.0, 12.2 | Publicly reachable services need server TLS with publicly trusted certificates. | No client certificate is asked of public clients. |
| OWASP API Security Top 10 2023, API2 | APIs are weak where services do not authenticate each other, or where tokens are predictable or unchecked. | A token is acceptable if it is checked and cannot be guessed. |
| OWASP Transport Layer Security and Microservices cheat sheets | mTLS "should be considered" for high-value APIs, and its cost is key provisioning, revocation and rotation; tokens over TLS are a legitimate alternative; every service enforces access to its own protected operations, internal calls included. | Recommendations, not a prohibition. They support strong authentication for the high-value class and justify tokens for the narrow one. |
| DevSecOps (the OWASP DevSecOps Guideline and the like) | Process guidance that points at the standards above. **No normative requirement about client certificates was found there.** | Cannot be cited for or against. |

**Where "not allowed" really holds.** (a) The full class authenticated by a static bearer token alone, on a door that reaches every node (CWE-306 privileged zone, and the
replay-resistance rationale of ASVS 5.0 12.3.5 if level 3 is claimed). (b) A node whose own listener demands a certificate while the same functions are reachable through
the relay without one (CWE-288, CWE-420), if the relay's policy is not the documented, single one. **Where it does not hold:** a scoped shared-model client with a token over TLS: its reach is
four routes of the nodes it lists, it has slots, a window and counters, and its token is revoked on the next request.

The level of the standard the operator is audited against decides how much of this is binding. At ASVS level 3 the replacement of a certificate by a token between services of one
system needs a written justification; below it, 9.2.3 is met by the token.

## 5. Design proposal

### 5.1 Zoning at the relay

The scoped class and the rules of a `swarm.clients` entry are unchanged: a bearer entry still works **without a certificate** under `client_auth: optional`.

For the full class, one optional key:

```yaml
swarm:
  tls:
    client_ca_file: /etc/coddy/client-ca.pem
    client_auth: optional                 # unchanged: scoped clients and nodes may come without a certificate
    full_cert_names: [ops-laptop.example] # new: DNS or URI names that may act with the full token; empty = today
```

With `full_cert_names` set, the full class is **the full token AND a verified certificate whose name is listed**, judged on every request against the live list (the rule of an entry with both,
and the per-request identity of `principalOf`): a full token with no certificate, or with an unlisted one, proves nothing at the full level, and a certificate alone still never opens it.
The key needs `client_ca_file`; it is meaningful under `optional` (the point) and allowed under `required`. Empty or absent is the behaviour of today, byte for byte.

### 5.2 What it affects

Affected: the full token on `/swarm/nodes/...`, the aggregated sessions, the settings routes, `GET /swarm/stats`, and the web UI of the relay (a browser then needs a certificate installed,
which the documentation says). Not affected: scoped clients, a node's registration (pairing tokens), the probe's ping of a scoped client, the media capability.

### 5.3 The alternate channel made visible, not removed

The relay is the single choke point and its policy is written down: `docs/operate/swarm.md` and `docs/features/shared-models.md` say that the certificate policy of an end client is decided at the
relay, and that a node's own `client_auth: required` does not see it. `coddy -t` and `--dry-run` warn when a node has `httpserver.tls.client_auth: required` and is also reached through a relay or joins by
tunnel (`swarm.join`), and when a relay has a `client_ca_file`, `client_auth: optional`, no `full_cert_names` and a full token (an information line: the full class is token-only).

### 5.4 Sources of truth

`internal/config` (struct and validation), `internal/config/config.schema.json` and `UISchemaMap()` (the relay's Settings form carries `swarm.tls`), `configure-coddy/SKILL.md`, `docs/reference/config.md` (generated), `docs/operate/swarm.md` and
`docs/operate/certificates.md`, `config.example.yaml`; the schema is published to the site with the release (a new optional key is safe to publish at once).

## 6. Edge cases

- A name listed in `full_cert_names` and in an entry's `cert_names`: the entry rule is unchanged, and a certificate never opens the full class by itself.
- A certificate that expires on an open connection, a name removed from the list, a resumed connection: judged per request, like entries.
- A full token that is also a scoped token: refused at load already.
- `required` with `full_cert_names`: allowed; every peer has a certificate, the list decides which may act with the full token.
- A relay with `full_cert_names` and no `client_ca_file`: refused at load and by `coddy -t` (no certificate can ever match, the full class would be unreachable).
- An operator locking themselves out: `coddy -t` names the cause; the key is empty by default.

## 7. Tests and models (the verification programme)

- **Model `p6-relay-principal`** (Promela, `mcd`): the decision over bearer (none, full, scoped, wrong) x certificate (none, listed for an entry, listed for the full class, both, unlisted, expired, not verifying) x mode (none, optional, required) x route class (shared-model mount, any other mount, the relay's own routes, stats) x configuration bits. Latches: a full request without a listed certificate is not full when the list is set; **a scoped token-only client is served on its shared-model routes without a certificate** (the requirement); a certificate alone never opens the full class; a certificate never lowers a credential; the empty list is today's behaviour. Mutants for each. Cross-reviewed twice, as the other models; heavy runs on `relay-huron` after a capacity check.
- **BDD** `features/swarm_full_cert.feature`, the happy path: with the list set, a full token with a listed certificate is full, a scoped token alone is served, a full token alone is refused.
- **Unit tests** for the edges of section 6 next to `external/swarm`, `internal/config` and `internal/dryrun`.
- **End to end** in `examples/tls` with real `openssl` and `coddy serve`: a scoped client without a certificate, the full client with and without one.

## 8. Stages

| Stage | Work |
|---|---|
| C0 | Confirmation of section 2 and of the shape of the key (section 10). |
| C1 | `p6-relay-principal`, its two cross-review rounds, its report. |
| C2 | Config key, validation, schema, UI schema, skill, documentation, `make docs`, `make site-schema` (with the release). |
| C3 | `principalOf`, BDD and unit tests, first red. |
| C4 | The warnings of `coddy -t` and `--dry-run`, with tests. |
| C5 | End to end in `examples/tls`; documentation of the browser case. |

## 9. Risks

- A browser user of the relay's web UI must install a client certificate once the list is set: documented, and off by default.
- The reading in section 2 may be wrong: C0 exists for that, before any model is written.
- The key is a second certificate list next to `swarm.clients[].cert_names`; the documentation must say which is which (a list for the full class, an entry per borrower).

## 10. Open questions

1. Is "session forwarding" the full class through `/swarm/nodes/...` and the relay's own routes (section 2)?
2. A list of names (`full_cert_names`) or a boolean "the full class needs any verified certificate"? The list follows the idiom of `cert_names` and needs no second key.
3. Should the direct listener (`httpserver.tls`) get the same for its main token? Today the main token is token-only there and `cert_names` opens only the shared-model routes.
4. Which ASVS level is the operator audited against? Level 3 makes 12.3.5 binding for service-to-service traffic.

## 11. Sources

- CWE-306: <https://cwe.mitre.org/data/definitions/306.html>, CWE-287: <https://cwe.mitre.org/data/definitions/287.html>, CWE-288: <https://cwe.mitre.org/data/definitions/288.html>, CWE-420: <https://cwe.mitre.org/data/definitions/420.html>, CWE-923: <https://cwe.mitre.org/data/definitions/923.html>
- OWASP ASVS 4.0, V9: <https://github.com/OWASP/ASVS/blob/master/4.0/en/0x17-V9-Communications.md>; ASVS 5.0, V12: <https://github.com/OWASP/ASVS/blob/v5.0.0/5.0/en/0x21-V12-Secure-Communication.md>
- OWASP API Security Top 10 2023, API2: <https://api-security.owasp.org/editions/2023/en/0xa2-broken-authentication>
- OWASP cheat sheets: Transport Layer Security <https://cheatsheetseries.owasp.org/cheatsheets/Transport_Layer_Security_Cheat_Sheet.html>, Microservices Security <https://cheatsheetseries.owasp.org/cheatsheets/Microservices_Security_Cheat_Sheet.html>

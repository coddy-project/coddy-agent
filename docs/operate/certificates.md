# Certificates and TLS: a field guide

Coddy speaks TLS in several places, and each place wants a different piece of key material from a different kind of authority. This page says, for each situation you are likely to be in, which files to make, who keeps them, which keys to set, and how to check that it works. It is written for the person who runs the relay, the node or the borrowing Coddy, and for the agent (Codex, Claude Code, Coddy itself, or another) that is doing the typing: section 10 is addressed to the agent.

Everything here is **opt-in**. A relay or a node with no certificate settings speaks plain HTTP, which is right on a loopback address, behind a reverse proxy that terminates TLS, or inside a tunnel. The settings below are for the other cases: you want Coddy itself to terminate TLS, to ask its callers for a certificate, or to present one.

## 1. The picture

Six legs use TLS. For each, one side **serves** (it has a certificate and a key) and the other **dials** (it checks the certificate it is shown, and may show one of its own).

| Leg | The server side | The dialling side | Keys |
|---|---|---|---|
| A borrowing Coddy to a node or a relay mount | `httpserver.tls` (node), `swarm.tls` (relay) | the provider row of type `coddy` | `providers[].ca_file`, `client_cert_file`, `client_key_file` |
| A node joining a relay | `swarm.tls` | the join entry | `swarm.join[].dial.ca_file`, `dial.cert_file`, `dial.key_file` |
| The relay to a node that registered itself over an address | `httpserver.tls` on the node | the relay | `swarm.node_tls.ca_file`, `cert_file`, `key_file` |
| The relay to a node or a child relay it lists by hand | `httpserver.tls`, `swarm.tls` | the relay | `swarm.upstreams[].dial.*` |
| A browser to the web UI of a node or a relay | `httpserver.tls`, `swarm.tls` | the browser | the browser's own stores |
| The console and ACP with `--remote` | `httpserver.tls`, `swarm.tls` | `coddy --remote` | none: the system's roots only, and no client certificate |

What a **server** asks of its callers is set in the same block as its own certificate: `client_ca_file` is the authority that client certificates are checked against, `client_auth` says whether a caller without one is let in (`optional`) or refused at the handshake (`required`). What a verified certificate is **worth** is a separate list of names: `httpserver.shared_models.cert_names` on a node, `swarm.clients[].cert_names` on a relay.

## 2. What Coddy reads: the rules that decide everything

These are facts about the code, not preferences. Most trouble comes from one of them.

- **PEM files only.** A certificate file is PEM: the **leaf first**, then its intermediates (a "full chain"). A key file is one PEM private key: `PRIVATE KEY` (PKCS#8), `RSA PRIVATE KEY` or `EC PRIVATE KEY`. DER, PKCS#12 (`.pfx`, `.p12`), Java keystores and keys held in a hardware token or the operating system's store are **not** read; section 4 shows the conversions.
- **No passphrases.** A private key with a passphrase cannot be used: Coddy has nowhere to ask for it, and says `tls: failed to parse private key`. Section 5 is about that.
- **Names are the DNS and URI entries of the Subject Alternative Name**, compared exactly with the entries of `cert_names`. The CN, an e-mail address and a user principal name (the `otherName` of an Active Directory certificate) are **never read**: a certificate with only those verifies and has no name, so nothing in `cert_names` can match it.
- **Key usages are enforced by the TLS stack.** A server checks that a client certificate has the `clientAuth` extended key usage (or none at all); a client checks that a server certificate has `serverAuth` (or none). A certificate from a public certificate authority is made for servers and, increasingly, has `serverAuth` only: it cannot be a client certificate (section 7.6).
- **The host name is checked against the SAN of the server certificate**, never the CN. A server reached by an address (`https://10.0.0.5:12346`) needs an **IP** entry in its SAN; one reached by a name needs that name (a wildcard covers exactly one label).
- **`ca_file` replaces the system's roots for that leg.** With it, only the authorities in the file are trusted for that connection; without it, the system's roots are. A file may hold several certificates, and a **self-signed certificate is its own authority**: listing the file of a self-signed server certificate in `ca_file` is how a client trusts it.
- **The system's roots are the operating system's.** On Linux that is the distribution's bundle (`/etc/ssl/certs`, `SSL_CERT_FILE` and `SSL_CERT_DIR` are honoured); on macOS the keychain's trust settings; on Windows the certificate store, enterprise roots pushed by a policy included. A private authority installed there is trusted by every program of the machine, which is why `ca_file` is the narrower choice.
- **There is no revocation check.** Neither a CRL nor OCSP is consulted. A certificate is revoked on this side by removing its name from `cert_names` or its authority from `client_ca_file`, or by letting it expire: use short lifetimes.
- **TLS 1.2 is the minimum**, and an HTTP/2 or HTTP/1.1 handshake is negotiated; a node's own listener serves HTTP/1.1 only, so that a shared call keeps a connection of its own ([the liveness bound](../features/shared-models.md#a-peer-that-vanishes)).

**When each file is read.** The server's own pair and its `client_ca_file` are **startup state**: changing them takes a restart of `coddy serve`. A dialling side's pair (`client_cert_file`, `dial.cert_file`, `node_tls.cert_file`) is read **at every handshake** and cached by size and modification time, so a renewed certificate is used by the next connection with no restart; a changed `ca_file` is picked up when the leg's transport is next built (a `coddy` row's next call; a join at its next start). Names are read from the live configuration on every request, so removing one from `cert_names` takes effect at once.

## 3. Which section is yours

| You have | Read |
|---|---|
| A self-signed certificate for a client, to be accepted by a relay or a node | 7.1 |
| A self-signed certificate for a relay or a node, to be trusted by its callers | 7.2 |
| Nothing yet, and you want to run your own authority (the relay's operator as the CA of its clients) | 7.3 |
| A company wildcard certificate (`*.corp.example`) | 7.4 |
| A personal certificate (issued to a person: e-mail, S/MIME) | 7.5 |
| A certificate you bought from a public authority | 7.6 |
| A Microsoft certificate authority (Active Directory Certificate Services) | 7.7 |
| Let's Encrypt or another ACME authority | 7.8 |
| A private ACME authority, Vault, cert-manager, mkcert | 7.9 |
| A tunnel, a VPN or a reverse proxy, and no certificates in Coddy at all | 7.10 |

## 4. The toolbox

All of this is plain `openssl` (3.x; the same flags work in 1.1.1) and is what the end-to-end test of this page runs. Put key material in a directory only the account that runs Coddy can read, and do the commands on the machine that will **use** a key: a private key is created where it stays and never travels.

**A private authority** (a key that stays with whoever issues, and a certificate that is handed out):

```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout ca.key -out ca.crt -days 825 -subj "/CN=Coddy private CA" \
  -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
  -addext "keyUsage=critical,keyCertSign,cRLSign"
chmod 600 ca.key
```

**A leaf signed by it.** The extensions are what Coddy and the TLS stack read; write `SAN` and `EKU` for the case:

```bash
name=acme                      # the file name
SAN="URI:urn:coddy:client:acme" # DNS:relay.example,IP:10.0.0.5 for a server; a URI or a DNS name for a client
EKU=clientAuth                  # serverAuth for a server certificate

openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout $name.key -out $name.csr -subj "/CN=$name"
printf "subjectAltName=%s\nextendedKeyUsage=%s\nbasicConstraints=CA:FALSE\nkeyUsage=digitalSignature\n" "$SAN" "$EKU" > $name.ext
openssl x509 -req -in $name.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -days 90 -extfile $name.ext -out $name.crt
chmod 600 $name.key
```

**A self-signed leaf** (the certificate is its own authority; the peer that trusts it lists this very file):

```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout self.key -out self.crt -days 90 -subj "/CN=self" \
  -addext "subjectAltName=DNS:self.example,IP:10.0.0.5" \
  -addext "extendedKeyUsage=serverAuth" \
  -addext "basicConstraints=critical,CA:FALSE"
chmod 600 self.key
```

**Look at what you have**, before you put it in a configuration:

```bash
openssl x509 -in acme.crt -noout -subject -issuer -enddate -ext subjectAltName,extendedKeyUsage
openssl verify -CAfile ca.crt -purpose sslclient acme.crt     # sslserver for a server certificate
openssl x509 -in acme.crt -noout -fingerprint -sha256         # to compare two copies of the same file
```

**Convert** what other tools hand you:

```bash
openssl x509 -inform der -in server.cer -out server.crt                       # a DER or .cer certificate
openssl pkcs12 -in bundle.pfx -clcerts -nokeys -out leaf.crt                  # the leaf of a .pfx
openssl pkcs12 -in bundle.pfx -cacerts -nokeys -out chain.crt                 # its chain
openssl pkcs12 -in bundle.pfx -nocerts -nodes -out leaf.key                   # its key, unencrypted (add -legacy for an old .pfx)
cat leaf.crt chain.crt > fullchain.pem                                        # the leaf first, then the intermediates
openssl pkey -in enc.key -out plain.key                                       # remove a passphrase (it prompts)
openssl pkcs12 -export -inkey acme.key -in acme.crt -certfile ca.crt \
  -name "Coddy acme" -out acme.p12                                            # for a browser or the system's store (it prompts for a password)
```

Do not put a passphrase on the command line (`-passin pass:...`) outside a throwaway test: it is in the shell history and in `ps`. Use `-passin env:NAME`, `-passin file:path`, or let it prompt.

## 5. Passwords and private keys

There are two different passwords in this story, and they behave differently.

- **A passphrase on a private key** (`ENCRYPTED PRIVATE KEY`, or `Proc-Type: 4,ENCRYPTED` in the file). **Coddy cannot use it**, anywhere. It would have to be asked for at every handshake, from a process that runs unattended. Remove the passphrase on the machine that will use the key (`openssl pkey -in enc.key -out plain.key`) and protect the plain file the way you protect any secret: mode `0600`, owned by the account that runs `coddy serve`, in a directory no one else can list, never in a repository, never in an image layer. On a service manager, prefer its credentials store (systemd `LoadCredential=` puts a file in `$CREDENTIALS_DIRECTORY`, readable by the service only) or a `tmpfs` that a deploy step fills from your secret manager at start; point the key at that path. Hardware tokens, smart cards, the TPM and cloud HSMs are not supported: the key must be a file.
- **The export password of a `.p12` / `.pfx`** is for moving a certificate and its key as one file, to a browser or to the operating system's store, which ask for it on import. Coddy does not read a `.pfx`: convert it first (section 4) and delete the intermediate files. Choose the export password freely; it protects the file in transit only.

A passphrase typed in a configuration file would be worse than no passphrase: there is no key in Coddy's configuration for one, and `coddy -t` refuses an encrypted key with the line to look for (section 9).

## 6. Where to install what: Coddy, the system, the browser

| Material | Coddy | The system's store | A browser |
|---|---|---|---|
| **The authority that signed a server's certificate** (the CA, or the self-signed certificate itself) | **`ca_file`** of the leg that dials (`providers[].ca_file`, `dial.ca_file`, `node_tls.ca_file`): narrow, per leg, lost with the file | works too, for every program of the machine, `--remote` included; broad, and a private authority there can vouch for any site | import it once to get the lock icon, or accept the warning |
| **A client certificate and its key** | **two PEM files** named in the configuration; this is the only way Coddy reads one | **not used**: installing a client certificate in the system's or a browser's store does **nothing** for Coddy | needed in the browser only when a server **requires** one at the handshake (`client_auth: required`); import the `.p12` |
| **The authority that signed client certificates** (what a server checks them against) | `client_ca_file` of the server, a file | not used | not used |

Three consequences:

- **A borrowing Coddy, a node, a relay: files.** Nothing about Coddy's own client certificates goes through the keychain or the certificate store of the workstation, on any operating system.
- **A browser** only needs a client certificate when a server refuses callers without one. The web UI signs in with a token or a password, not a certificate (`cert_names` opens the shared-model routes and nothing else), so on a node whose web UI people use, set `client_auth: optional`: a browser is let in, and may show a certificate picker if one is installed. Use `required` only on a node that no browser opens.
- **`coddy --remote` and `coddy acp --remote`** have no option for a client certificate or a private authority. Under `required` they cannot connect at all; with `optional` they connect with a token. To trust a private authority they use the system's roots: install the authority there, or start them with `SSL_CERT_FILE=/path/to/ca.crt` on Linux and BSD (macOS and Windows use their own stores).

## 7. The scenarios

### 7.1 A self-signed certificate for a client, imported to a relay or a node

The simplest private arrangement for one or two borrowers: no authority at all. The client makes a self-signed certificate (section 4, with `extendedKeyUsage=clientAuth` and a name in the SAN), keeps the key, and **sends the certificate file** (never the key) to whoever runs the relay or the node. That operator appends the file to the server's `client_ca_file` bundle and lists the name:

```yaml
swarm:
  tls:
    cert_file: /etc/coddy/relay.crt
    key_file: /etc/coddy/relay.key
    client_ca_file: /etc/coddy/clients.pem     # holds acme-self.crt (one or more self-signed certificates, and any authorities)
    client_auth: optional
  clients:
    - name: acme
      scope: shared_models
      nodes: [workstation]
      cert_names: ["acme.example"]              # the name inside acme-self.crt
```

Because the certificate is its own authority, **only that file is accepted**: another self-signed certificate with the very same name is not, and the name proves nothing without the anchor. To remove the client, delete its certificate from the bundle (a restart: `client_ca_file` is startup state) and its name from `cert_names` (at once). Renewing means sending a new file; there is no chain to keep.

The borrower's side, with a certificate and key made for the purpose:

```yaml
providers:
  - name: workstation
    type: coddy
    api_base: https://relay.example:12346/swarm/nodes/workstation
    client_cert_file: /home/me/.coddy/tls/acme-self.crt
    client_key_file: /home/me/.coddy/tls/acme-self.key
```

### 7.2 A self-signed certificate for the relay or the node, trusted by its callers

Make a self-signed server certificate (section 4, `serverAuth`, **with every name and address the callers use in the SAN**, an IP entry for an address). The server's operator uses it as `cert_file` and `key_file` and **sends the certificate file** to each caller. Each caller trusts exactly that file:

```yaml
providers:
  - name: workstation
    type: coddy
    api_base: https://10.0.0.5:12345
    ca_file: /home/me/.coddy/tls/workstation.crt   # the self-signed certificate itself
swarm:
  join:
    - url: https://10.0.0.5:12346
      pairing_token: "${CODDY_SWARM_PAIRING_TOKEN}"
      dial: { ca_file: /etc/coddy/relay.crt }
```

A browser does not trust it until it is imported or the warning is accepted; `coddy --remote` needs the system's store or `SSL_CERT_FILE` (section 6). Renewing means redistributing the certificate to every caller, which is why this scales to a handful of callers and no further; past that, run an authority (7.3).

### 7.3 The relay's operator as the authority of its clients

The relay (or the machine of whoever runs it) becomes a small private certificate authority: it signs the relay's own certificate, every node's, and every borrower's. Callers then trust **one file** (`ca.crt`), and a new borrower is a new leaf, not a new file on every server.

1. **Make the authority** (section 4). Keep `ca.key` **off the relay** if you can: on an administrator's workstation or an offline machine. The relay needs only `ca.crt`. If a single machine does everything, keep the key `0600`, in a directory of its own.
2. **Sign the relay's certificate** with `SAN=DNS:relay.example,IP:10.0.0.5`, `EKU=serverAuth`.
3. **Sign a leaf per borrower and per node**, `EKU=clientAuth`, with a **URI or DNS name that identifies them**: `urn:coddy:client:acme`, `nas02.example`. These are the strings of `cert_names`.
4. **Hand out** `ca.crt` (public) to every caller, and each leaf and its key **to its owner only**.
5. **Configure** the relay and the nodes:

```yaml
# the relay
swarm:
  tls:
    cert_file: /etc/coddy/relay.crt
    key_file: /etc/coddy/relay.key
    client_ca_file: /etc/coddy/ca.crt
    client_auth: optional           # required, if every caller of the relay has a certificate and no browser opens it
  node_tls:                         # what the relay presents to nodes that ask for a certificate
    ca_file: /etc/coddy/ca.crt
    cert_file: /etc/coddy/relay-client.crt
    key_file: /etc/coddy/relay-client.key
  clients:
    - name: acme
      scope: shared_models
      nodes: [nas02]
      cert_names: ["urn:coddy:client:acme"]

# a node
httpserver:
  tls:
    cert_file: /etc/coddy/nas02.crt
    key_file: /etc/coddy/nas02.key
    client_ca_file: /etc/coddy/ca.crt
    client_auth: required            # the relay is the only caller of this node
swarm:
  join:
    - url: https://relay.example:12346
      name: nas02
      pairing_token: "${CODDY_SWARM_PAIRING_TOKEN}"
      advertise_url: https://nas02.example:12345
      token: "${CODDY_SHARED_MODELS_TOKEN}"
      dial: { ca_file: /etc/coddy/ca.crt, cert_file: /etc/coddy/nas02-client.crt, key_file: /etc/coddy/nas02-client.key }
```

6. **Revoke** by removing a name from `cert_names` (immediately) and by letting short-lived leaves expire; there is no CRL. Choose lifetimes you can renew: 90 days for leaves, years for the authority.

The end-to-end test of this page builds exactly this arrangement and drives it ([section 11](#11-the-tests-that-hold-this-page)).

### 7.4 A company wildcard certificate

A wildcard (`*.corp.example`) issued by the company's authority is a **server** certificate, and a good one when the relay or a node has a name under that domain: use the full chain as `cert_file` and its key as `key_file`. Callers whose machines already trust the company's root (a managed laptop does) need **no `ca_file`**: the system's roots are enough, which is the point of a corporate root.

Three cautions.

- **It covers one label.** `relay.corp.example` and `node.corp.example` match; `a.b.corp.example` and `corp.example` do not.
- **Do not use it as a client identity.** `cert_names` compares strings, so the name of a wildcard certificate is the literal `*.corp.example`, shared by everyone who holds the key. It cannot tell two clients apart, and the key on many machines is one leak away from being every one of them. Give each client a certificate of its own (7.3, 7.7).
- **The key is in many places.** A wildcard key copied to every relay and node is a wide secret. Prefer one certificate per host if the company's authority makes that easy.

### 7.5 A personal certificate

A personal certificate (issued to a person: an e-mail address, a name, often the S/MIME or "user" template of a company's authority) **verifies and has no name Coddy can use**, because its identity is an e-mail address or a user principal name, which Coddy does not read (section 2). Two ways to use a person as a client:

- **Have the authority issue a certificate with a URI or DNS name** for that person (`urn:coddy:user:alice`), through a template that allows a name to be supplied; then use that URI in `cert_names`. Section 7.7 has the Microsoft version.
- **Make the person's certificate yourself** from the private authority of 7.3. This is the normal route, and it keeps the identity of a Coddy client out of the mail system.

If the personal certificate has to live in the browser or the system's store as well (to sign in to something else), that does not make it usable by Coddy: Coddy reads only the two PEM files (section 6). Export a copy with `openssl pkcs12` (section 4) only if you mean to use this certificate for Coddy, and protect the key file like any other.

### 7.6 A certificate you bought from a public authority

A purchased TLS certificate (DV, OV or EV) is a **server certificate for a name you own**. Use it as the `cert_file` of a relay or a node that has a public name: the **full chain** the authority sends (leaf, then intermediates) and the key you generated when you ordered it. Callers need no `ca_file`: the public roots are in the system's store.

- **It cannot be a client certificate.** Public authorities issue certificates for servers, and have been dropping the `clientAuth` key usage from them (the browser root programs set 2026 as the year). Check with `openssl x509 -noout -ext extendedKeyUsage`: with only `TLS Web Server Authentication`, a server that asks for a client certificate refuses it. Clients get certificates from a private authority (7.3). The end-to-end test shows the refusal.
- **Renewal is a restart.** The server's pair is read at start: install the renewed files and restart `coddy serve`. Validity is getting shorter (the maximum has been falling towards a few weeks), so script it (7.8).
- **The name must be the one callers use**, and a `*.example.com` certificate must cover it (7.4).

### 7.7 A Microsoft certificate authority (Active Directory Certificate Services)

An enterprise with AD CS can issue everything this page needs, with three points of care.

- **The authority's chain, as PEM.** Callers on Windows machines in the domain already trust the enterprise root through the store, so a dialling Coddy there needs no `ca_file` for a certificate that chain issued. A server's `client_ca_file` needs the chain as a **Base-64 encoded X.509** file: download it from the authority's web enrolment page (*Download a CA certificate, certificate chain, or CRL*, encoding Base64), or `certutil -ca.cert root.cer` then `openssl x509 -inform der -in root.cer -out root.pem`; put the root **and the issuing authority** in the file.
- **Pick a template that carries a DNS or URI name and the right key usage.** The built-in **Web Server** template (`serverAuth`) and a copy of **Computer** (both `serverAuth` and `clientAuth`, the machine's DNS name in the SAN) serve servers and machine clients. The built-in **User** template names the person by user principal name and e-mail only: Coddy reads neither (7.5). A client certificate for a person needs a **custom template** that allows the requester to supply the subject alternative name (a URI or a DNS name) and the `clientAuth` usage.
- **The key must be exportable.** Auto-enrolled certificates normally keep a non-exportable key inside the Windows store, and Coddy cannot read a key from there. Request with a policy that allows the private key to be exported (`Exportable = TRUE` in a `certreq` INF, or *Allow private key to be exported* on the template), export a `.pfx`, and convert it (section 4). Then remove the pair from the store if it has no other use.
- **The authority's revocation is not consulted.** AD CS publishes a CRL; Coddy does not read it. Revoke on the Coddy side (remove the name) as well as at the authority.
- **Paths on Windows** go in the YAML with forward slashes or in single quotes: `cert_file: 'C:\ProgramData\coddy\relay.crt'`.

### 7.8 Let's Encrypt and other ACME authorities

ACME gives a **server** certificate for a public name, free and automated; it does not give client certificates (7.6). For a relay or a node with a public DNS name:

```bash
certbot certonly --standalone -d relay.example          # port 80 must reach this host for the HTTP-01 challenge
# or, for a wildcard or a host that is not reachable from the internet:
certbot certonly --manual --preferred-challenges dns -d '*.corp.example'     # DNS-01, with your DNS provider's plugin in practice
```

```yaml
swarm:
  tls:
    cert_file: /etc/letsencrypt/live/relay.example/fullchain.pem
    key_file: /etc/letsencrypt/live/relay.example/privkey.pem
```

- **Renewal needs a restart of Coddy**, because the server's pair is read once at start. Let certbot do it: `certbot renew --deploy-hook "systemctl --user restart coddy"` (or the unit your service runs under). A restart is a few seconds of refused connections and ends the calls in flight, so renew where that is acceptable, or put the ACME client in front (below).
- **The account that runs Coddy must be able to read the key.** Files under `/etc/letsencrypt/live` are `0600` and owned by root; copy them in the deploy hook to a directory Coddy's account owns, or give a group access, rather than running Coddy as root.
- **Callers need no `ca_file`**: the system trusts Let's Encrypt.
- **An alternative that renews without a restart**: terminate TLS in a reverse proxy that does ACME by itself (Caddy, Traefik, nginx with an ACME module) and proxy to Coddy on loopback over plain HTTP. The cost: the proxy, not Coddy, sees the client certificate, so `cert_names` and `client_ca_file` do nothing behind it (a terminator in front carries none); require client certificates in the proxy, or use tokens.
- **Staging first.** Use the authority's staging environment while you test: the production one rate-limits failures.

### 7.9 A private ACME authority, Vault, cert-manager, mkcert

When you want automation **and** client certificates, run an authority that speaks ACME or an API and issues `clientAuth`:

- **A private ACME authority** (Smallstep `step-ca` and similar) issues short-lived server **and** client certificates, renewed by a small agent next to each Coddy. Client pairs are re-read at every handshake, so their renewal needs no restart; a server's renewal does (7.8's hook).
- **HashiCorp Vault's PKI engine** issues leaves with a TTL and the names you ask for; render the files with Vault Agent templates and restart on change.
- **Kubernetes with cert-manager** writes `tls.crt`, `tls.key` and `ca.crt` into a Secret you mount as files; roll the pod when it renews (a reloader does it).
- **mkcert** makes a local authority and installs it in the system's and the browsers' stores: fine for a developer machine and `localhost`, wrong for anything shared (it puts an authority that can vouch for any site in the store).

In every case the files Coddy reads are the same PEM pairs and a CA bundle.

### 7.10 No certificates in Coddy at all

Sometimes the right answer is not to use Coddy's TLS: a **reverse proxy** that terminates TLS (and, if you want mutual TLS, enforces it) in front of a Coddy on loopback; a **VPN or an overlay network** (WireGuard, Tailscale) so that the relay is only reachable inside it; an **SSH tunnel**; a **cloud tunnel** that terminates TLS at the provider's edge. All of them leave `httpserver.tls` and `swarm.tls` unset, the bearer tokens and the scopes do the authorising, and Coddy sees only the proxy. State it as a decision: what you give up is mapping a certificate to a scoped client, which only a Coddy that terminates the TLS itself can do.

## 8. The settings of every setup, side by side

| You want | Server side | Dialling side |
|---|---|---|
| Encrypt a node's API, trust a private authority | `httpserver.tls.cert_file`, `key_file` | `providers[].ca_file` |
| Know a borrower by its certificate, on a node | `httpserver.tls.client_ca_file`, `client_auth`, `httpserver.shared_models.cert_names` | `client_cert_file`, `client_key_file` |
| Know a borrower by its certificate, on a relay | `swarm.tls.client_ca_file`, `client_auth`, `swarm.clients[].cert_names` | `client_cert_file`, `client_key_file` |
| A node that requires certificates, behind a relay | node: `httpserver.tls.client_ca_file`, `client_auth: required` | relay: `swarm.node_tls.cert_file`, `key_file`, `ca_file`; node's join: `dial.*` |
| Nodes that join a relay that requires certificates | relay: `swarm.tls.client_auth: required` | `swarm.join[].dial.cert_file`, `key_file`, `ca_file` |

[The reference of every key](../reference/config.md) has the types and the defaults. `coddy -t` reports what is wrong with a block at its line; `coddy -t --dry-run` loads each pair and each authority file, reads the expiry of a `coddy` row's client certificate (a warning inside two weeks, an error once past), and dials the remote.

## 9. Checking it, and what the errors mean

Check each leg from the outside before you trust it, then with Coddy's own tools:

```bash
# what the server shows, and whether it accepts your certificate (look for "Verification: OK" and the alert, if any)
openssl s_client -connect relay.example:12346 -servername relay.example -CAfile ca.crt \
  -cert acme.crt -key acme.key -verify_return_error </dev/null 2>&1 | sed -n '/Verification/p;/alert/p'

# the API with the certificate, no token
curl --cacert ca.crt --cert acme.crt --key acme.key https://relay.example:12346/swarm/nodes/nas02/coddy/llm/models

coddy -t --dry-run --config config.yaml       # every pair and bundle loads; each provider is dialled; the expiry is read
```

| What you see | Cause and fix |
|---|---|
| `x509: certificate signed by unknown authority` (the dialling side) | The server's certificate is not signed by anything in `ca_file` (or in the system's roots). Point `ca_file` at the authority's certificate, or at the self-signed certificate itself. |
| `x509: certificate is valid for A, not B` / `certificate is not valid for any names` | The name or address you dial is not in the server certificate's SAN. Reissue with the right `DNS:` or `IP:` entries; the CN is not read. |
| `tls: failed to parse private key` (`coddy -t`, `--dry-run`) | The key has a passphrase, or is not PEM (a `.pfx`, a DER file), or is not the key of that certificate. Remove the passphrase (section 5), convert (section 4), or use the right pair. |
| `tls: private key does not match public key` | The two files are not a pair. Compare `openssl x509 -in c.crt -noout -pubkey` with `openssl pkey -in k.key -pubout`. |
| `remote error: tls: bad certificate` / `certificate required` (the dialling side) | The server wants a client certificate and the one shown was refused or missing: not signed by `client_ca_file`, expired, or **without the `clientAuth` key usage** (a certificate bought for a web server). Check with `openssl verify -purpose sslclient`. |
| `certificate has expired or is not yet valid` | The clock of one side is wrong, or the certificate is past its dates. A client certificate is rechecked at every request. |
| A certificate handshakes and every call is a `401` | The chain is fine and **no name matches**: the SAN has no DNS or URI entry, or it differs by a character from the one in `cert_names`. Compare `openssl x509 -noout -ext subjectAltName` with the list. An e-mail or a user principal name is never read (7.5). |
| A node answers, but the web UI does not load in a browser | `client_auth: required` and the browser has no certificate. Use `optional`, or import one (section 6). |
| `coddy --remote` fails with a certificate error | It has no `ca_file`: the authority has to be in the system's store, or `SSL_CERT_FILE` has to name it (Linux, BSD); under `required` it cannot connect. |
| Everything worked until the renewal | The server's pair is read once at start: restart `coddy serve` after the files change. A dialling side's pair needs no restart. |
| The relay does not start, naming `swarm.node_tls` or `swarm.tls` | A file in the block cannot be read or is not a PEM pair; the message names the key. |

## 10. For agents: Codex, Claude Code, Coddy and others

This section is addressed to an AI agent that is setting up certificates for a person. The person can paste the block below into their agent's instructions.

> **Instructions for an agent that configures TLS or mutual TLS for Coddy.**
> 1. **Ask first which scenario this is** (the table in section 3 of `docs/operate/certificates.md`, or `coddy docs show operate/certificates` on a machine with Coddy): who serves, who dials, what kind of certificate exists already, and whether browsers open the node. Do not guess an authority.
> 2. **Never read, print, paste or log a private key or a passphrase.** Refer to key files by path only. Do not `cat` them, do not put them in a commit, a ticket or this conversation, and do not put a passphrase in a command line or a configuration file (Coddy has no key for one). Keys are created, and stay, on the machine that uses them.
> 3. **Create keys with the commands of section 4**, set the mode of every key to `0600` and its owner to the account that runs Coddy, and create the files in a directory only that account can read. If the key a person hands you has a passphrase, ask them to remove it on their machine (`openssl pkey -in enc.key -out plain.key`) rather than doing it where you can see the passphrase.
> 4. **Verify before you configure**: `openssl x509 -noout -ext subjectAltName,extendedKeyUsage -enddate` on every certificate, `openssl verify -purpose sslclient|sslserver`, the names against the list you are about to write. A server certificate needs the host or IP that callers use; a client certificate needs a **DNS or URI** name and the `clientAuth` usage; a certificate bought from a public authority is **not** a client certificate.
> 5. **Write the configuration with the keys of section 8, then run `coddy -t --dry-run --config <file>`** and read every finding. Fix the cause. **Never "fix" a certificate error with `insecure_skip_verify`**, never by trusting an authority system-wide when a `ca_file` would do, and never by loosening `client_auth` without saying so.
> 6. **Do not touch the operating system's trust store, the browser's store, a service unit, or anything that needs `sudo`, without asking.** Say what you will change and why. Prefer files under the Coddy home or `/etc/coddy`.
> 7. **A server's pair and its client CA are read at start**: tell the person that a restart is needed, and do not restart a service that other people use without asking. A dialling side's pair and a name in `cert_names` need none.
> 8. **Report the outcome with evidence**: the command run, the line it printed, and what remains the person's to do (distribute a certificate file, import a `.p12`, renew before the date). Distribute **certificates and authorities, never keys**.

For a Coddy session, the bundled `configure-coddy` skill carries the same rules and the same table of keys; an agent that is configuring Coddy through it follows them without being told.

A short decision procedure an agent can follow:

1. The connection is only on loopback or through a tunnel or proxy that terminates TLS: **stop**, set nothing (7.10).
2. The server needs a certificate: the person has a public name and can reach port 80 or use DNS-01 → ACME (7.8); has a purchased or company certificate → use it as the server pair (7.4, 7.6); has neither → self-signed (7.2) for one or two callers, a private authority (7.3) for more.
3. The server must tell clients apart by certificate: a private authority or self-signed client certificates (7.1, 7.3), names in `cert_names` that are DNS or URI entries, `client_auth: optional` if a browser opens it, `required` if only machines do.
4. The key has a passphrase or is a `.pfx`: convert on the owner's machine (section 4, section 5).
5. Check with section 9, state the restart a server's pair needs.

## 11. The tests that hold this page

What this page says about the TLS stack and about Coddy's two certificate functions is held by tests, so a change that makes it false breaks a build:

- **In process**, [`internal/netx/certscenarios_test.go`](../../internal/netx/certscenarios_test.go) runs each scenario as a real handshake over loopback with the functions Coddy uses: a self-signed client certificate that is its own anchor, and one nobody imported; a self-signed server certificate trusted through `ca_file`; a private authority issuing both ends; an intermediate; a certificate with only `serverAuth` refused as a client certificate; a wildcard that covers one label and whose name as a client is the literal string; a personal certificate with only an e-mail address that verifies and has no name; expired and not-yet-valid certificates; a key with a passphrase refused; a client pair rotated on disk and used by the next handshake with no restart.
- **End to end**, [`examples/tls/tls_e2e_selfsigned.py`](../../examples/tls/tls_e2e_selfsigned.py) (`make test-tls-e2e`, `examples/test_tls.sh`) makes the material with the commands of section 4, boots real `coddy serve` processes (an agent terminating TLS with `optional` and with `required`, one with a self-signed server certificate, and a relay with TLS in front of the node that requires certificates), and checks that a certificate named in `cert_names` lists the shared models with no token directly and through the relay, that it opens nothing else, that an outsider's certificate and one without `clientAuth` get no connection, that the borrower's own client (`coddy -t --dry-run`) reaches both with `ca_file` and `client_cert_file`, and that a key with a passphrase is refused with the error this page names. It needs `openssl` and skips without it.

A TLS interaction this page does not hold: ACME, a Microsoft authority and a browser's certificate store need the real thing, and are described from the documentation of those systems and of the code above, not exercised here.

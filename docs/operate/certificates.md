# Certificates and TLS: a field guide

Coddy listens over HTTP. A separate proxy or transport service owns incoming HTTPS, mutual TLS, certificates and renewal. Outgoing HTTPS still verifies the server certificate and host name through the system trust store. Bearer tokens, authorization and shared-model limits remain in Coddy.

This change removes the built-in certificate authority, `coddy tls`, TLS listeners and per-connection certificate settings. Old configuration is refused, including empty blocks and keys set to `false`; it is never silently downgraded to HTTP.

## Deployment boundary

Keep the HTTP backend on loopback or an isolated network reachable only by the transport service. Opening a public HTTPS endpoint does not justify opening its HTTP backend to the public network.

- **Web UI, HTTP API and shared models:** terminate HTTPS in a reverse proxy and forward HTTP to Coddy. Disable response buffering for SSE and give streams a suitable read timeout;
- **Swarm direct mounts, registration and upstream relays:** expose HTTPS through the external service. Configure the advertised URLs for the endpoint the caller can actually reach;
- **Swarm reverse tunnel:** its HTTP/1.1 `POST /swarm/tunnel` handshake is followed by a bidirectional HTTP/2 stream with reversed client/server roles. Use a TLS-terminating TCP forwarder that negotiates `http/1.1` through ALPN and preserves the bytes after the handshake. An ordinary HTTP reverse proxy is not a substitute unless it explicitly supports that protocol transition;
- **Outgoing private CA trust:** install the authority in the trust store used by the Coddy process. On systems that support them, `SSL_CERT_FILE` and `SSL_CERT_DIR` select the certificate roots;
- **Outgoing client certificates:** a separate transport component presents them. Coddy has no client-certificate settings.

A certificate authenticates a connection at the external service. Coddy still requires the appropriate main, shared-model, relay-client or pairing token. Do not treat a proxy-added certificate header as a Coddy credential.

## Migrating an existing installation

Perform the migration as one deployment change. Keep a compatible old binary and old configuration for a deliberate rollback.

1. Deploy external TLS or mTLS and verify the server names, trust and renewal;
2. Restrict direct access to every HTTP backend;
3. Prepare a configuration without the keys below. Keep HTTPS URLs where they address the external endpoint; never replace them with HTTP just to make validation pass;
4. Run the new binary's `coddy -t --dry-run --config <file>` and inspect every finding;
5. Switch the binary and configuration together, then verify HTTPS, token authorization, SSE, shared-model calls and every Swarm path in use.

| Removed configuration | Replacement |
| --- | --- |
| `tls` | Certificate management in the external service |
| `httpserver.tls`, `swarm.tls` | External TLS termination and a protected HTTP listener |
| `swarm.node_tls` | System trust and an external transport where client certificates are required |
| `swarm.join[].dial` and `swarm.upstreams[].dial`: `ca_file`, `cert_file`, `key_file`, `auto`, `insecure_skip_verify` | System trust and external transport; `dial.proxy` remains supported |
| `providers[]`: `ca_file`, `client_cert_file`, `client_key_file`, `tls_auto` | System trust and external transport; URL, proxy and credentials remain supported |

Startup, config checks, dry-run, reload, Settings saves, config commits and rollback reject these keys before they can disappear in decoding. An incompatible config does not trigger automatic backup recovery. A rejected reload keeps the running configuration; a rejected rollback does not overwrite the file.

### Existing certificate files

Coddy no longer reads or changes `$CODDY_HOME/tls`. Installation, update and service startup do not generate or renew certificates. Existing files remain on disk.

Before removing them by hand, check whether another service or an older Coddy installation still uses them. Preserve any files needed by your rollback. Never copy private keys into a repository, ticket, log or chat.

## External HTTPS for the HTTP API

Bind Coddy's HTTP listener to loopback. Keep your existing authentication settings; they are deliberately omitted from this minimal listener example.

```yaml
httpserver:
  enable: true
  host: 127.0.0.1
  port: 12345
```

For example, Caddy can terminate publicly trusted HTTPS and forward unbuffered responses.

```caddyfile
coddy.example.com {
    reverse_proxy 127.0.0.1:12345 {
        flush_interval -1
    }
}
```

For mTLS, configure client-certificate verification and any certificate-subject policy in the external service. Coddy's token check stays enabled behind it. The service also owns ACME, revocation, IP allowlists, WAF rules and connection-level audit.

This HTTP proxy example does not cover the Swarm reverse tunnel. Its transport must preserve the `POST /swarm/tunnel` handshake and the subsequent bidirectional byte stream. Use a separate endpoint or a protocol-aware deployment and verify it before switching an existing installation.

## Verification and troubleshooting

Check from outside the TLS boundary, not only against the HTTP backend.

```bash
coddy -t --dry-run --config <file>
curl -H "Authorization: Bearer $SHARED_TOKEN" https://coddy.example.com/coddy/llm/models
```

- An unknown authority means the Coddy process does not trust the endpoint's CA. Install the correct authority in its trust environment; do not disable certificate verification;
- A host-name mismatch means the URL and certificate SAN disagree. Correct the endpoint or certificate;
- A successful handshake followed by `401` means application credentials are missing or invalid. A certificate does not replace the token;
- A refused old TLS key means the configuration still needs migration. Removing it before protecting the backend is not a safe fix;
- A tunnel that registers but cannot carry requests may be behind an HTTP proxy that does not preserve the byte stream after the `POST /swarm/tunnel` handshake. Test the transport transition, idle heartbeat, disconnect and reconnect;
- A stream that stalls behind a proxy needs buffering and timeouts checked. Test cancellation and vanished-client detection through the actual deployed endpoint.

For shared models, verify that an abandoned client releases its slot. A TCP liveness timeout sees the immediate peer, which is now the proxy, not the caller beyond it. The application probe can detect an absent participating client, but the external service must also stop forwarding a stream whose downstream client vanished. Do not infer end-to-end liveness from a successful TLS handshake.

Related guides: [serve](serve.md), [Swarm](swarm.md), [shared models](../features/shared-models.md), and [security](security.md).

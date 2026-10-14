# Certificates and TLS: a field guide

The agent's HTTP listener listens over plain HTTP; a separate proxy or transport service owns incoming HTTPS, client-certificate verification, certificates and renewal. The relay can instead serve HTTPS from an existing certificate and private key in `swarm.tls` ([Swarm](swarm.md), [Encryption and proxies](swarm.md#encryption-and-proxies)). It accepts TLS 1.2 and newer, and it needs a restart after certificate rotation. Outgoing HTTPS verifies the server certificate and host name through the system trust store. Coddy handles bearer tokens, authorization and shared-model limits.

## Deployment boundary

Keep the HTTP backend on loopback or an isolated network reachable only by the transport service. Opening a public HTTPS endpoint does not justify opening its HTTP backend to the public network.

- **Web UI, HTTP API and shared models:** terminate HTTPS in a reverse proxy and forward HTTP to Coddy. Disable response buffering for SSE and give streams a suitable read timeout;
- **Swarm direct mounts, registration and upstream relays:** expose HTTPS through the external service or configure the relay's `swarm.tls` certificate and key. Configure the advertised URLs for the endpoint the caller can actually reach;
- **Swarm reverse tunnel:** its HTTP/1.1 `POST /swarm/tunnel` handshake is followed by a bidirectional HTTP/2 stream with reversed client/server roles. Use a TLS-terminating TCP forwarder that negotiates `http/1.1` through ALPN and preserves the bytes after the handshake. An ordinary HTTP reverse proxy is not a substitute unless it explicitly supports that protocol transition;
- **Outgoing private CA trust:** install the authority in the trust store used by the Coddy process. On systems that support them, `SSL_CERT_FILE` and `SSL_CERT_DIR` select the certificate roots;
- **Outgoing client certificates:** a separate transport component presents them. Coddy has no client-certificate settings.

A certificate authenticates a connection at the external service. Coddy still requires the appropriate main, shared-model, relay-client or pairing token. Do not treat a proxy-added certificate header as a Coddy credential.

## Deployment checklist

Configure and verify the transport before making the public endpoint available.

1. Deploy external HTTPS and verify the server names, trust and certificate renewal;
2. Restrict direct access to every HTTP backend;
3. Set the advertised and client URLs to the external HTTPS endpoints;
4. Run `coddy -t --dry-run --config <file>` and inspect every finding;
5. Verify HTTPS, token authorization, SSE, shared-model calls and every Swarm path in use through the public endpoint.

### Certificate management

The external service stores certificate files and private keys, renews certificates and applies access policy. Configure its storage permissions and renewal monitoring separately from Coddy.

Alternatively, a relay can use `swarm.tls.cert_file` and `swarm.tls.key_file` to serve HTTPS from an existing PEM certificate chain and private key. Set both paths or leave both empty for HTTP. The relay accepts TLS 1.2 and newer. Restart the relay after rotating either file. This TLS listener applies only to the relay. It does not add HTTPS to an agent listener, issue certificates, provide PKI, or enable client mTLS.

Never copy private keys into a repository, ticket, log or chat.

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

If the deployment requires client certificates, configure their verification and any certificate-subject policy in the external service. Keep Coddy's token check enabled behind it. The service also owns ACME, revocation, IP allowlists, WAF rules and connection-level audit.

This HTTP proxy example does not cover the Swarm reverse tunnel. Its transport must preserve the `POST /swarm/tunnel` handshake and the subsequent bidirectional byte stream. Use a separate endpoint or a protocol-aware deployment and verify it before admitting tunnel connections.

## Verification and troubleshooting

Check from outside the TLS boundary, not only against the HTTP backend.

```bash
coddy -t --dry-run --config <file>
curl -H "Authorization: Bearer $SHARED_TOKEN" https://coddy.example.com/coddy/llm/models
```

- An unknown authority means the Coddy process does not trust the endpoint's CA. Install the correct authority in its trust environment; do not disable certificate verification;
- A host-name mismatch means the URL and certificate SAN disagree. Correct the endpoint or certificate;
- A successful handshake followed by `401` means application credentials are missing or invalid. A certificate does not replace the token;
- A tunnel that registers but cannot carry requests may be behind an HTTP proxy that does not preserve the byte stream after the `POST /swarm/tunnel` handshake. Test the transport transition, idle heartbeat, disconnect and reconnect;
- A stream that stalls behind a proxy needs buffering and timeouts checked. Test cancellation and vanished-client detection through the actual deployed endpoint.

For shared models, verify that an abandoned client releases its slot. A TCP liveness timeout sees the immediate peer, which is the proxy, not the caller beyond it. The application probe can detect an absent participating client, but the external service must also stop forwarding a stream whose downstream client vanished. Do not infer end-to-end liveness from a successful TLS handshake.

Related guides: [serve](serve.md), [Swarm](swarm.md), [shared models](../features/shared-models.md), and [security](security.md).

# Provider proxy enforcement

Status: reviewed (crossreview: approve with changes)

## Problem (from audit)

`providers[].proxy` resolves correctly (`config.ResolvedLLM.ProxyURL`,
`internal/llm/transport.go` shared transports keyed by setting) and every wired
call site already passes a proxied client: `NewProvider` → `providerHTTPClient`
covers completions/responses/model listing/usage for openai, anthropic,
neuraldeep, codex, devin; CLI and HTTP sign-in routes use
`HTTPClientForProviderProxy(prov.Proxy)`; codex token refresh rides the managed
auth source built with the provider's client.

Two classes of leaks remain:

1. **Silent nil fallbacks.** Exported provider-network helpers default a nil
   client to `http.DefaultClient` / `&http.Client{}`:
   - `codex_auth.go:140` (`newCodexAuthSource` -> refresh token endpoint
     `https://auth.openai.com/oauth/token`),
   - `codex_device_auth.go:56,132` (device start / poll+exchange),
   - `neuraldeep_auth.go:437,493,687,711` (device start, poll, revoke,
     `neuralDeepGetJSON`),
   - `neuraldeep_usage.go:181` (`FetchNeuralDeepUsage`),
   - `devin_auth.go:630,879` (`devinUnary`, `exchangeDevinCode`).
   Every in-tree caller passes a client today, so the leaks are latent - but a
   future provider (kimicode-style OAuth) would silently bypass
   `providers[].proxy`. The requirement is structural: nothing in the provider
   path may reach the network without going through the row's proxy.
   Compounding it: `newOpenAIProvider`, `newAnthropicProvider` and
   `responsesClient` attach the client only when non-nil
   (`internal/llm/openai.go:35`, `anthropic.go:34`, `codex.go:82`), so a nil
   there silently rides the SDK default.

2. **Unsaved proxy dropped at sign-in (the reported bug).**
   `resolveSignInProvider` (`external/httpserver/codex_auth_http.go:283`)
   returns the saved row when the type matches and synthesizes a probe row
   keeping `saved.Proxy` of the old row otherwise - in both branches a proxy
   typed in the Settings form but not yet saved never reaches the client. The
   device-start POST bodies carry no proxy at all (codex parses no body beyond
   the JSON gate; neuraldeep parses only `api_base`), and the UI posts `{}` /
   `{api_base}`. `POST /coddy/providers/models` already accepts `proxy` in its
   body ("the row as the form holds it") - sign-in must do the same.

## Design

### 1. Proxy setting is the mandatory input at the llm boundary

Exported helpers that perform provider network I/O take the proxy *setting*
string instead of `*http.Client` and build the client internally with
`HTTPClientForProviderProxy` (which surfaces proxy-URL parse errors as errors):

- Codex: `CodexDeviceSignIn`, `StartCodexDeviceLogin`,
  `CompleteCodexDeviceLogin`, `CompleteCodexDeviceLoginWith`
- NeuralDeep: `NeuralDeepDeviceSignIn`,
  `CompleteNeuralDeepDeviceLoginWith`, `StartNeuralDeepDeviceLogin`,
  `PollNeuralDeepDeviceToken`, `RevokeNeuralDeepKey`,
  `FetchNeuralDeepWhoami`, `FetchNeuralDeepStatus`,
  `ApplyNeuralDeepLoginToConfig` (+ internal `neuralDeepGetJSON`)
- `FetchNeuralDeepUsage`
- Devin: `VerifyDevinCredential`, `DevinSignIn` (+ internal `devinUnary`,
  `exchangeDevinCode`, `devinPostJSON`)

Where a client param must remain for tests (TLS servers, custom transports),
keep an unexported `xxxWithClient` variant; the exported surface demands the
setting. Internal unexported helpers keep `*http.Client` params but lose the
nil default: nil returns a descriptive error (`provider http client is
required; build it with llm.HTTPClientForProviderProxy`).
`newCodexAuthSource` / `newManagedCodexAuthSource` error on nil instead of
silently using `http.DefaultClient`. `NewProvider` requires the built client
to be non-nil so the SDK-default attach path cannot fire (the providers
constructors keep their non-nil guard; the nil path becomes impossible at the
input boundary rather than silently losing the proxy).

All in-repo callers are updated (`cmd/coddy/{codex,devin,providers}.go`,
`external/httpserver/*_auth_http.go`, `codex_usage.go`).

### 2. Sign-in honours the form's unsaved proxy

- Device-start request bodies gain an optional `proxy` field, typed `*string`
  so three states stay distinct: absent = use the resolved row's proxy;
  `""`/`"inherit"` = follow the system proxy (the form's cleared state);
  a URL or `"none"` = that setting, normalized/validated through the same
  `config` proxy helpers, 400 on invalid. The override applies to the resolved
  provider *after* `resolveSignInProvider`, covering both the saved-row and the
  probe-row branch.
- `codexDeviceStartRequest` and `neuralDeepDeviceStartRequest` grow the field;
  the codex POST starts decoding its JSON body.
- `DELETE .../neuraldeep-auth` dials the hub for revoke and has no body - it
  accepts `?proxy=` (and `?api_base=` where relevant) with the same
  three-state semantics. Status/poll GETs never dial the provider (polling
  rides the client captured at POST) - verified, no override needed there.
- UI: `CodexAuthField.tsx` and `NeuralDeepAuthField.tsx` send the form's
  current `proxy` value in the device-start body and the revoke query;
  `CodexAuthField` receives the row (or its proxy) as a prop - it currently
  gets only `providerName`.
- `openapi.go` + `docs/reference/http-api.md` updated. No visual change, so no
  new screenshots - stated in the PR.

### 3. Regression net

- Extend `features/provider_proxy.feature` +
  `internal/llm/bdd_provider_proxy_test.go`: the scenario outline counts
  *every* request class per provider type against stub proxies counting
  absolute-form requests, including today's four neuraldeep classes plus:
  openai/anthropic `GET .../models`; codex responses stream, usage, token
  refresh (in-package `tokenURL` field override), catalog `GET {base}/models`,
  `ApplyCodexLoginToConfig`, device sign-in; neuraldeep whoami/status; devin
  JWT mint + catalog + chat stream (one chat = three requests), model list,
  usage, PKCE exchange.
- Happy-path spec for the sign-in override: device-start with `proxy` in the
  body for an unsaved / type-switched provider routes through that proxy
  (httpserver godog, extending `features/neuraldeep_auth.feature` or a
  dedicated `provider_signin_proxy.feature`). The invalid-proxy -> 400 case is
  a unit test, not a feature scenario (workflow rule).
- Guard test `internal/llm/proxy_guard_test.go` (precedent:
  `TestEverySpawnSiteAdaptsTheCommand`) scans non-test `internal/llm` sources
  for *anchored* forbidden shapes: `http.DefaultClient` as a whole token,
  exactly `&http.Client{}` (empty literal), `http.Get(`, `http.Post(`,
  `http.Head(`, with the sanctioned constructors in `transport.go` /
  `proxy_http_client.go` whitelisted; extends to provider auth files in
  `external/httpserver` and `cmd/coddy` where they hand clients to llm.

### 4. Permanent agent rule + docs

- New `.cursor/rules/provider-proxy.mdc` (globs covering `internal/llm/**`,
  provider auth http files, `cmd/coddy/*`) mirrored to
  `.claude/rules/provider-proxy.md`; index line in `.codex/rules.md`; review
  bullet in `AGENTS.md` under Code Review Rules ("Provider transport").
- Update the existing statements of the `providers[].proxy` guarantee in
  `docs/getting-started/configuration.md` and `docs/surfaces/web-ui.md`; no new
  page.

## Non-goals

Non-provider HTTP users keep their own semantics: `internal/tools/web`
(per-request proxy arg), `internal/update`, `internal/remote`,
`internal/dryrun` probes, `internal/skills/remote.go`, the Telegram gateway's
`gateways.telegram.proxy`.

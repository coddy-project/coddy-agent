# Web Authentication Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move PR 709 to a Settings-managed password and optional TOTP flow with a separate second sign-in step, Base16/Base32 secrets, backup codes, and no `set-totp` command.

**Architecture:** Keep password editing in the Settings save transaction through a transient plaintext input converted to Argon2id before storage. Use authenticated, same-origin enrollment endpoints for TOTP and recovery material. Split login into password and challenge verification calls, with short-lived in-memory challenges and existing cookie/session defenses.

**Tech Stack:** Go, `internal/webauth`, `rsc.io/qr`, React, TypeScript, Vitest, Godog, SCSS, English and Russian documentation.

---

### Task 1: TOTP encodings and recovery credentials

**Files:**
- Modify: `internal/webauth/totp.go`
- Modify: `internal/webauth/totp_test.go`
- Create: `internal/webauth/backup.go`
- Create: `internal/webauth/backup_test.go`
- Modify: `internal/webauth/session.go`

- [ ] Write Base16 validation/code-vector tests and Base16-to-canonical-Base32 QR tests. Add backup-code tests for format, hashed storage, atomic consumption, and generation-based session invalidation.
- [ ] Run `go test ./internal/webauth -run 'Base16|Backup' -count=1` and confirm failures name the missing behavior.
- [ ] Implement decoding that accepts normalized Base32 and hexadecimal, keeping the 80-bit minimum. Convert accepted input to canonical Base32 for `TOTPKeyURI`. Generate Base32 secrets as before.
- [ ] Implement random backup-code generation, SHA-256 hashes with a per-set random generation id, constant-time lookup, and consumption under one lock. Include only the stable generation id in `AccountFingerprint`.
- [ ] Run `go test ./internal/webauth -count=1`; expect PASS.
- [ ] Commit with `git add internal/webauth && git commit -m "feat(webauth): accept hex TOTP keys and add backup codes"`.

### Task 2: Settings account and enrollment backend

**Files:**
- Modify: `internal/config/http.go`, `internal/config/jsondto.go`, `internal/config/config.schema.json`, `internal/config/ui_schema.go`
- Modify: `internal/config/http_login_test.go`
- Create: `external/httpserver/auth_enrollment.go`
- Create: `external/httpserver/auth_enrollment_test.go`
- Modify: `external/httpserver/auth_login.go`, `external/httpserver/server.go`

- [ ] Add failing config tests for transient `password`, empty-password preservation, Base16 secrets, redacted backup hashes, and a stable backup generation.
- [ ] Add failing HTTP tests for authenticated same-origin enrollment, QR generation, verify-before-enable, failed enrollment leaving live config unchanged, disable, and one-time backup-code display/rotation.
- [ ] Run `go test ./internal/config ./external/httpserver -run 'Login|TOTP|Backup|Enrollment' -count=1`; confirm expected failures.
- [ ] Extend login config with write-only backup state and generation. Hash transient `password` before validation/storage and never return it. Add a login-only UI schema without exposing listener or token controls.
- [ ] Add enrollment routes that use the server's live config backend and existing config file lock/transaction. Return QR PNG or a data image and canonical secret during setup; persist only after code verification. Generate backup codes with the same commit and return plaintext only in that response.
- [ ] Run the narrow Go tests again; expect PASS.
- [ ] Commit with `git add internal/config external/httpserver && git commit -m "feat(httpserver): manage web authentication from Settings"`.

### Task 3: Two-step sign-in

**Files:**
- Modify: `external/httpserver/auth_login.go`
- Create: `external/httpserver/auth_challenge.go`
- Modify: `external/httpserver/auth_login_totp_test.go`
- Modify: `features/webui_login_totp.feature`
- Modify: `external/httpserver/bdd_webui_login_totp_test.go`

- [ ] Rewrite the happy-path spec so password success returns a pending challenge and only the second call opens the API. Add a backup-code happy path.
- [ ] Add failing unit tests for challenge expiry, binding, one-time use, wrong codes, throttling, and unchanged bearer/Telegram/browser behavior.
- [ ] Run `go test ./external/httpserver -run 'TOTP|Challenge|WebuiLogin' -count=1`; confirm failures show the old one-call flow.
- [ ] Implement an in-memory challenge store keyed by crypto-random tokens. Bind each challenge to account fingerprint and expiry. Apply existing throttling to both steps and consume challenges according to the design.
- [ ] Keep the password-only login response compatible. On TOTP accounts return no cookie until `/coddy/auth/login/verify` accepts a TOTP or unused backup code.
- [ ] Run narrow tests and `go test ./internal/webauth ./external/httpserver -count=1`; expect PASS.
- [ ] Commit with `git add external/httpserver features/webui_login_totp.feature && git commit -m "feat(httpserver): split TOTP sign-in into two steps"`.

### Task 4: Authentication Settings UI

**Files:**
- Create: `external/ui/src/ui/settings/AuthenticationSection.tsx`
- Create: `external/ui/src/ui/settings/AuthenticationSection.test.tsx`
- Create: `external/ui/src/ui/settings/SecretInput.tsx`
- Create: `external/ui/src/ui/settings/SecretInput.test.tsx`
- Modify: `external/ui/src/ui/settings/SettingsSection.tsx`, `settingsSections.ts`, `settingsSearch.ts`
- Modify: `external/ui/src/ui/settings/icons.tsx`
- Modify: `external/ui/src/styles/settings/_fields.scss`
- Modify: `external/ui/src/ui/i18n/messages/en.ts`, `ru.ts`

- [ ] Write failing component tests for the top enable switch, editable login/password, eye toggle, TOTP switch, QR only during Settings enrollment, Base16/Base32 input, period bar, backup codes in the same fieldset, and mobile-safe markup.
- [ ] Run `cd external/ui && npm test -- AuthenticationSection SecretInput`; confirm expected missing-component failures.
- [ ] Implement the approved desktop/mobile mockup with existing SwitchField, FieldHint, settings fieldsets, search landmarks, and both dictionaries. Add the API client for enrollment operations.
- [ ] Derive the period from `Date.now()` modulo 30 seconds. Respect reduced motion and simplified mode.
- [ ] Run the component tests, Settings drawing/index tests, i18n tests, and typecheck; expect PASS.
- [ ] Commit the UI changes with `git add external/ui && git commit -m "feat(ui): add authentication settings and TOTP enrollment"`.

### Task 5: Two-step sign-in UI

**Files:**
- Modify: `external/ui/src/ui/auth/authState.ts`, `SignInScreen.tsx`
- Modify: `external/ui/src/ui/auth/authState.test.ts`, `AuthGate.test.tsx`
- Modify: `external/ui/src/styles/screens/_auth.scss`

- [ ] Write failing tests proving the code field is absent before password acceptance, appears after a challenge, submits to the verify route, offers backup-code entry, and never renders a QR on sign-in.
- [ ] Run `cd external/ui && npm test -- AuthGate authState`; confirm failures against the old one-form behavior.
- [ ] Implement challenge state and the two submit steps. Clear refused codes, preserve the password only for the first step, and retain existing cookie-not-kept and error handling.
- [ ] Add the shrinking period indicator and accessible backup-code alternative using the approved mockup.
- [ ] Run auth tests, typecheck, and Prettier; expect PASS.
- [ ] Commit with `git add external/ui && git commit -m "feat(ui): make authenticator verification a separate sign-in step"`.

### Task 6: Remove the CLI enrollment surface

**Files:**
- Delete: `cmd/coddy/serve_totp.go`, `cmd/coddy/serve_totp_test.go`
- Modify: `cmd/coddy/main.go`, `serve.go`, `usage_test.go`
- Modify: `packaging/man/coddy.1`, `packaging/completions/coddy.bash`, `coddy.zsh`
- Modify: `go.mod`, `go.sum` only if QR generation has moved entirely behind the HTTP tag and the dependency remains needed there

- [ ] Add the usage expectation without `set-totp` and run `go test ./cmd/coddy -run 'TestUsage|TestPackaging' -count=1`; confirm the stale command fails the test.
- [ ] Remove dispatch, help, implementation, tests, man page, and completions entries. Sweep with `git grep -nI 'set-totp'` and leave no live references outside historical design records.
- [ ] Run `go test ./cmd/coddy -count=1`; expect PASS.
- [ ] Commit with `git add cmd/coddy packaging go.mod go.sum && git commit -m "refactor(cli): remove set-totp in favor of Settings"`.

### Task 7: API and documentation synchronization

**Files:**
- Modify: `external/httpserver/openapi.go`
- Modify: `docs/reference/http-api.md`, `docs/getting-started/configuration.md`, `docs/operate/remote.md`, `docs/operate/security.md`, `docs/surfaces/web-ui.md`
- Modify the matching `docs/ru/` pages and `docs/ru/config-descriptions.yaml`
- Modify: `DESIGN.md`, `config.example.yaml`, `README.md`, `internal/skills/bundled/configure-coddy/SKILL.md`
- Regenerate: `docs/reference/config.md`, `docs/reference/cli.md`, `docs/README.md`, `docs/assets/INDEX.md`

- [ ] Update OpenAPI request/response schemas and status codes before calling the API complete.
- [ ] Replace CLI enrollment documentation with Settings enrollment and backup-code recovery. Describe the two sign-in steps and Base16/Base32 compatibility in both languages.
- [ ] Capture the real changed surfaces at 1280px and 390px and embed the documentation assets required by the workflow.
- [ ] Run `make docs`, translation review/restamping, `make docs-check`, `make site-schema-check`, and `make site-docs-check`; resolve drift or report external-checkout limitations honestly.
- [ ] Commit with `git add DESIGN.md README.md config.example.yaml docs external/httpserver/openapi.go internal/skills && git commit -m "docs: describe Settings-managed two-step authentication"`.

### Task 8: Full verification and independent review

- [ ] Run `make test` with the locally prepared Node.js on PATH, then `make lint` and `make models-catalog-check`.
- [ ] Run focused browser checks for sign-in and Settings at 1280px and 390px; inspect all screenshots for private data.
- [ ] Create a crossreview brief from the final branch diff, excluding generated assets and lockfiles. Run every roster reviewer and verify each finding against the code.
- [ ] Fix confirmed findings through new failing tests, rerun affected suites and full checks, then perform a fresh review round when fixes are substantive.
- [ ] Report exact commands, outcomes, screenshots, known limitations, and crossreview verdict without claiming unrun checks.

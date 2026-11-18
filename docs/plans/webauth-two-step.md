# Web authentication redesign

## Scope

Rework PR 709 into a Settings-managed web authentication flow. The change covers password account editing, optional TOTP enrollment, one-time backup codes, a two-step browser sign-in, and complete removal of `coddy serve set-totp`. Bearer-token clients, Telegram Mini App sign-in, and browser-panel tickets keep their existing authentication paths.

## Considered approaches

1. **Selected: dedicated Authentication section plus purpose-built enrollment endpoints.** Password account fields use the settings document and its Save all transaction. TOTP enrollment and backup-code rotation use authenticated API operations because they generate secrets, require verification before activation, and return one-time recovery material. This keeps the ordinary config form simple while giving enrollment an explicit transaction.
2. **Entire flow through the generic config form.** Rejected: the form cannot safely generate a QR enrollment, confirm a code before activation, or show newly generated backup codes only once.
3. **Keep CLI enrollment and add only sign-in changes.** Rejected: it conflicts with the requested Settings-first flow and leaves two competing management surfaces.

## Settings

Add a dedicated **Authentication** section to the Settings drawer. It must not expose unrelated HTTP listener, bind, or bearer-token settings.

The section contains:

- an **Enable authentication** switch at the top;
- editable login and password fields, with a visibility toggle on the password;
- write-only password semantics: an empty password keeps the stored hash, and plaintext is never returned by the config API. The settings document carries a transient `password` value only on write; the server validates it, derives the Argon2id hash before validation and storage, and never persists the plaintext field;
- an **Authenticator app** fieldset with its own TOTP switch, editable Base16/Base32 secret input, QR code, six-digit verification field, and a shrinking 30-second period indicator;
- backup codes inside the same fieldset, separated by a subtle divider, with count, copy, download, and deliberate regeneration controls.

The desktop and 390px mobile layouts follow the approved interactive mockup. New labels and descriptions join Settings search, both dictionaries, and the index-versus-drawing tests.

TOTP activation is transactional: generate or enter a secret, scan the QR code, verify a current code, then persist the secret and initial backup-code hashes together. A failed or abandoned enrollment changes no live authentication state. Disabling or rotating TOTP requires an authenticated same-origin request and invalidates sessions through the existing account fingerprint mechanism.

## Sign-in flow

Password-only accounts sign in with one request as before.

For TOTP-enabled accounts:

1. `POST /coddy/auth/login` verifies the login and password without creating a session. On success it returns a short-lived, single-use pending challenge.
2. The SPA replaces the password form with **Two-step verification**. It shows only the code field, period progress bar, backup-code alternative, and a back action. No QR or TOTP secret is exposed at sign-in.
3. `POST /coddy/auth/login/verify` accepts the challenge and either a TOTP or a backup code. Success creates the browser session. Failure clears the submitted code and preserves throttling and anti-enumeration behavior.

Pending challenges are held in memory, expire quickly, are bound to the account fingerprint, and are consumed on every completed verification attempt so a challenge cannot be brute-forced repeatedly. Existing login throttling, constant-time credential checks, CSRF defenses, cookie attributes, and replay protection remain in force.

The progress indicator is derived from the current 30-second TOTP boundary rather than a free-running animation alone. It respects reduced-motion and simplified-mode contracts.

## Secret encodings and backup codes

TOTP accepts normalized Base32 and Base16 secrets with the existing minimum 80-bit key size. Generated secrets remain Base32 because that is the interoperable `otpauth://` representation. The QR encodes the canonical Base32 key URI regardless of which encoding the operator entered.

Backup codes are randomly generated, shown once after creation or regeneration, and stored only as cryptographic hashes. Verification consumes a code atomically so it cannot be reused. A stable random backup-set generation identifier, not the changing list of remaining hashes, contributes to account session invalidation when the set is rotated. Consuming a code therefore leaves existing sessions valid. No API response returns stored backup-code hashes.

## Removal and compatibility

Remove `set-totp` from command dispatch, help, usage tests, man page, completions, examples, bundled instructions, and both documentation languages. Existing `httpserver.login.totp_secret` values remain readable; valid Base32 configurations upgrade without re-enrollment.

## Errors and security

- Invalid credentials do not disclose whether the login exists.
- A pending challenge is returned only after a valid password; the second step necessarily confirms that the first step passed.
- Setup, disable, and backup-code operations require an authenticated session and same-origin request checks.
- Secrets and recovery material never enter logs, general config reads, or sign-in responses.
- Environment-provided accounts keep precedence; the Settings section reports that the password account is managed externally instead of pretending to edit it.

## Verification

Write tests first and observe the expected failures:

- extend `features/webui_login_totp.feature` for password step, challenge, TOTP step, and backup-code sign-in;
- add Base16 vectors, encoding normalization, backup-code single-use and hashing tests;
- cover challenge expiry, consumption, wrong account binding, and existing replay/throttle/session-invalidation invariants;
- cover Settings account editing, secret visibility, enrollment states, QR visibility rules, backup-code actions, mobile layout, and both locales;
- run the Go suite with shipped tags, SPA tests and typecheck, `make test`, `make lint`, documentation checks, and browser captures at 1280px and 390px;
- run an independent cross-review of the final diff and verify every finding before deciding whether to fix it.

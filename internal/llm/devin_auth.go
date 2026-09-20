package llm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Devin (Cognition) sign-in and credentials. A Devin account is reached
// through the same API server the Devin CLI and Devin Desktop talk to: a
// long-lived session token (devin-session-token$...) is exchanged for a
// short-lived user JWT, and both travel in the Metadata of every Connect-RPC
// call. The session token comes from one of three places, first match wins:
//
//   - an explicit api_key (or api_key_command, or the NAME_API_KEY variable);
//   - the Coddy-managed login, $CODDY_HOME/providers/<name>/devin-auth.json,
//     written by `coddy providers login <name>` after a browser sign-in;
//   - the Devin CLI login, credentials.toml written by `devin auth login`.
const (
	devinDefaultAPIServer = "https://server.codeium.com"
	devinDefaultWebapp    = "https://app.devin.ai"
	devinDefaultAPIURL    = "https://api.devin.ai"

	// EnvDevinAPIServerURL overrides the API server (GetUserJwt, the model
	// catalog, chat) for the whole process: stands and tests. A config file
	// cannot move it, so a session token never leaves for a host the
	// operator did not choose outside config.yaml.
	EnvDevinAPIServerURL = "CODDY_DEVIN_API_SERVER_URL"
	// EnvDevinWebappURL overrides the sign-in page host (app.devin.ai).
	EnvDevinWebappURL = "CODDY_DEVIN_WEBAPP_URL"
	// EnvDevinAPIURL overrides the Devin API that exchanges the sign-in code
	// for a session token (api.devin.ai).
	EnvDevinAPIURL = "CODDY_DEVIN_API_URL"
	// EnvDevinCLICredentials points at the Devin CLI credentials.toml instead
	// of its default location.
	EnvDevinCLICredentials = "CODDY_DEVIN_CLI_CREDENTIALS"

	devinSessionTokenPrefix = "devin-session-token$"
	// devinJWTSkew mints a new user JWT this long before the current one
	// expires, so no request starts with a token about to lapse.
	devinJWTSkew = 60 * time.Second
	// devinRPCTimeout bounds one unary call (JWT, catalog, code exchange).
	devinRPCTimeout = 30 * time.Second
)

// Credential sources reported by DevinAuthStatus.Source.
const (
	DevinSourceAPIKey   = "api_key"
	DevinSourceCoddy    = "coddy"
	DevinSourceDevinCLI = "devin_cli"
)

// devinLoginTimeout bounds a browser sign-in. A variable so the CLI
// harnesses can shrink a wait that would otherwise outlive `go test`.
var devinLoginTimeout = 10 * time.Minute

// SetDevinLoginTimeout replaces that deadline and returns the call that
// restores it. Test-only seam, like SetNeuralDeepLoginTimeout.
func SetDevinLoginTimeout(d time.Duration) func() {
	prev := devinLoginTimeout
	devinLoginTimeout = d
	return func() { devinLoginTimeout = prev }
}

func envOr(name, fallback string) string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv(name)), "/"); v != "" {
		return v
	}
	return fallback
}

// devinAPIServer returns the API server a credential talks to: the process
// override, then the server the login recorded, then the default.
func devinAPIServer(recorded string) string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv(EnvDevinAPIServerURL)), "/"); v != "" {
		return v
	}
	if v := strings.TrimRight(strings.TrimSpace(recorded), "/"); v != "" {
		return v
	}
	return devinDefaultAPIServer
}

// DevinAPIServerURL is the API server a devin provider reaches when its
// credential records none: the process override, else the default.
func DevinAPIServerURL() string { return devinAPIServer("") }

// normalizeDevinToken adds the session-token prefix a bare token lacks.
// Keys of the other shapes the API server accepts are left alone.
func normalizeDevinToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	for _, p := range []string{devinSessionTokenPrefix, "wspkce$", "sk-ws-", "cog_"} {
		if strings.HasPrefix(token, p) {
			return token
		}
	}
	return devinSessionTokenPrefix + token
}

// maskDevinToken renders the token's kind and last four characters.
func maskDevinToken(token string) string {
	token = strings.TrimSpace(token)
	kind := "token"
	if i := strings.IndexByte(token, '$'); i > 0 {
		kind = token[:i]
		token = token[i+1:]
	}
	if len(token) < 16 {
		return kind + "$…"
	}
	return kind + "$…" + token[len(token)-4:]
}

// devinCredential is a resolved session token and where it came from.
type devinCredential struct {
	token     string
	apiServer string
	source    string
	path      string
}

// errDevinNotSignedIn is returned when no source holds a session token.
var errDevinNotSignedIn = errors.New("devin: not signed in (run `coddy providers login <name>`, or `devin auth login`)")

// resolveDevinCredential applies the source order documented at the top of
// this file. explicit is the provider's api_key after its own resolution.
func resolveDevinCredential(explicit, authPath string) (devinCredential, error) {
	if t := normalizeDevinToken(explicit); t != "" {
		return devinCredential{token: t, source: DevinSourceAPIKey}, nil
	}
	if f, err := loadDevinAuth(authPath); err != nil {
		return devinCredential{}, err
	} else if f != nil && strings.TrimSpace(f.SessionToken) != "" {
		return devinCredential{token: normalizeDevinToken(f.SessionToken), apiServer: f.APIServerURL, source: DevinSourceCoddy, path: authPath}, nil
	}
	cli, path, err := loadDevinCLICredentials()
	if err != nil {
		return devinCredential{}, err
	}
	if cli != nil && strings.TrimSpace(cli.APIKey) != "" {
		return devinCredential{token: normalizeDevinToken(cli.APIKey), apiServer: cli.APIServerURL, source: DevinSourceDevinCLI, path: path}, nil
	}
	return devinCredential{}, errDevinNotSignedIn
}

// devinAuthFile is the Coddy-managed credential. The session token is the
// only secret; the rest serves status displays.
type devinAuthFile struct {
	SessionToken string `json:"session_token"`
	APIServerURL string `json:"api_server_url,omitempty"`
	Email        string `json:"email,omitempty"`
	ObtainedAt   string `json:"obtained_at"`
}

// devinAuthMu serializes managed credential file access process-wide.
var devinAuthMu sync.Mutex

func loadDevinAuth(path string) (*devinAuthFile, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	devinAuthMu.Lock()
	defer devinAuthMu.Unlock()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("devin auth: read %s: %w", path, err)
	}
	var f devinAuthFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("devin auth: parse %s: %w", path, err)
	}
	return &f, nil
}

// saveDevinAuth writes the managed credential atomically with 0600.
func saveDevinAuth(path string, f devinAuthFile) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("devin auth: credential path is empty")
	}
	if strings.TrimSpace(f.SessionToken) == "" {
		return errors.New("devin auth: refusing to store an empty session token")
	}
	f.ObtainedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	devinAuthMu.Lock()
	defer devinAuthMu.Unlock()
	return writePrivateFileAtomic(path, data)
}

// RemoveDevinAuth deletes the Coddy-managed credential; the Devin CLI login
// is never touched. A missing file is fine.
func RemoveDevinAuth(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("devin auth: credential path is empty")
	}
	devinAuthMu.Lock()
	defer devinAuthMu.Unlock()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// devinCLICredentials is the part of the Devin CLI credentials.toml Coddy reads.
type devinCLICredentials struct {
	APIKey       string
	APIServerURL string
}

// DevinCLICredentialsPath is where `devin auth login` keeps its credential on
// this machine: EnvDevinCLICredentials when set, else the first candidate that
// exists, else the usual location (reported even when absent).
func DevinCLICredentialsPath() string {
	if p := strings.TrimSpace(os.Getenv(EnvDevinCLICredentials)); p != "" {
		return p
	}
	candidates := devinCLICredentialCandidates()
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

func devinCLICredentialCandidates() []string {
	var out []string
	if x := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); x != "" {
		out = append(out, filepath.Join(x, "devin", "credentials.toml"))
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		out = append(out, filepath.Join(home, ".local", "share", "devin", "credentials.toml"))
		if runtime.GOOS == "darwin" {
			out = append(out, filepath.Join(home, "Library", "Application Support", "devin", "credentials.toml"))
		}
	}
	if runtime.GOOS == "windows" {
		for _, env := range []string{"LOCALAPPDATA", "APPDATA"} {
			if d := strings.TrimSpace(os.Getenv(env)); d != "" {
				out = append(out, filepath.Join(d, "devin", "credentials.toml"))
			}
		}
	}
	return out
}

// loadDevinCLICredentials reads the Devin CLI login. A missing file is not an
// error: nil comes back with the path that was looked at.
func loadDevinCLICredentials() (*devinCLICredentials, string, error) {
	path := DevinCLICredentialsPath()
	if path == "" {
		return nil, "", nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, path, nil
	}
	if err != nil {
		return nil, path, fmt.Errorf("devin auth: read %s: %w", path, err)
	}
	values, err := parseFlatTOMLStrings(data)
	if err != nil {
		return nil, path, fmt.Errorf("devin auth: parse %s: %w", path, err)
	}
	return &devinCLICredentials{APIKey: values["windsurf_api_key"], APIServerURL: values["api_server_url"]}, path, nil
}

// parseFlatTOMLStrings reads the top-level string keys of a TOML document,
// which is all the Devin CLI credential file holds. Keys under a [table] and
// values of other types are skipped; a malformed string is an error.
func parseFlatTOMLStrings(data []byte) (map[string]string, error) {
	out := map[string]string{}
	inTable := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inTable = true
			continue
		}
		key, rest, ok := strings.Cut(line, "=")
		if !ok || inTable {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		rest = strings.TrimSpace(rest)
		var val string
		switch {
		case strings.HasPrefix(rest, `"`):
			end := closingQuote(rest)
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated string", n)
			}
			v, err := strconv.Unquote(rest[:end+1])
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			val = v
		case strings.HasPrefix(rest, "'"):
			end := strings.IndexByte(rest[1:], '\'')
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated string", n)
			}
			val = rest[1 : end+1]
		default:
			continue
		}
		out[key] = val
	}
	return out, sc.Err()
}

// closingQuote returns the index of the quote that ends the basic string
// starting at s[0], or -1.
func closingQuote(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// DevinAuthStatus is the display-safe state of a devin provider's login.
type DevinAuthStatus struct {
	Connected bool   `json:"connected"`
	Source    string `json:"source,omitempty"`
	Path      string `json:"path,omitempty"`
	Masked    string `json:"masked,omitempty"`
	Email     string `json:"email,omitempty"`
}

// InspectDevinAuth reports the login a devin provider would use without an
// explicit api_key: the Coddy-managed file first, then the Devin CLI login.
// No network call is made.
func InspectDevinAuth(authPath string) (DevinAuthStatus, error) {
	f, err := loadDevinAuth(authPath)
	if err != nil {
		return DevinAuthStatus{}, err
	}
	if f != nil && strings.TrimSpace(f.SessionToken) != "" {
		return DevinAuthStatus{Connected: true, Source: DevinSourceCoddy, Path: authPath, Masked: maskDevinToken(normalizeDevinToken(f.SessionToken)), Email: f.Email}, nil
	}
	cli, path, err := loadDevinCLICredentials()
	if err != nil {
		return DevinAuthStatus{}, err
	}
	if cli != nil && strings.TrimSpace(cli.APIKey) != "" {
		return DevinAuthStatus{Connected: true, Source: DevinSourceDevinCLI, Path: path, Masked: maskDevinToken(normalizeDevinToken(cli.APIKey))}, nil
	}
	return DevinAuthStatus{}, nil
}

// DevinAccount is who a session token belongs to, read off the user JWT.
type DevinAccount struct {
	Email string
	Name  string
}

// devinUserJWT is a minted user JWT and the server chat goes to with it.
type devinUserJWT struct {
	jwt       string
	chatURL   string
	expiresAt time.Time
	account   DevinAccount
}

// devinJWTCache keeps one user JWT per session token and API server, so a
// burst of requests mints once. Each entry has its own lock: minting for one
// account never waits on another.
var devinJWTCache = struct {
	mu sync.Mutex
	m  map[string]*devinJWTEntry
}{m: map[string]*devinJWTEntry{}}

type devinJWTEntry struct {
	mu  sync.Mutex
	jwt devinUserJWT
}

func devinJWTKey(cred devinCredential) string {
	return devinAPIServer(cred.apiServer) + "\x00" + cred.token
}

// devinJWTFor returns a user JWT for the credential, minting one when none is
// cached or the cached one is about to expire.
func devinJWTFor(ctx context.Context, hc *http.Client, cred devinCredential) (devinUserJWT, error) {
	key := devinJWTKey(cred)
	devinJWTCache.mu.Lock()
	entry := devinJWTCache.m[key]
	if entry == nil {
		entry = &devinJWTEntry{}
		devinJWTCache.m[key] = entry
	}
	devinJWTCache.mu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.jwt.jwt != "" && time.Now().Add(devinJWTSkew).Before(entry.jwt.expiresAt) {
		return entry.jwt, nil
	}
	minted, err := mintDevinJWT(ctx, hc, cred)
	if err != nil {
		return devinUserJWT{}, err
	}
	entry.jwt = minted
	return minted, nil
}

// forgetDevinJWT drops the cached JWT of a credential the server rejected, so
// the next request mints a fresh one.
func forgetDevinJWT(cred devinCredential) {
	devinJWTCache.mu.Lock()
	delete(devinJWTCache.m, devinJWTKey(cred))
	devinJWTCache.mu.Unlock()
}

// mintDevinJWT exchanges the session token for a user JWT (GetUserJwt).
func mintDevinJWT(ctx context.Context, hc *http.Client, cred devinCredential) (devinUserJWT, error) {
	server := devinAPIServer(cred.apiServer)
	meta := devinMetadata{ide: devinChatIDE, apiKey: cred.token, sessionID: newCodexSessionID(), requestID: uint64(time.Now().UnixMilli()), triggerID: newCodexSessionID()}
	var w pbWriter
	w.msg(1, meta.encode())
	body, err := devinUnary(ctx, hc, server+"/exa.auth_pb.AuthService/GetUserJwt", w.buf)
	if err != nil {
		var apiErr *devinAPIError
		if errors.As(err, &apiErr) && apiErr.status == http.StatusUnauthorized {
			return devinUserJWT{}, fmt.Errorf("devin auth: the session token %s was rejected (%w); %s", devinSourcePhrase(cred), err, devinSignInHint(cred))
		}
		return devinUserJWT{}, fmt.Errorf("devin auth: %w", err)
	}
	jwt, chatURL, err := decodeDevinUserJWT(body)
	if err != nil {
		return devinUserJWT{}, fmt.Errorf("devin auth: decode GetUserJwt: %w", &devinInvalidResponseError{err})
	}
	if strings.TrimSpace(jwt) == "" {
		return devinUserJWT{}, &devinInvalidResponseError{errors.New("devin auth: the API server returned no user JWT")}
	}
	out := devinUserJWT{jwt: jwt, chatURL: server, expiresAt: time.Now().Add(10 * time.Minute)}
	if u := strings.TrimRight(strings.TrimSpace(chatURL), "/"); u != "" && os.Getenv(EnvDevinAPIServerURL) == "" {
		// A dedicated deployment: chat goes where the account lives. The
		// session token travels with every chat request, so only an HTTPS
		// address is taken; anything else stays on the server that answered.
		if parsed, perr := url.Parse(u); perr == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil {
			out.chatURL = u
		}
	}
	claims := jwtClaims(jwt)
	if exp, ok := claims["exp"].(float64); ok && exp > 0 {
		out.expiresAt = time.Unix(int64(exp), 0)
	}
	out.account.Email, _ = claims["email"].(string)
	out.account.Name, _ = claims["name"].(string)
	return out, nil
}

// devinSourcePhrase names where a credential came from, for messages.
func devinSourcePhrase(cred devinCredential) string {
	switch cred.source {
	case DevinSourceAPIKey:
		return "from the provider's api_key"
	case DevinSourceDevinCLI:
		return "of the Devin CLI login " + cred.path
	case DevinSourceCoddy:
		return "stored at " + cred.path
	}
	return ""
}

// devinSignInHint says how to replace a rejected credential.
func devinSignInHint(cred devinCredential) string {
	switch cred.source {
	case DevinSourceAPIKey:
		return "check api_key (a Devin session token, devin-session-token$...)"
	case DevinSourceDevinCLI:
		return "run `devin auth login`, or `coddy providers login <name>`"
	}
	return "run `coddy providers login <name>` to sign in again"
}

// jwtClaims decodes the payload of a JWT without verifying it; the token is
// only read for display and expiry, the server is what checks it.
func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims
}

// devinInvalidResponseError marks an upstream answer that was syntactically
// corrupt: not a transport or auth failure, but a payload the schema cannot
// read. Usage reads map it to ProviderUsageInvalid.
type devinInvalidResponseError struct{ err error }

func (e *devinInvalidResponseError) Error() string { return e.err.Error() }
func (e *devinInvalidResponseError) Unwrap() error { return e.err }

// devinAPIError is an error answer of the Devin API: the HTTP status it came
// with (or the one its Connect code stands for) and the server's own message.
type devinAPIError struct {
	status  int
	code    string
	message string
	// retryAfter is the Retry-After hint of a throttled or unavailable
	// answer, zero when the server sent none.
	retryAfter time.Duration
}

func (e *devinAPIError) Error() string {
	msg := strings.TrimSpace(e.message)
	if msg == "" {
		msg = http.StatusText(e.status)
	}
	if e.code != "" {
		return fmt.Sprintf("HTTP %d %s: %s", e.status, e.code, msg)
	}
	return fmt.Sprintf("HTTP %d: %s", e.status, msg)
}

// devinErrorFromBody builds the error of a failed call from its Connect error
// body ({"code": ..., "message": ...}), or the FastAPI one ({"detail": ...}).
func devinErrorFromBody(status int, raw []byte) *devinAPIError {
	var env struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Detail  json.RawMessage `json:"detail"`
	}
	e := &devinAPIError{status: status}
	if json.Unmarshal(raw, &env) == nil {
		e.code, e.message = env.Code, env.Message
		if e.message == "" && len(env.Detail) > 0 {
			var text string
			if json.Unmarshal(env.Detail, &text) == nil {
				e.message = text
			} else {
				e.message = string(env.Detail)
			}
		}
	}
	if e.message == "" && e.code == "" {
		e.message = strings.TrimSpace(string(raw))
		if len(e.message) > 300 {
			e.message = e.message[:300] + "…"
		}
	}
	return e
}

// devinUnary performs a unary Connect call with a protobuf body.
func devinUnary(ctx context.Context, hc *http.Client, endpoint string, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, devinRPCTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/proto")
	req.Header.Set("Connect-Protocol-Version", "1")
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, connectMaxFrame))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		e := devinErrorFromBody(resp.StatusCode, raw)
		e.retryAfter = parseUsageRetryAfter(resp.Header.Get("Retry-After"))
		return nil, e
	}
	// A Connect error envelope can ride a 200 when a proxy in the middle
	// answered for the server. A protobuf payload never starts with '{'.
	if len(raw) > 0 && raw[0] == '{' {
		if e := devinErrorFromBody(resp.StatusCode, raw); e.code != "" {
			return nil, e
		}
	}
	return raw, nil
}

// VerifyDevinCredential resolves the credential a devin provider would use
// and proves it by minting a user JWT. It reports the source and the account.
func VerifyDevinCredential(ctx context.Context, hc *http.Client, explicitKey, authPath string) (DevinAuthStatus, DevinAccount, error) {
	cred, err := resolveDevinCredential(explicitKey, authPath)
	if err != nil {
		return DevinAuthStatus{}, DevinAccount{}, err
	}
	st := DevinAuthStatus{Connected: true, Source: cred.source, Path: cred.path, Masked: maskDevinToken(cred.token)}
	jwt, err := devinJWTFor(ctx, hc, cred)
	if err != nil {
		return st, DevinAccount{}, err
	}
	st.Email = jwt.account.Email
	return st, jwt.account, nil
}

// DevinLoginPrompt is handed to the caller once the loopback server listens.
type DevinLoginPrompt struct {
	AuthURL     string
	RedirectURI string
}

// DevinSignInOptions shapes a browser sign-in.
type DevinSignInOptions struct {
	// OnPrompt prints the sign-in URL (and may open a browser).
	OnPrompt func(DevinLoginPrompt)
	// Paste, when set, is read line by line while the sign-in waits: a line
	// holding the address the browser was sent back to (or just its code)
	// completes the sign-in. That is the way in when the browser runs on
	// another machine and cannot reach this one's loopback port.
	Paste io.Reader
	// OnPasteRejected reports a pasted line that completes nothing.
	OnPasteRejected func(reason string)
}

// DevinSignIn runs the PKCE browser sign-in of the Devin CLI: a loopback
// server on 127.0.0.1 receives the authorization code, the code is exchanged
// for a session token, the token is proven by minting a user JWT, and the
// result is stored at authPath.
//
// The state is compared in constant time and a mismatching callback is
// answered 400 while the wait goes on, so a local process cannot abort the
// sign-in; the PKCE verifier never leaves this process until the exchange.
func DevinSignIn(ctx context.Context, hc *http.Client, authPath string, opts DevinSignInOptions) (DevinAccount, error) {
	if strings.TrimSpace(authPath) == "" {
		return DevinAccount{}, errors.New("devin auth: credential path is empty")
	}
	verifier, err := randomURLToken(32)
	if err != nil {
		return DevinAccount{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return DevinAccount{}, err
	}
	state := hex.EncodeToString(stateBytes)

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return DevinAccount{}, fmt.Errorf("devin auth: listen on loopback: %w", err)
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)

	ctx, cancel := context.WithTimeout(ctx, devinLoginTimeout)
	defer cancel()

	type outcome struct {
		code string
		err  error
	}
	codeCh := make(chan outcome, 1)
	var once sync.Once
	deliver := func(o outcome) { once.Do(func() { codeCh <- o }) }

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, devinPage("Request rejected", "The state check failed. Go back to the terminal and sign in again."))
			return
		}
		if e := q.Get("error"); e != "" {
			desc := q.Get("error_description")
			if desc == "" {
				desc = e
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, devinPage("Sign-in failed", html.EscapeString(desc)))
			deliver(outcome{err: fmt.Errorf("devin auth: sign-in refused: %s", desc)})
			return
		}
		code := strings.TrimSpace(q.Get("code"))
		if code == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, devinPage("No code received", "Devin sent no authorization code. Sign in again."))
			return
		}
		_, _ = io.WriteString(w, devinPage("Coddy is signing in to Devin", "You can close this tab and go back to the terminal."))
		deliver(outcome{code: code})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
		}
	}()

	if opts.Paste != nil {
		go func() {
			sc := bufio.NewScanner(opts.Paste)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" {
					continue
				}
				code, err := devinCodeFromPaste(line, state)
				if err != nil {
					if opts.OnPasteRejected != nil {
						opts.OnPasteRejected(err.Error())
					}
					continue
				}
				deliver(outcome{code: code})
				return
			}
		}()
	}

	q := url.Values{}
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("prompt", "select_account")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	authURL := envOr(EnvDevinWebappURL, devinDefaultWebapp) + "/auth/cli/continue?" + q.Encode()
	if opts.OnPrompt != nil {
		opts.OnPrompt(DevinLoginPrompt{AuthURL: authURL, RedirectURI: redirectURI})
	}

	var code string
	select {
	case o := <-codeCh:
		if o.err != nil {
			return DevinAccount{}, o.err
		}
		code = o.code
	case <-ctx.Done():
		select {
		case o := <-codeCh:
			if o.err != nil {
				return DevinAccount{}, o.err
			}
			code = o.code
		default:
			return DevinAccount{}, fmt.Errorf("devin auth: sign-in not completed: %w", ctx.Err())
		}
	}

	token, apiServer, err := exchangeDevinCode(ctx, hc, code, verifier)
	if err != nil {
		return DevinAccount{}, err
	}
	cred := devinCredential{token: normalizeDevinToken(token), apiServer: apiServer, source: DevinSourceCoddy, path: authPath}
	jwt, err := devinJWTFor(ctx, hc, cred)
	if err != nil {
		return DevinAccount{}, fmt.Errorf("devin auth: the new session token does not work: %w", err)
	}
	if err := saveDevinAuth(authPath, devinAuthFile{SessionToken: cred.token, APIServerURL: apiServer, Email: jwt.account.Email}); err != nil {
		return DevinAccount{}, err
	}
	return jwt.account, nil
}

// devinCodeFromPaste reads the authorization code out of a pasted line: the
// whole address the browser landed on, its query string, or the bare code.
func devinCodeFromPaste(line, state string) (string, error) {
	line = strings.Trim(strings.TrimSpace(line), `"'`)
	if strings.Contains(line, "code=") {
		raw := line
		if i := strings.IndexByte(raw, '?'); i >= 0 {
			raw = raw[i+1:]
		}
		if i := strings.IndexByte(raw, '#'); i >= 0 {
			raw = raw[:i]
		}
		q, err := url.ParseQuery(raw)
		if err != nil {
			return "", fmt.Errorf("could not read that address: %v", err)
		}
		// The address the browser lands on always carries the state; one
		// without it, or with another, did not come from this sign-in.
		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			return "", errors.New("that address belongs to another sign-in (state does not match)")
		}
		if code := strings.TrimSpace(q.Get("code")); code != "" {
			return code, nil
		}
		return "", errors.New("that address carries no code")
	}
	// A bare code is not taken: without the state next to it nothing ties it
	// to this sign-in, and a wrong one would end the wait on a failed exchange.
	return "", errors.New("paste the whole address the browser ended on after signing in")
}

// exchangeDevinCode trades the authorization code and PKCE verifier for a
// session token at the Devin API (the endpoint the Devin CLI's browser flow
// uses). An API that no longer serves it (404, 405) falls back to the
// Connect method of the API server that does the same exchange.
func exchangeDevinCode(ctx context.Context, hc *http.Client, code, verifier string) (token, apiServer string, err error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	body, _ := json.Marshal(map[string]string{"code": code, "code_verifier": verifier})
	status, raw, err := devinPostJSON(ctx, hc, envOr(EnvDevinAPIURL, devinDefaultAPIURL)+"/auth/cli/token", body)
	if err != nil {
		return "", "", fmt.Errorf("devin auth: exchange the sign-in code: %w", err)
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		body, _ = json.Marshal(map[string]string{"code": code, "codeVerifier": verifier})
		status, raw, err = devinPostJSON(ctx, hc, devinAPIServer("")+"/exa.seat_management_pb.SeatManagementService/ExchangeDevinCLIPKCECode", body)
		if err != nil {
			return "", "", fmt.Errorf("devin auth: exchange the sign-in code: %w", err)
		}
	}
	if status != http.StatusOK {
		return "", "", fmt.Errorf("devin auth: exchange the sign-in code: %w", devinErrorFromBody(status, raw))
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("devin auth: exchange answer is not JSON: %w", err)
	}
	for _, k := range []string{"token", "session_token", "sessionToken", "api_key", "apiKey"} {
		if v, ok := out[k].(string); ok && strings.TrimSpace(v) != "" {
			token = v
			break
		}
	}
	for _, k := range []string{"api_server_url", "apiServerUrl"} {
		if v, ok := out[k].(string); ok && strings.TrimSpace(v) != "" {
			apiServer = strings.TrimRight(strings.TrimSpace(v), "/")
			break
		}
	}
	if token == "" {
		return "", "", errors.New("devin auth: the code exchange returned no session token")
	}
	return token, apiServer, nil
}

func devinPostJSON(ctx context.Context, hc *http.Client, endpoint string, body []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, devinRPCTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, raw, err
}

func randomURLToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// devinPage renders the minimal self-contained page of the loopback server.
func devinPage(title, body string) string {
	return fmt.Sprintf(
		"<!doctype html><meta charset=utf-8><title>%s</title>"+
			"<body style=\"font-family:system-ui;background:#08090C;color:#eaeaea;text-align:center;padding:64px 20px\">"+
			"<h2>%s</h2><p>%s</p></body>",
		html.EscapeString(title), html.EscapeString(title), body)
}

// LogDevinAuthNotices writes one startup line per devin provider naming the
// credential it will use, or a warning when it has none. No network call.
func LogDevinAuthNotices(log *slog.Logger, cfg *config.Config) {
	if log == nil || cfg == nil {
		return
	}
	for i := range cfg.Providers {
		prov := &cfg.Providers[i]
		if prov.Type != "devin" {
			continue
		}
		if strings.TrimSpace(prov.APIKey) != "" || strings.TrimSpace(prov.APIKeyCommand) != "" {
			log.Info("devin credential", "provider", prov.Name, "detail", "session token from the provider's api_key")
			continue
		}
		st, err := InspectDevinAuth(config.DevinAuthPath(cfg.Paths.Home, prov.Name))
		switch {
		case err != nil:
			log.Warn("devin credential", "provider", prov.Name, "detail", err.Error())
		case !st.Connected:
			if env := config.ProviderAPIKeyEnvVarName(prov.Name); env != "" && strings.TrimSpace(os.Getenv(env)) != "" {
				log.Info("devin credential", "provider", prov.Name, "detail", "session token from "+env)
				continue
			}
			log.Warn("devin credential", "provider", prov.Name, "detail", fmt.Sprintf("not signed in, run `coddy providers login %s`", prov.Name))
		case st.Source == DevinSourceDevinCLI:
			log.Info("devin credential", "provider", prov.Name, "detail", "Devin CLI login "+st.Path)
		default:
			log.Info("devin credential", "provider", prov.Name, "detail", "signed in (Coddy-managed credential)")
		}
	}
}

// writePrivateFileAtomic writes data to path with 0600 through a temp file in
// the same directory, so a crash never leaves a truncated file and the data
// never exists on disk with looser permissions.
func writePrivateFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		// Rename over an existing file is unreliable on Windows; the callers
		// hold their own lock, so removing first is safe.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Rename(tmpName, path)
}

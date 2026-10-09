package dryrun

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/netx"
	"github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

const defaultTelegramAPIBase = "https://api.telegram.org"

// listeners tries each address the command would bind, once, and lets go.
func (r *runner) listeners() {
	for _, l := range r.req.Listeners {
		ln, err := net.Listen("tcp", l.Addr)
		if err == nil {
			_ = ln.Close()
			r.rep.add(r.check(StatusOK, l.Path, l.Path+".port", l.Addr+" is free to bind", ""))
			continue
		}
		flag := "-P"
		if l.Path == "swarm" {
			flag = "--swarm-port"
		}
		if isAddrInUse(err) {
			r.rep.add(r.check(StatusError, l.Path, l.Path+".port", l.Addr+" is in use",
				fmt.Sprintf("another process listens there (a running coddy serve? see `coddy serve status`); stop it or change %s.port / %s", l.Path, flag)))
			continue
		}
		r.rep.add(r.check(StatusError, l.Path, l.Path+".port", fmt.Sprintf("cannot bind %s: %s", l.Addr, shortErr(err)),
			fmt.Sprintf("check %s.host and %s.port; ports below 1024 need privileges", l.Path, l.Path)))
	}
}

func isAddrInUse(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") || strings.Contains(msg, "only one usage of each socket address")
}

// telegramProbes checks the bot token against the Bot API when the gateway
// is enabled: getMe answers 401 for a revoked or mistyped token.
func (r *runner) telegramProbes() []probe {
	tg := &r.req.Cfg.Gateways.Telegram
	if !tg.Enabled {
		return nil
	}
	return []probe{func(ctx context.Context) []Check {
		const path = "gateways.telegram"
		token := tg.EffectiveToken()
		if token == "" {
			return []Check{r.check(StatusError, path, path, "no token: gateways.telegram.token is empty and "+config.TelegramBotTokenEnvVar+" is not set",
				"set gateways.telegram.token or export "+config.TelegramBotTokenEnvVar)}
		}
		hc, err := llm.HTTPClientForOptionalProxy(tg.Proxy)
		if err != nil {
			return []Check{r.check(StatusError, path, path+".proxy", "proxy: "+err.Error(), "fix gateways.telegram.proxy")}
		}
		if hc == nil {
			hc = &http.Client{}
		}
		base := strings.TrimRight(strings.TrimSpace(os.Getenv(config.TelegramAPIBaseEnv)), "/")
		if base == "" {
			base = defaultTelegramAPIBase
		}
		redact := func(s string) string { return strings.ReplaceAll(s, token, "<token>") }
		status, body, err := r.get(ctx, hc, base+"/bot"+token+"/getMe", nil, "")
		if err != nil {
			fix := "check the network"
			if strings.TrimSpace(tg.Proxy) != "" {
				fix += " and gateways.telegram.proxy"
			}
			return []Check{r.check(StatusError, path, path, fmt.Sprintf("cannot reach %s: %s", base, redact(shortErr(err))), fix)}
		}
		switch status {
		case http.StatusOK:
			var me struct {
				OK     bool `json:"ok"`
				Result struct {
					Username string `json:"username"`
				} `json:"result"`
			}
			if jerr := json.Unmarshal(body, &me); jerr != nil || !me.OK {
				return []Check{r.check(StatusWarning, path, path, "the Bot API answered, but not with a bot description", "check that "+base+" is the Bot API")}
			}
			return []Check{r.check(StatusOK, path, path, "token accepted by the Bot API, bot @"+me.Result.Username, "")}
		case http.StatusUnauthorized, http.StatusNotFound:
			return []Check{r.check(StatusError, path, path+".token", fmt.Sprintf("token rejected by the Bot API (HTTP %d)", status),
				"check gateways.telegram.token: a revoked or mistyped token is answered like this; @BotFather issues a new one")}
		default:
			return []Check{r.check(StatusError, path, path, fmt.Sprintf("the Bot API answered HTTP %d", status), "try again later or check gateways.telegram.proxy")}
		}
	}}
}

// pachcaRequiredScopes are the scopes the Pachca bot cannot work without;
// pachcaRecommendedScopes the ones it works without, at a cost.
var (
	pachcaRequiredScopes = []string{
		"messages:create", "messages:update", "messages:read",
		"chats:read", "profile:read", "webhooks:events:read",
	}
	pachcaRecommendedScopes = []string{"webhooks:events:delete", "users:read"}
)

// pachcaProbes checks the Pachca bot token when that bot is enabled:
// GET /oauth/token/info needs no scope, answers 401 for a revoked token and
// lists the scopes a live one carries.
func (r *runner) pachcaProbes() []probe {
	pc := &r.req.Cfg.Gateways.Pachca
	if !pc.Enabled {
		return nil
	}
	return []probe{func(ctx context.Context) []Check {
		const path = "gateways.pachca"
		token := pc.EffectiveToken()
		if token == "" {
			return []Check{r.check(StatusError, path, path, "no token: gateways.pachca.token is empty and "+config.PachcaBotTokenEnvVar+" is not set",
				"set gateways.pachca.token or export "+config.PachcaBotTokenEnvVar)}
		}
		hc, err := llm.HTTPClientForOptionalProxy(pc.Proxy)
		if err != nil {
			return []Check{r.check(StatusError, path, path+".proxy", "proxy: "+err.Error(), "fix gateways.pachca.proxy")}
		}
		if hc == nil {
			hc = &http.Client{}
		}
		base := strings.TrimRight(strings.TrimSpace(os.Getenv(config.PachcaAPIBaseEnv)), "/")
		if base == "" {
			base = config.DefaultPachcaAPIBase
		}
		redact := func(s string) string { return strings.ReplaceAll(s, token, "<token>") }
		status, body, err := r.get(ctx, hc, base+"/oauth/token/info", nil, token)
		if err != nil {
			fix := "check the network"
			if strings.TrimSpace(pc.Proxy) != "" {
				fix += " and gateways.pachca.proxy"
			}
			return []Check{r.check(StatusError, path, path, fmt.Sprintf("cannot reach %s: %s", base, redact(shortErr(err))), fix)}
		}
		switch status {
		case http.StatusOK:
			var info struct {
				Data struct {
					UserID int64    `json:"user_id"`
					Scopes []string `json:"scopes"`
				} `json:"data"`
			}
			if jerr := json.Unmarshal(body, &info); jerr != nil || info.Data.UserID == 0 {
				return []Check{r.check(StatusWarning, path, path, "Pachca answered, but not with a token description", "check that "+base+" is the Pachca API")}
			}
			have := map[string]bool{}
			for _, s := range info.Data.Scopes {
				have[s] = true
			}
			var missing, recommended []string
			for _, s := range pachcaRequiredScopes {
				if !have[s] {
					missing = append(missing, s)
				}
			}
			for _, s := range pachcaRecommendedScopes {
				if !have[s] {
					recommended = append(recommended, s)
				}
			}
			if len(missing) > 0 {
				return []Check{r.check(StatusError, path, path+".token", "the token lacks scopes the bot needs: "+strings.Join(missing, ", "),
					"grant them in the bot's settings in Pachca (Integrations, the bot, API tab) and copy the new token")}
			}
			if len(recommended) > 0 {
				return []Check{r.check(StatusWarning, path, path+".token", "the token lacks recommended scopes: "+strings.Join(recommended, ", ")+" (without webhooks:events:delete the events history keeps growing, without users:read a quoted reply names no author)",
					"grant them in the bot's settings in Pachca")}
			}
			return []Check{r.check(StatusOK, path, path, fmt.Sprintf("token accepted by Pachca, bot user %d", info.Data.UserID), "")}
		case http.StatusUnauthorized:
			return []Check{r.check(StatusError, path, path+".token", "token rejected by Pachca (HTTP 401)",
				"check gateways.pachca.token: a revoked or mistyped token is answered like this; the bot's settings in Pachca show the current one")}
		default:
			return []Check{r.check(StatusError, path, path, fmt.Sprintf("Pachca answered HTTP %d", status), "try again later or check gateways.pachca.proxy")}
		}
	}}
}

// gatewayAdmins warns about an enabled bot with no admins: everybody it lets
// in then only chats - nothing is approved, and /resume, /app, the Mini App
// and the settings of a group are nobody's.
func (r *runner) gatewayAdmins() {
	gw := r.req.Cfg.Gateways
	for _, b := range []struct {
		path    string
		enabled bool
		admins  int
	}{
		{"gateways.telegram", gw.Telegram.Enabled, len(gw.Telegram.Admins)},
		{"gateways.pachca", gw.Pachca.Enabled, len(gw.Pachca.Admins)},
	} {
		if !b.enabled || b.admins > 0 {
			continue
		}
		r.rep.add(r.check(StatusWarning, b.path+".admins", b.path+".enable",
			"the bot has no admins: everybody it lets in only chats - the agent gets no approval for a command, a write or a request, and the settings, /resume and the web UI are nobody's",
			"list the messenger user ids of the people who may run the agent in full under "+b.path+".admins"))
	}
}

// corsOpen says when the CORS policy admits pages nobody listed - allow_loopback
// or "*" - on a server that asks for no credential: every page served from the
// browser's own machine (a dev server, a desktop app), or with "*" every page
// anywhere, can then read this API, /coddy/config and the provider keys in it
// included. The bind address does not matter; a loopback bind is exactly where
// such pages reach. httpserver.allow_insecure silences it like the startup
// warning it mirrors.
func (r *runner) corsOpen() {
	c := r.req.Cfg.HTTPServer.CORS
	if !r.req.WebUIOpen || !c.OpenToUnlistedOrigins() {
		return
	}
	const path = "httpserver.cors"
	loc, what := path+".allow_loopback", "every page served from the browser's own machine (allow_loopback: a dev server, a desktop app's page)"
	// The list is read before allow_loopback, so a "*" in it opens the API to
	// every page anywhere whether or not the toggle is on as well.
	for _, o := range c.AllowedOrigins {
		if strings.TrimSpace(o) == "*" {
			loc, what = path+".allowed_origins", "any page anywhere (allowed_origins: \"*\")"
			break
		}
	}
	r.rep.add(r.check(StatusWarning, path, loc,
		"CORS admits "+what+" and the API asks for no credential: such a page can read /coddy/config, provider keys included, and run the agent",
		"set a token (httpserver.auth_token / --auth-token / "+httpserver.TokenEnvVar+") or run `coddy serve set-password`; httpserver.allow_insecure: true silences this"))
}

// miniApp says when the bot will not advertise the web UI it is told to offer
// as its Mini App: the web UI of this process asks for no sign-in.
func (r *runner) miniApp() {
	tg := &r.req.Cfg.Gateways.Telegram
	if !tg.Enabled || strings.TrimSpace(tg.MiniApp.URL) == "" || !r.req.WebUIOpen {
		return
	}
	const path = "gateways.telegram.mini_app"
	r.rep.add(r.check(StatusWarning, path, path+".url",
		"the bot will not advertise the web UI as its Mini App: the web UI asks for no sign-in, and its menu button is shown to everybody who opens the bot",
		"run `coddy serve set-password` (or set "+httpserver.LoginUserEnvVar+" / "+httpserver.LoginPasswordEnvVar+", or a token), or set httpserver.allow_insecure: true to publish it open"))
}

// miniAppProbes asks the address the bot gives Telegram for the web UI. It is
// the operator's public address, often behind a TLS proxy this machine cannot
// always reach itself, so an answer that is not the web UI is a warning.
func (r *runner) miniAppProbes() []probe {
	tg := &r.req.Cfg.Gateways.Telegram
	target := strings.TrimSpace(tg.MiniApp.URL)
	if !tg.Enabled || target == "" {
		return nil
	}
	return []probe{func(ctx context.Context) []Check {
		const path = "gateways.telegram.mini_app.url"
		status, body, err := r.get(ctx, &http.Client{}, target, map[string]string{"Accept": "text/html"}, "")
		switch {
		case err != nil:
			return []Check{r.check(StatusWarning, path, path, fmt.Sprintf("cannot reach %s from this machine: %s", target, shortErr(err)),
				"Telegram opens it from the person's phone: check the address, its TLS certificate and the proxy in front of coddy serve")}
		case status != http.StatusOK:
			return []Check{r.check(StatusWarning, path, path, fmt.Sprintf("%s answered HTTP %d", target, status),
				"the address should serve the web UI of coddy serve")}
		case !strings.Contains(string(body), `id="root"`):
			return []Check{r.check(StatusWarning, path, path, fmt.Sprintf("%s answered, but not with the web UI", target),
				"the address should serve the web UI of coddy serve (built with the ui tag)")}
		default:
			return []Check{r.check(StatusOK, path, path, "the web UI answers at "+target, "")}
		}
	}}
}

// mcpRemoteProbes asks every remote MCP server of <home>/mcp.json for any
// HTTP answer. Project-local .coddy/mcp.json declarations are not contacted:
// they sit behind the workspace trust gate, and a dry run must not be the
// thing that reaches out to them.
func (r *runner) mcpRemoteProbes() []probe {
	file, servers, err := r.globalMCPServers()
	if err != nil {
		// mcpCommands reports the file that does not read.
		return nil
	}
	var out []probe
	for i := range servers {
		srv := &servers[i]
		if srv.Disabled || strings.TrimSpace(srv.URL) == "" {
			continue
		}
		out = append(out, func(ctx context.Context) []Check {
			path := mcpCheckPath(srv.Name)
			raw := strings.TrimSpace(config.ExpandMCPValue(srv.URL, r.req.Paths.CWD))
			if u, err := url.Parse(raw); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return []Check{r.check(StatusError, path, path, fmt.Sprintf("url %q is not an http(s) address", raw), "write the server's full URL, for example https://host/mcp, as the url of "+srv.Name+" in "+file)}
			}
			headers := map[string]string{}
			for _, h := range srv.Headers {
				headers[h.Name] = config.ExpandMCPValue(h.Value, r.req.Paths.CWD)
			}
			status, _, err := r.get(ctx, &http.Client{}, raw, headers, "")
			if err != nil {
				return []Check{r.check(StatusError, path, path, fmt.Sprintf("cannot reach %s: %s", raw, shortErr(err)), "check the url of "+srv.Name+" in "+file+" and that the server is running")}
			}
			return []Check{r.check(StatusOK, path, path, fmt.Sprintf("%s answers (HTTP %d)", raw, status), "")}
		})
	}
	return out
}

// remoteProbes checks the remotes config.yaml names and the --remote target
// of this run. A configured remote that is down is a warning - it is used
// only when asked for - while a --remote target that rejects the token or
// cannot be reached would fail the very command being dry-run. Each is asked
// with the token it would be used with: a configured entry's own, the run's
// for the target.
func (r *runner) remoteProbes() []probe {
	var out []probe
	for i := range r.req.Cfg.HTTPServer.Remotes {
		rem := r.req.Cfg.HTTPServer.Remotes[i]
		if strings.TrimSpace(rem.URL) == "" {
			continue
		}
		out = append(out, func(ctx context.Context) []Check {
			base := strings.TrimRight(strings.TrimSpace(rem.URL), "/")
			token := strings.TrimSpace(rem.Token)
			a := r.askRemote(ctx, &http.Client{}, base, token)
			return []Check{r.check(configuredRemoteVerdict(rem, a))}
		})
	}
	if ropts := r.req.Remote; ropts != nil {
		out = append(out, func(ctx context.Context) []Check {
			hc := ropts.HTTPClient
			if hc == nil {
				hc = &http.Client{}
			}
			base := strings.TrimRight(ropts.BaseURL, "/")
			return []Check{remoteTargetVerdict(base, r.askRemote(ctx, hc, base, ropts.Token))}
		})
	}
	return out
}

// remoteAnswer is what a remote said to the questions a client asks it: its
// model catalog, and when there is none, whether it is a swarm relay and
// whether the relay takes the token.
type remoteAnswer struct {
	err    error
	status int // GET /v1/models
	relay  bool
	// nodesStatus is the relay's answer to GET /swarm/nodes with the token;
	// agents are the agent nodes it listed.
	nodesStatus int
	agents      []string
}

func (r *runner) askRemote(ctx context.Context, hc *http.Client, base, token string) remoteAnswer {
	var a remoteAnswer
	a.status, _, a.err = r.get(ctx, hc, base+"/v1/models", nil, token)
	if a.err != nil || a.status == http.StatusOK {
		return a
	}
	// A relay serves no /v1 at all and answers 404 whatever the token; its
	// public info route says what it is.
	status, body, err := r.get(ctx, hc, base+"/swarm/info", nil, "")
	var info struct {
		Swarm bool `json:"swarm"`
	}
	if err != nil || status != http.StatusOK || json.Unmarshal(body, &info) != nil || !info.Swarm {
		return a
	}
	a.relay = true
	status, body, err = r.get(ctx, hc, base+"/swarm/nodes", nil, token)
	if err != nil {
		return a
	}
	a.nodesStatus = status
	var list struct {
		Nodes []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"nodes"`
	}
	if status == http.StatusOK && json.Unmarshal(body, &list) == nil {
		for _, n := range list.Nodes {
			if n.Kind == "agent" && n.Name != "" {
				a.agents = append(a.agents, n.Name)
			}
		}
	}
	return a
}

func refused(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

// configuredRemoteVerdict judges an entry of httpserver.remotes. It is used
// only when asked for, so nothing here is worse than a warning.
func configuredRemoteVerdict(rem config.HTTPRemote, a remoteAnswer) (Status, string, string, string, string) {
	path := "httpserver.remotes[" + rem.Name + "]"
	withToken := strings.TrimSpace(rem.Token) != ""
	switch {
	case a.err != nil:
		return StatusWarning, path, path + ".url", fmt.Sprintf("cannot reach %s: %s", rem.URL, shortErr(a.err)),
			"the remote is only used with --remote " + rem.Name + "; check the url and that coddy serve runs there"
	case a.relay && a.nodesStatus == http.StatusOK:
		msg := fmt.Sprintf("%s is a swarm relay with %d agents", rem.URL, len(a.agents))
		if withToken {
			msg += " and accepts the token"
		}
		return StatusOK, path, path + ".url", msg, ""
	case a.relay && refused(a.nodesStatus) && withToken:
		return StatusWarning, path, path + ".token", fmt.Sprintf("the relay %s rejected the token (HTTP %d)", rem.URL, a.nodesStatus),
			"set " + path + ".token to the relay's client token (its swarm.auth_token)"
	case a.relay:
		return StatusOK, path, path + ".url", rem.URL + " is a swarm relay; its client token comes from the browser or --remote-token", ""
	case a.status == http.StatusOK && withToken:
		return StatusOK, path, path + ".url", rem.URL + " accepts the token", ""
	case refused(a.status) && withToken:
		return StatusWarning, path, path + ".token", fmt.Sprintf("%s rejected the token (HTTP %d)", rem.URL, a.status),
			"set " + path + ".token to the server's httpserver.auth_token"
	case a.status == http.StatusNotFound:
		return StatusWarning, path, path + ".url", fmt.Sprintf("%s answered HTTP 404 and is neither a coddy serve nor a swarm relay", rem.URL),
			"check " + path + ".url"
	default:
		return StatusOK, path, path + ".url", fmt.Sprintf("%s answers (HTTP %d)", rem.URL, a.status), ""
	}
}

// remoteTargetVerdict judges the --remote target, which the command being
// dry-run is about to drive.
func remoteTargetVerdict(base string, a remoteAnswer) Check {
	const path = "--remote"
	switch {
	case a.err != nil:
		return Check{Status: StatusError, Path: path, Message: fmt.Sprintf("cannot reach %s: %s", base, shortErr(a.err)), Fix: "check the address and that coddy serve runs there"}
	case a.status == http.StatusOK:
		return Check{Status: StatusOK, Path: path, Message: base + " accepts the token"}
	case a.relay && refused(a.nodesStatus):
		return Check{Status: StatusError, Path: path, Message: fmt.Sprintf("%s is a swarm relay and rejected the token (HTTP %d)", base, a.nodesStatus),
			Fix: "pass the relay's client token (its swarm.auth_token) with --remote-token, CODDY_REMOTE_TOKEN or the token of its httpserver.remotes entry"}
	case a.relay:
		// A relay drives nothing itself: its nodes are what a client talks to.
		fix := "point --remote at a node mounted under it: " + base + swarm.MountPath + "<name>"
		if len(a.agents) > 0 {
			mounts := make([]string, 0, len(a.agents))
			for _, n := range a.agents {
				mounts = append(mounts, base+swarm.MountPath+n)
			}
			fix = "point --remote at a node mounted under it: " + strings.Join(mounts, ", ")
		}
		return Check{Status: StatusError, Path: path, Message: base + " is a swarm relay, which serves no sessions of its own", Fix: fix}
	case refused(a.status):
		return Check{Status: StatusError, Path: path, Message: fmt.Sprintf("%s rejected the token (HTTP %d)", base, a.status),
			Fix: "pass --remote-token or set CODDY_REMOTE_TOKEN to the server's httpserver.auth_token"}
	default:
		return Check{Status: StatusWarning, Path: path, Message: fmt.Sprintf("%s answered HTTP %d", base, a.status), Fix: "check that the address is a coddy serve server"}
	}
}

// swarmProbes reaches the relays this node joins and, for a relay, the
// upstreams it mounts, through the dial settings each entry carries. Both
// are coddy serve's business; the console and acp skip them.
func (r *runner) swarmProbes() []probe {
	if !r.serveOnly() {
		return nil
	}
	cfg := r.req.Cfg
	var out []probe
	for i := range cfg.Swarm.Join {
		j := cfg.Swarm.Join[i]
		path := fmt.Sprintf("swarm.join[%d]", i)
		out = append(out, func(ctx context.Context) []Check {
			return r.probeSwarmPeer(ctx, path, j.URL, j.Dial, StatusError, "check "+path+".url and its dial settings (proxy, ca_file)")
		})
	}
	if cfg.Swarm.Enabled {
		for i := range cfg.Swarm.Upstreams {
			up := cfg.Swarm.Upstreams[i]
			path := fmt.Sprintf("swarm.upstreams[%d]", i)
			out = append(out, func(ctx context.Context) []Check {
				return r.probeSwarmPeer(ctx, path, up.URL, up.Dial, StatusWarning, "the relay starts without it and keeps retrying; check "+path+".url")
			})
		}
	}
	return out
}

func (r *runner) probeSwarmPeer(ctx context.Context, path, rawURL string, dial config.SwarmDialConfig, onFail Status, fix string) []Check {
	var out []Check
	if dial.Auto && r.builtinPending {
		return append(out, r.check(StatusSkipped, path, path+".url", "not probed: the built-in certificates of "+path+".dial are not made yet", ""))
	}
	df := r.req.Cfg.DialFiles(dial)
	if ca := df.CAFile; ca != "" {
		caCheck := r.caFileCheck(path+".dial.ca_file", ca)
		out = append(out, caCheck)
		if caCheck.Status == StatusError {
			return append(out, r.check(StatusSkipped, path, path+".url", "not probed: "+path+".dial.ca_file is unusable", ""))
		}
	}
	// The dial pair the leg presents when the other end asks for a certificate: loaded and checked here, and presented by the probe
	// as the real join and the real mount present it.
	pairChecks := r.clientPairChecks(path+".dial.cert_file", df.CertFile, df.KeyFile)
	out = append(out, pairChecks...)
	if hasIdentityError(pairChecks) {
		return append(out, r.check(StatusSkipped, path, path+".url", "not probed: "+path+".dial.cert_file is unusable", ""))
	}
	opts := netx.Options{Proxy: dial.Proxy, CAFile: df.CAFile, CertFile: df.CertFile, KeyFile: df.KeyFile, InsecureSkipVerify: dial.InsecureSkipVerify}
	hc, err := opts.HTTPClient()
	if err != nil {
		return append(out, r.check(StatusError, path, path+".dial", "dial settings: "+err.Error(), "fix "+path+".dial"))
	}
	status, _, err := r.get(ctx, hc, strings.TrimSpace(rawURL), nil, "")
	if err != nil {
		return append(out, r.check(onFail, path, path+".url", fmt.Sprintf("cannot reach %s: %s", rawURL, shortErr(err)), fix))
	}
	return append(out, r.check(StatusOK, path, path+".url", fmt.Sprintf("%s answers (HTTP %d)", rawURL, status), ""))
}

// get performs one bounded GET and returns the status and up to 64 KiB of
// body; the body is what the caller inspects, the status is the answer.
func (r *runner) get(ctx context.Context, hc *http.Client, target string, headers map[string]string, bearer string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.req.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, body, nil
}

// webLogin reports the state of the optional web sign-in.
//
// It is a local check rather than a probe: the two questions it answers are
// about the configuration this process would start with, and both are the kind
// of thing an operator learns too late otherwise. One is a form asked for with
// no account behind it, which makes the server refuse to start. The other is a
// server closed with a password and no bearer token, which leaves everything
// that is not a browser - `coddy --remote`, `coddy acp --remote`, a swarm relay
// mounting this node, scripts - without a credential on its next call.
func (r *runner) webLogin() {
	if r.req.Surface != SurfaceServe || !r.req.Cfg.HTTPServer.IsEnabled() {
		return
	}
	login := &r.req.Cfg.HTTPServer.Login
	if login.IsExplicitlyDisabled() {
		r.rep.add(r.check(StatusSkipped, "httpserver.login", "httpserver.login.enable",
			"web sign-in is switched off", ""))
		return
	}
	envUser := strings.TrimSpace(os.Getenv(httpserver.LoginUserEnvVar))
	envPassword := os.Getenv(httpserver.LoginPasswordEnvVar)
	source := ""
	switch {
	case envUser != "" && envPassword != "":
		source = "env"
	case login.HasAccount():
		source = "config"
	}
	if source == "" {
		if login.IsExplicitlyEnabled() {
			r.rep.add(r.check(StatusError, "httpserver.login", "httpserver.login.enable",
				"web sign-in is enabled but no account is configured",
				"run `coddy serve set-password`, or set "+httpserver.LoginUserEnvVar+" and "+
					httpserver.LoginPasswordEnvVar+" (e.g. in <home>/.env)"))
			return
		}
		r.rep.add(r.check(StatusSkipped, "httpserver.login", "httpserver.login",
			"no web sign-in configured", ""))
		return
	}
	// Half an account in the environment is a typo worth naming: it looks set
	// and does nothing.
	if source == "config" && (envUser != "" || envPassword != "") {
		r.rep.add(r.check(StatusWarning, "httpserver.login", "httpserver.login",
			"only one of "+httpserver.LoginUserEnvVar+" / "+httpserver.LoginPasswordEnvVar+" is set, so the file's account is used",
			"set both variables or neither"))
	}
	tokens := len(r.req.Cfg.HTTPServer.EffectiveAuthTokens()) > 0 || strings.TrimSpace(os.Getenv(httpserver.TokenEnvVar)) != ""
	if !tokens {
		r.rep.add(r.check(StatusWarning, "httpserver.login", "httpserver.login",
			"web sign-in is on ("+source+") and no bearer token is set",
			"API clients (coddy --remote, coddy acp --remote, a swarm relay, scripts) authenticate with a token: set httpserver.auth_token, --auth-token or "+httpserver.TokenEnvVar))
		return
	}
	r.rep.add(r.check(StatusOK, "httpserver.login", "httpserver.login",
		"web sign-in is configured ("+source+"), with a bearer token for API clients", ""))
}

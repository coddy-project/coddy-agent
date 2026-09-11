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

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/netx"
)

// TelegramAPIBaseEnv overrides the Bot API origin the Telegram probe talks
// to (default https://api.telegram.org), for tests and stand-ins.
const TelegramAPIBaseEnv = "CODDY_TELEGRAM_API_BASE"

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
		base := strings.TrimRight(strings.TrimSpace(os.Getenv(TelegramAPIBaseEnv)), "/")
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

// mcpRemoteProbes asks every remote MCP server of config.yaml for any HTTP
// answer. Project-local .coddy/mcp.json declarations are not contacted: they
// sit behind the workspace trust gate, and a dry run must not be the thing
// that reaches out to them.
func (r *runner) mcpRemoteProbes() []probe {
	var out []probe
	for i := range r.req.Cfg.MCPServers {
		srv := &r.req.Cfg.MCPServers[i]
		if srv.Disabled || strings.TrimSpace(srv.URL) == "" {
			continue
		}
		out = append(out, func(ctx context.Context) []Check {
			path := "mcp_servers[" + srv.Name + "]"
			raw := strings.TrimSpace(srv.URL)
			if u, err := url.Parse(raw); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return []Check{r.check(StatusError, path, path+".url", fmt.Sprintf("url %q is not an http(s) address", raw), "write the server's full URL, for example https://host/mcp")}
			}
			headers := map[string]string{}
			for _, h := range srv.Headers {
				headers[h.Name] = h.Value
			}
			status, _, err := r.get(ctx, &http.Client{}, raw, headers, "")
			if err != nil {
				return []Check{r.check(StatusError, path, path+".url", fmt.Sprintf("cannot reach %s: %s", raw, shortErr(err)), "check the url and that the server is running")}
			}
			return []Check{r.check(StatusOK, path, path+".url", fmt.Sprintf("%s answers (HTTP %d)", raw, status), "")}
		})
	}
	return out
}

// remoteProbes checks the remotes config.yaml names and the --remote target
// of this run. A configured remote that is down is a warning - it is used
// only when asked for - while a --remote target that rejects the token or
// cannot be reached would fail the very command being dry-run.
func (r *runner) remoteProbes() []probe {
	var out []probe
	for i := range r.req.Cfg.HTTPServer.Remotes {
		rem := r.req.Cfg.HTTPServer.Remotes[i]
		if strings.TrimSpace(rem.URL) == "" {
			continue
		}
		out = append(out, func(ctx context.Context) []Check {
			path := "httpserver.remotes[" + rem.Name + "]"
			target := strings.TrimRight(strings.TrimSpace(rem.URL), "/") + "/v1/models"
			status, _, err := r.get(ctx, &http.Client{}, target, nil, "")
			if err != nil {
				return []Check{r.check(StatusWarning, path, path+".url", fmt.Sprintf("cannot reach %s: %s", rem.URL, shortErr(err)),
					"the remote is only used with --remote "+rem.Name+"; check the url and that coddy serve runs there")}
			}
			return []Check{r.check(StatusOK, path, path+".url", fmt.Sprintf("%s answers (HTTP %d)", rem.URL, status), "")}
		})
	}
	if ropts := r.req.Remote; ropts != nil {
		out = append(out, func(ctx context.Context) []Check {
			const path = "--remote"
			hc := ropts.HTTPClient
			if hc == nil {
				hc = &http.Client{}
			}
			status, _, err := r.get(ctx, hc, strings.TrimRight(ropts.BaseURL, "/")+"/v1/models", nil, ropts.Token)
			if err != nil {
				return []Check{{Status: StatusError, Path: path, Message: fmt.Sprintf("cannot reach %s: %s", ropts.BaseURL, shortErr(err)), Fix: "check the address and that coddy serve runs there"}}
			}
			switch status {
			case http.StatusOK:
				return []Check{{Status: StatusOK, Path: path, Message: ropts.BaseURL + " accepts the token"}}
			case http.StatusUnauthorized, http.StatusForbidden:
				return []Check{{Status: StatusError, Path: path, Message: fmt.Sprintf("%s rejected the token (HTTP %d)", ropts.BaseURL, status),
					Fix: "pass --remote-token or set CODDY_REMOTE_TOKEN to the server's httpserver.auth_token"}}
			default:
				return []Check{{Status: StatusWarning, Path: path, Message: fmt.Sprintf("%s answered HTTP %d", ropts.BaseURL, status), Fix: "check that the address is a coddy serve server"}}
			}
		})
	}
	return out
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
	if ca := strings.TrimSpace(dial.CAFile); ca != "" {
		caCheck := r.caFileCheck(path+".dial.ca_file", ca)
		out = append(out, caCheck)
		if caCheck.Status == StatusError {
			return append(out, r.check(StatusSkipped, path, path+".url", "not probed: "+path+".dial.ca_file is unusable", ""))
		}
	}
	opts := netx.Options{Proxy: dial.Proxy, CAFile: dial.CAFile, InsecureSkipVerify: dial.InsecureSkipVerify}
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

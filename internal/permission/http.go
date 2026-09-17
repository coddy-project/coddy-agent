package permission

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/web"
)

// OptionAllowAlwaysURL and OptionAllowAlwaysOrigin are the "always" choices of
// an http_request dialog: every later request to this address (any method,
// any query), or to anything on this origin.
const (
	OptionAllowAlwaysURL    = "allow_always_url"
	OptionAllowAlwaysOrigin = "allow_always_origin"
)

// Prefixes of the http_request grant keys kept in the session. A destination
// key covers where a request goes; the others cover what a request to that
// destination carries, and are recorded with it by the same "always" answer.
const (
	httpGrantOrigin   = "origin|"
	httpGrantURL      = "url|"
	httpGrantFile     = "file|"
	httpGrantProxy    = "proxy|"
	httpGrantInsecure = "insecure|"
	httpGrantOutput   = "output|"
)

// httpExtra is something a request carries that approving its destination
// does not approve by itself.
type httpExtra struct {
	kind string
	key  string
}

// httpExtras lists them for one request. A file, a proxy and an unchecked
// certificate are bound to the origin they were approved for: a file approved
// for one service is not approved for the next.
func httpExtras(req *web.HTTPRequest) []httpExtra {
	origin := req.Origin()
	var out []httpExtra
	for _, f := range req.Files {
		out = append(out, httpExtra{kind: "file", key: httpGrantFile + origin + "|" + f.Path})
	}
	if proxy := req.ProxyOrigin(); proxy != "" {
		out = append(out, httpExtra{kind: "proxy", key: httpGrantProxy + origin + "|" + proxy})
	}
	if req.InsecureTLS {
		out = append(out, httpExtra{kind: "insecure", key: httpGrantInsecure + origin})
	}
	if req.OutputFile != "" {
		out = append(out, httpExtra{kind: "output", key: httpGrantOutput + req.OutputFile})
	}
	return out
}

// HTTPRequestAllowedWithSession reports whether an http_request call may run
// without asking, given the permission mode, tools.http_request.allowlist and
// the grants collected in this session's dialogs.
//
// Under bypass nothing asks. Otherwise the destination has to be allowlisted
// or granted, and so does everything the request carries beyond it: a local
// file and an unchecked certificate are covered by an allowlisted destination
// or by their own grant; a proxy is a destination of its own and needs its own
// allowlist entry or grant; the file a response is saved to follows the write
// policy, so accept_edits covers it and ask needs its grant. Arguments the tool
// would refuse still ask - the prompt says why they will fail - so a file that
// appears between the check and the call cannot slip through unapproved.
func HTTPRequestAllowedWithSession(env *tooling.Env, grants []string, argsJSON string) bool {
	if env == nil {
		return false
	}
	if env.PermissionMode == config.PermModeBypass {
		return true
	}
	req, err := web.ParseHTTPRequest(argsJSON, env.CWD)
	if err != nil {
		return false
	}
	allowlisted := config.HTTPAllowlistAllows(env.HTTPAllowlist, req.URL)
	if !allowlisted && !hasGrant(grants, httpGrantOrigin+req.Origin()) && !hasGrant(grants, httpGrantURL+req.Address()) {
		return false
	}
	for _, extra := range httpExtras(req) {
		switch extra.kind {
		case "file", "insecure":
			if allowlisted {
				continue
			}
		case "proxy":
			if config.HTTPAllowlistAllows(env.HTTPAllowlist, req.Proxy) {
				continue
			}
		case "output":
			if env.PermissionMode == config.PermModeAcceptEdits {
				continue
			}
		}
		if !hasGrant(grants, extra.key) {
			return false
		}
	}
	return true
}

// HTTPRequestPromptBody is the permission prompt text for an http_request call:
// the request as it would be sent, and what an "always" answer would cover
// beyond its destination.
func HTTPRequestPromptBody(argsJSON, cwd string) string {
	req, err := web.ParseHTTPRequest(argsJSON, cwd)
	if err != nil {
		return PromptBody(web.ToolHTTPRequest, argsJSON) + "\n\nThe tool will refuse these arguments: " + err.Error()
	}
	text := req.Describe()
	var covers []string
	if len(req.Files) > 0 {
		covers = append(covers, "the files it uploads")
	}
	if req.Proxy != nil {
		covers = append(covers, "the proxy")
	}
	if req.InsecureTLS {
		covers = append(covers, "the unchecked certificate")
	}
	if req.OutputFile != "" {
		covers = append(covers, "the file it saves")
	}
	if len(covers) > 0 {
		text += "\n\nAn always answer also approves " + joinWords(covers) + " for requests to " + req.Origin() + "."
	}
	return text
}

func httpRequestOptions(argsJSON string) []acp.PermissionOption {
	options := []acp.PermissionOption{{OptionID: OptionAllow, Name: "Allow", Kind: "allow_once"}}
	if origin, address, ok := web.HTTPRequestDestination(argsJSON); ok {
		// A request to the root of an origin would offer two buttons that
		// grant the same thing under different names.
		if address != origin+"/" {
			options = append(options, acp.PermissionOption{OptionID: OptionAllowAlwaysURL, Name: "Always allow " + address, Kind: "allow_always"})
		}
		options = append(options, acp.PermissionOption{OptionID: OptionAllowAlwaysOrigin, Name: "Always allow " + origin, Kind: "allow_always"})
	}
	return append(options, acp.PermissionOption{OptionID: OptionReject, Name: "Reject", Kind: "reject_once"})
}

// recordHTTPGrants stores what an "always" answer approved: the destination it
// named and everything the approved request carried. A client that answers the
// generic allow_always (a prompt restored without its own options) gets the
// narrower address grant.
func recordHTTPGrants(st *session.State, argsJSON, cwd, optionID string) {
	req, err := web.ParseHTTPRequest(argsJSON, cwd)
	if err != nil {
		return
	}
	switch optionID {
	case OptionAllowAlwaysOrigin:
		st.AddHTTPGrantIfNew(httpGrantOrigin + req.Origin())
	case OptionAllowAlwaysURL, OptionAllowAlways:
		st.AddHTTPGrantIfNew(httpGrantURL + req.Address())
	default:
		return
	}
	for _, extra := range httpExtras(req) {
		st.AddHTTPGrantIfNew(extra.key)
	}
}

func hasGrant(grants []string, key string) bool {
	for _, g := range grants {
		if g == key {
			return true
		}
	}
	return false
}

func joinWords(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	default:
		return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
	}
}

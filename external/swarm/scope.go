//go:build swarm

package swarm

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// What a scoped client may reach, and where (docs/plans/remote-model-provider-phase3.md, 3.2; the verdicts of D1 and D5 are
// in docs/plans/remote-model-provider-models/p3-d1-allowlist-chain.md and p3-d5-scope-routes.md).

// splitHops reads a mounted request the way the node will route it. name is the node of this relay and rest the escaped path
// after it; the result is the chain of node names from this relay, the first being name, and the DECODED route that follows the
// last hop. A chain composes by writing hops into the path (`/swarm/nodes/<next>/...`), so every hop is a name the first relay has
// to judge: a child forwards with its own full client token, and only the first relay sees the borrower's class. ok is false for
// a remainder that cannot be decoded. mountRemainder has already refused encoded separators and relative segments, so decoding
// cannot change the segment structure.
func splitHops(name, rest string) (hops []string, route string, ok bool) {
	decoded, err := url.PathUnescape(rest)
	if err != nil {
		return nil, "", false
	}
	hops = []string{name}
	for strings.HasPrefix(decoded, swarmdto.MountPath) {
		after := strings.TrimPrefix(decoded, swarmdto.MountPath)
		slash := strings.IndexByte(after, '/')
		if slash < 0 {
			// The path ends at a node name: `/swarm/nodes/<last>` and no route.
			hops = append(hops, after)
			return hops, "", true
		}
		hops = append(hops, after[:slash])
		decoded = after[slash:]
	}
	return hops, decoded, true
}

// clientAdmitsHops is the allowlist of D1: an entry is an exact hop path. A bare `a` admits the single-hop path [a] only,
// `edge/gpu` the two-hop path through the chained relay `edge` only, and `*` any single-hop path below this relay. A longer
// chain that starts with an entry, and a node of the same name at another place in the topology, are not admitted.
func clientAdmitsHops(c config.SwarmClient, hops []string) bool {
	if len(hops) == 0 {
		return false
	}
	path := strings.Join(hops, "/")
	for _, entry := range c.Nodes {
		if entry == "*" {
			if len(hops) == 1 {
				return true
			}
			continue
		}
		if entry == path {
			return true
		}
	}
	return false
}

const (
	sharedModelsRoute       = "/coddy/llm/models"
	sharedModelsUsagePrefix = "/coddy/llm/models/"
	sharedModelsUsageSuffix = "/usage"
)

// sharedAliveRoute is the ping of the application probe (docs/plans/remote-model-provider-probe.md): the one entry the table gained after D5.
// The call id travels in a header, so the route is a fixed string like the others and no path carries a capability.
const sharedAliveRoute = "/coddy/llm/alive"

// isProbePing reports whether a request is a ping of the application probe. It is a shared-model route a scoped client may use, but not
// a call: it takes no slot, spends no token and is not counted.
func isProbePing(method, route string) bool {
	return method == http.MethodPost && route == sharedAliveRoute
}

// sharedRoute is the closed table of D5: the three shared-model routes and the ping of the probe, with their methods, matched exactly on the decoded
// route. Nothing else is in it: not a HEAD, not an OPTIONS, not a path with a trailing slash or a `;x=1`, not a route a later
// release adds under /coddy/llm/. A prefix would admit that route the day it ships; a new scoped route is a change of this table.
func sharedRoute(method, route string) bool {
	switch {
	case method == http.MethodGet && route == sharedModelsRoute:
		return true
	case method == http.MethodPost && route == sharedCompletionsRoute:
		return true
	case isProbePing(method, route):
		return true
	case method == http.MethodGet && strings.HasPrefix(route, sharedModelsUsagePrefix) && strings.HasSuffix(route, sharedModelsUsageSuffix):
		alias := strings.TrimSuffix(strings.TrimPrefix(route, sharedModelsUsagePrefix), sharedModelsUsageSuffix)
		return !strings.Contains(alias, "/") && config.ValidSharedAlias(alias)
	}
	return false
}

package config

import "encoding/json"

// RelayUISchemaJSON is RelayUISchemaMap as the settings form reads it.
func RelayUISchemaJSON() ([]byte, error) {
	return json.Marshal(RelayUISchemaMap())
}

// RelayUISchemaMap is the settings form of a swarm relay: its own deployment
// (the swarm block) and its log (issue #401). A relay holds no model, no
// session and no workspace, so the rest of the configuration has nothing to set
// there. The document says it is a relay's (x-coddy-relay), and the web UI
// leaves out what only an agent has - the Sessions tab.
//
// The relay's credentials are write-only, the way SwarmJSON serves them: a
// field marked writeOnly comes back empty, x-coddy-configured names the flag
// that says whether one is set, and an empty field keeps the stored value on
// save (preserveSwarmSecrets).
func RelayUISchemaMap() map[string]interface{} {
	props, _ := UISchemaMap()["properties"].(map[string]interface{})
	return map[string]interface{}{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"title":       "Coddy relay config",
		"description": "The settings of a swarm relay, edited from its own page. A save rebuilds the relay: nodes register and open their tunnels again within seconds.",
		"type":        "object",
		"properties": map[string]interface{}{
			"swarm":  swarmUISchema(),
			"logger": props["logger"],
		},
		"additionalProperties":   false,
		"x-coddy-property-order": []interface{}{"swarm", "logger"},
		"x-coddy-relay":          true,
	}
}

// secretProp is a write-only credential: served empty, kept when sent back
// empty, replaced when sent with a value. configured is the sibling field that
// reports whether one is set.
func secretProp(title, description, configured string) map[string]interface{} {
	out := strProp(title, description)
	out["writeOnly"] = true
	out["x-coddy-configured"] = configured
	return out
}

func stringListProp(title, description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "array",
		"title":       title,
		"description": description,
		"items":       map[string]interface{}{"type": "string"},
	}
}

// swarmDialUISchema is how a relay or a joining node dials the other end.
func swarmDialUISchema() map[string]interface{} {
	return objectSchema("Dialling", "How this end reaches the other one: an outbound proxy and the certificate check.",
		map[string]interface{}{
			"proxy":                secretProp("Proxy", "http, https, socks5 or socks5h proxy URL. It may carry a password, so it is write-only: leave it empty to keep the one set.", "proxy_configured"),
			"ca_file":              strProp("CA file", "PEM bundle trusted for the other end's certificate, besides the system roots."),
			"insecure_skip_verify": boolProp("Skip certificate check", "For a lab only: every connection is logged as insecure."),
		},
		[]string{"proxy", "ca_file", "insecure_skip_verify"},
		nil)
}

// swarmUISchema mirrors SwarmJSON for the relay's settings form. enable is not
// offered: a relay whose settings are open is the relay, and switching it off
// from its own page would leave nothing to reach.
func swarmUISchema() map[string]interface{} {
	upstreamProps := map[string]interface{}{
		"name": strProp("Name", "Name the node is mounted under: /swarm/nodes/<name>."),
		"url":  strProp("URL", "Address the relay dials the node at."),
		"kind": map[string]interface{}{
			"type":        "string",
			"title":       "Kind",
			"description": "agent, or relay for a relay chained under this one.",
			"enum":        []string{"agent", "relay"},
			"default":     "agent",
		},
		"token": secretProp("Token", "The node's own bearer token, which the relay presents when it proxies. Write-only: leave it empty to keep the one set.", "token_configured"),
		"dial":  swarmDialUISchema(),
	}
	joinProps := map[string]interface{}{
		"url":           strProp("Relay URL", "Parent relay this relay registers into."),
		"name":          strProp("Name", "Name this relay registers under; empty takes the host name."),
		"pairing_token": secretProp("Pairing token", "The parent's pairing token. Write-only: leave it empty to keep the one set.", "pairing_token_configured"),
		"advertise_url": strProp("Advertise URL", "Address the parent dials this relay at. Empty dials out: the parent drives this relay back down a tunnel."),
		"token":         secretProp("Token", "This relay's own client token, which the parent presents when it proxies. Write-only: leave it empty to keep the one set.", "token_configured"),
		"dial":          swarmDialUISchema(),
	}
	return objectSchema("Swarm relay", "This relay's deployment: its name and address, the tokens of its clients and nodes, CORS for pages served elsewhere, the nodes it dials itself and the relays it joins. A save rebuilds the relay; a new address takes a restart.",
		map[string]interface{}{
			"name": strProp("Name", "The relay's name on the map and in /swarm/info; empty takes the host name it runs on."),
			"host": strProp("Listen host", "Address to bind. 0.0.0.0 listens on every interface. Takes effect on a restart."),
			"port": intProp("Listen port", "Port to bind. Takes effect on a restart."),
			"auth_token": secretProp("Client token", "Bearer token a client presents to use this relay, and every node behind it. Write-only: leave it empty to keep the one set. A new one signs out every client, this page included: enter it again with Connect to… in the environment menu.",
				"auth_configured"),
			"pairing_tokens": func() map[string]interface{} {
				out := stringListProp("Pairing tokens", "Tokens a node presents to join this relay. Write-only: the list is served empty, and a list sent back empty keeps the tokens set; tokens entered here replace them all.")
				out["writeOnly"] = true
				out["x-coddy-configured"] = "pairing_configured"
				return out
			}(),
			"cors": objectSchema("CORS", "Pages served from another origin, such as a laptop's coddy serve, that may call this relay from the browser.",
				map[string]interface{}{
					"enable":          boolProp("Enable CORS", "Answer cross-origin requests from the origins below."),
					"allow_loopback":  boolProp("Allow loopback origins", "Also admit any page from the browser's own machine - localhost, *.localhost, 127.0.0.0/8 or [::1] on any port - such as a laptop's coddy serve, whatever port it took. The client token still applies."),
					"allowed_origins": stringListProp("Allowed origins", "Exact origins, for example http://localhost:12345, or * for any."),
				},
				[]string{"enable", "allow_loopback", "allowed_origins"},
				nil),
			"tls": objectSchema("TLS", "Certificate and key the relay serves HTTPS with. Both or neither.",
				map[string]interface{}{
					"cert_file": strProp("Certificate file", "PEM certificate chain."),
					"key_file":  strProp("Key file", "PEM private key."),
				},
				[]string{"cert_file", "key_file"},
				nil),
			"lease_ttl_seconds":          intProp("Lease TTL (seconds)", "How long a registration lasts without a refresh; nodes refresh at a third of it."),
			"fanout_timeout_seconds":     intProp("Fan-out timeout (seconds)", "How long the aggregated session list and the topology wait for a node."),
			"allow_private_upstreams":    stringListProp("Private upstream hosts", "Host names allowed to resolve into private ranges when a node advertises a URL."),
			"allow_insecure":             boolProp("Allow without a client token", "Let a relay bound off loopback run without a client token. For a lab only."),
			"insecure_open_registration": boolProp("Open registration", "Let any node join without a pairing token. For a lab only."),
			"upstreams": map[string]interface{}{
				"type":        "array",
				"title":       "Upstreams",
				"description": "Nodes this relay dials itself, pinned on the map whether or not they check in.",
				"items":       objectSchema("", "", upstreamProps, []string{"name", "url", "kind", "token", "dial"}, nil),
			},
			"join": map[string]interface{}{
				"type":        "array",
				"title":       "Joins",
				"description": "Parent relays this relay registers into, which is how relays chain.",
				"items":       objectSchema("", "", joinProps, []string{"url", "name", "pairing_token", "advertise_url", "token", "dial"}, nil),
			},
		},
		[]string{
			"name", "host", "port", "auth_token", "pairing_tokens", "cors", "tls",
			"lease_ttl_seconds", "fanout_timeout_seconds", "upstreams", "join",
			"allow_private_upstreams", "allow_insecure", "insecure_open_registration",
		},
		nil)
}

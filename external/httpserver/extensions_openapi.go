//go:build http

package httpserver

func mergeExtensionsOpenAPI(doc map[string]interface{}) {
	paths := doc["paths"].(map[string]interface{})
	str := map[string]any{"type": "string"}
	boolean := map[string]any{"type": "boolean"}
	object := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}
	array := func(items any) map[string]any {
		return map[string]any{"type": "array", "items": items}
	}
	entry := object(map[string]any{"api": map[string]any{"type": "integer", "enum": []int{1}}, "id": str, "name": str, "version": str, "description": str, "permissions": array(str), "sha256": str, "entry_sha256": str, "enabled": boolean, "ui": object(map[string]any{"entry": str, "styles": array(str), "compat_styles": array(str)})})
	response := func(desc string, schema any) map[string]any {
		return map[string]any{"description": desc, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
	}
	errResponse := map[string]any{"description": "JSON error"}
	params := []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": str}}
	paths["/coddy/extensions"] = map[string]any{
		"get":  map[string]any{"operationId": "listUIExtensions", "summary": "List operator-installed UI extensions", "responses": map[string]any{"200": response("Installed packages", object(map[string]any{"items": map[string]any{"type": "array", "items": entry}})), "500": errResponse}},
		"post": map[string]any{"operationId": "installUIExtension", "summary": "Install a ZIP as a disabled UI extension", "description": "Raw ZIP up to 8 MiB, at most 128 entries and 16 MiB expanded. Root manifest.json with api 1, id, name, version and ui.entry naming a single bundled ES module. Optional permissions name scopes of a closed vocabulary and ui.compat_styles names CSS files of the archive applied on legacy engines. Existing ids are refused; installation runs no JavaScript.", "requestBody": map[string]any{"required": true, "content": map[string]any{"application/zip": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}}, "responses": map[string]any{"201": response("Installed and disabled", entry), "400": errResponse, "409": errResponse, "413": errResponse, "500": errResponse}},
	}
	paths["/coddy/extensions/{id}"] = map[string]any{
		"put":    map[string]any{"operationId": "setUIExtensionEnabled", "summary": "Enable or disable the exact installed digest", "parameters": params, "description": "Enabling requires accept_risk:true. UI code runs in the application origin. Reload every affected browser to revoke previously executed code.", "requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": []string{"sha256", "enabled"}, "additionalProperties": false, "properties": map[string]any{"sha256": str, "enabled": boolean, "accept_risk": boolean}}}}}, "responses": map[string]any{"200": response("Current state", entry), "400": errResponse, "404": errResponse, "409": errResponse, "500": errResponse}},
		"delete": map[string]any{"operationId": "removeUIExtension", "summary": "Remove an installed UI extension", "parameters": params, "responses": map[string]any{"204": map[string]any{"description": "Removed; reload browsers to revoke executed code"}, "400": errResponse, "404": errResponse, "500": errResponse}},
	}
	paths["/coddy/extensions/{id}/assets/{file}"] = map[string]any{"get": map[string]any{"operationId": "readUIExtensionAsset", "summary": "Fetch an asset of an enabled, digest-matched extension", "parameters": append(append([]any{}, params...), map[string]any{"name": "file", "in": "path", "required": true, "schema": str}, map[string]any{"name": "sha256", "in": "query", "required": true, "schema": str}), "responses": map[string]any{"200": map[string]any{"description": "Asset bytes; attachment, no-store, sandbox CSP", "content": map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}}, "400": errResponse, "404": errResponse, "409": errResponse, "500": errResponse}}}
	request := object(map[string]any{
		"sha256": str, "url": map[string]any{"type": "string", "format": "uri", "maxLength": 8192},
		"method":  map[string]any{"type": "string", "default": "GET", "enum": []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}},
		"headers": map[string]any{"type": "object", "additionalProperties": str},
		"body":    map[string]any{"type": "string", "format": "byte", "description": "Base64 bytes, at most 4 MiB decoded"},
	})
	request["required"] = []string{"sha256", "url"}
	request["additionalProperties"] = false
	result := object(map[string]any{
		"status": map[string]any{"type": "integer", "minimum": 200, "maximum": 599}, "status_text": str,
		"headers": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "array", "items": str}},
		"body":    map[string]any{"type": "string", "format": "byte", "nullable": true, "description": "Base64 bytes, at most 8 MiB decoded; null for an empty body"},
	})
	paths["/coddy/extensions/{id}/fetch"] = map[string]any{"post": map[string]any{
		"operationId": "fetchForUIExtension", "summary": "Send an HTTP(S) request from this Coddy server for an approved UI extension", "parameters": params,
		"description": "Requires the exact enabled package digest and normal API authentication. Allows loopback and private targets. JSON envelope up to 6 MiB, decoded body up to 4 MiB, request headers up to 32 KiB; response up to 8 MiB and 64 KiB headers, timeout 30 seconds. No redirects, browser cookies, ambient API credentials or environment/provider proxies. Explicit target Authorization is allowed; cookie, routing, hop-by-hop, Origin/Referer and Sec-* headers are refused. Returns target statuses, headers and bytes inside JSON, never as an active same-origin document. Disable/removal on this server cancels admitted requests; effects already sent cannot be undone.",
		"requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": request}}},
		"responses":   map[string]any{"200": response("Target response, including target HTTP errors and unfollowed redirects", result), "400": errResponse, "401": errResponse, "403": errResponse, "409": errResponse, "413": errResponse, "415": errResponse, "502": errResponse, "504": errResponse},
	}}
}

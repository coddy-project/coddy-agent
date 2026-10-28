//go:build http

package httpserver

func mergeExtensionsOpenAPI(doc map[string]interface{}) {
	paths := doc["paths"].(map[string]interface{})
	str := map[string]any{"type": "string"}
	boolean := map[string]any{"type": "boolean"}
	object := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}
	entry := object(map[string]any{"api": map[string]any{"type": "integer", "enum": []int{1}}, "id": str, "name": str, "version": str, "description": str, "sha256": str, "entry_sha256": str, "enabled": boolean, "ui": object(map[string]any{"entry": str, "styles": map[string]any{"type": "array", "items": str}})})
	response := func(desc string, schema any) map[string]any {
		return map[string]any{"description": desc, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
	}
	errResponse := map[string]any{"description": "JSON error"}
	params := []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": str}}
	paths["/coddy/extensions"] = map[string]any{
		"get":  map[string]any{"operationId": "listUIExtensions", "summary": "List operator-installed UI extensions", "responses": map[string]any{"200": response("Installed packages", object(map[string]any{"items": map[string]any{"type": "array", "items": entry}})), "500": errResponse}},
		"post": map[string]any{"operationId": "installUIExtension", "summary": "Install a ZIP as a disabled UI extension", "description": "Raw ZIP up to 8 MiB, at most 128 entries and 16 MiB expanded. Root manifest.json with api 1, id, name, version and ui.entry naming a single bundled ES module. Existing ids are refused; installation runs no JavaScript.", "requestBody": map[string]any{"required": true, "content": map[string]any{"application/zip": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}}, "responses": map[string]any{"201": response("Installed and disabled", entry), "400": errResponse, "409": errResponse, "413": errResponse, "500": errResponse}},
	}
	paths["/coddy/extensions/{id}"] = map[string]any{
		"put":    map[string]any{"operationId": "setUIExtensionEnabled", "summary": "Enable or disable the exact installed digest", "parameters": params, "description": "Enabling requires accept_risk:true. UI code runs in the application origin. Reload every affected browser to revoke previously executed code.", "requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": []string{"sha256", "enabled"}, "additionalProperties": false, "properties": map[string]any{"sha256": str, "enabled": boolean, "accept_risk": boolean}}}}}, "responses": map[string]any{"200": response("Current state", entry), "400": errResponse, "404": errResponse, "409": errResponse, "500": errResponse}},
		"delete": map[string]any{"operationId": "removeUIExtension", "summary": "Remove an installed UI extension", "parameters": params, "responses": map[string]any{"204": map[string]any{"description": "Removed; reload browsers to revoke executed code"}, "400": errResponse, "404": errResponse, "500": errResponse}},
	}
	paths["/coddy/extensions/{id}/assets/{file}"] = map[string]any{"get": map[string]any{"operationId": "readUIExtensionAsset", "summary": "Fetch an asset of an enabled, digest-matched extension", "parameters": append(append([]any{}, params...), map[string]any{"name": "file", "in": "path", "required": true, "schema": str}, map[string]any{"name": "sha256", "in": "query", "required": true, "schema": str}), "responses": map[string]any{"200": map[string]any{"description": "Asset bytes; attachment, no-store, sandbox CSP", "content": map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}}, "400": errResponse, "404": errResponse, "409": errResponse, "500": errResponse}}}
}

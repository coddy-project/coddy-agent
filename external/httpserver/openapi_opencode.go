//go:build http

package httpserver

import "github.com/EvilFreelancer/coddy-agent/internal/llm"

func mergeOpenCodeAuthOpenAPI(doc map[string]interface{}) {
	paths := doc["paths"].(map[string]interface{})
	schemas := doc["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	schemas["OpenCodeAuthStatus"] = map[string]interface{}{
		"type": "object", "required": []string{"connected", "account", "source"},
		"properties": map[string]interface{}{
			"connected":    map[string]string{"type": "boolean"},
			"account":      map[string]string{"type": "boolean"},
			"organization": map[string]string{"type": "string"},
			"masked":       map[string]string{"type": "string"},
			"source":       map[string]interface{}{"type": "string", "enum": []string{"none", "oauth", "stored_key", "api_key", "api_key_command", "env"}},
		},
	}
	schemas["OpenCodeAuthDeviceStartRequest"] = map[string]interface{}{
		"type": "object", "properties": map[string]interface{}{
			"api_base": map[string]interface{}{"type": "string", "enum": llm.OpenCodeAPIBases(), "description": "Current unsaved Go or Console gateway. Omitted: the saved row, then Go."},
			"proxy":    map[string]string{"type": "string", "description": "Current unsaved provider proxy. Absent retains the row route, even while switching its type; empty or inherit follows the process proxy, none connects directly, a URL selects that proxy."},
		},
	}
	base := "/coddy/providers/{name}/opencode-auth"
	paths[base] = map[string]interface{}{
		"get": map[string]interface{}{
			"summary": "Get OpenCode sign-in status", "operationId": "getProviderOpenCodeAuth",
			"description": "Accepts valid saved or unsaved provider names, including rows switched to OpenCode before Save. Reports organization, a masked service account key, and the active credential source. Never returns account tokens or keys.",
			"parameters":  []interface{}{codexProviderNameParameter()},
			"responses":   map[string]interface{}{"200": jsonSchemaResponse("Non-secret OpenCode status.", "#/components/schemas/OpenCodeAuthStatus"), "400": errorResponseRef(), "500": errorResponseRef()},
		},
		"delete": map[string]interface{}{
			"summary": "Sign out of OpenCode", "operationId": "deleteProviderOpenCodeAuth",
			"description": "Cancels pending sign-ins and removes the local credential. Revoke the account session or service account key in Console to end server-side access.",
			"parameters":  []interface{}{codexProviderNameParameter()},
			"responses":   map[string]interface{}{"200": jsonSchemaResponse("Status after local removal.", "#/components/schemas/OpenCodeAuthStatus"), "400": errorResponseRef(), "500": errorResponseRef()},
		},
	}
	paths[base+"/device"] = map[string]interface{}{"post": map[string]interface{}{
		"summary": "Start OpenCode account device authorization", "operationId": "startProviderOpenCodeDeviceAuth",
		"description": "Requires application/json. Uses the form's unsaved gateway and proxy. Returns a verification URL and user code while the server polls Console, discovers the selected organization and stores tokens privately. A new start or logout supersedes earlier attempts; shutdown cancels waits. An overridden route applies to this login only; Save persists the provider settings for later chats and refreshes.",
		"parameters":  []interface{}{codexProviderNameParameter()},
		"requestBody": map[string]interface{}{"required": false, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]string{"$ref": "#/components/schemas/OpenCodeAuthDeviceStartRequest"}}}},
		"responses":   map[string]interface{}{"200": jsonSchemaResponse("Verification page and code.", "#/components/schemas/CodexAuthDeviceStart"), "400": errorResponseRef(), "409": errorResponseRef(), "415": errorResponseRef(), "500": errorResponseRef(), "502": errorResponseRef()},
	}}
	paths[base+"/device/{loginID}"] = map[string]interface{}{"get": map[string]interface{}{
		"summary": "Poll OpenCode account authorization", "operationId": "getProviderOpenCodeDeviceAuth",
		"description": "Returns pending, completed or failed, connected and an optional error. Tokens never leave the server; another provider cannot read this attempt.",
		"parameters":  []interface{}{codexProviderNameParameter(), map[string]interface{}{"name": "loginID", "in": "path", "required": true, "schema": map[string]string{"type": "string"}}},
		"responses":   map[string]interface{}{"200": jsonSchemaResponse("Current authorization state.", "#/components/schemas/CodexAuthDeviceStatus"), "400": errorResponseRef(), "404": errorResponseRef(), "500": errorResponseRef()},
	}}
}

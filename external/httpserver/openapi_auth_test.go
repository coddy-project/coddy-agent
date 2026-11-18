//go:build http

package httpserver

import "testing"

// openAPIAuthOperation digs out one operation of the served spec, or fails.
func openAPIAuthOperation(t *testing.T, route, method string) map[string]interface{} {
	t.Helper()
	paths := openAPISpec()["paths"].(map[string]interface{})
	spec, ok := paths[route].(map[string]interface{})
	if !ok {
		t.Fatalf("openapi has no %s", route)
	}
	op, ok := spec[method].(map[string]interface{})
	if !ok {
		t.Fatalf("openapi %s has no %s operation", route, method)
	}
	return op
}

// openAPIRequestProps returns the JSON request-body properties of an operation,
// nil when it declares none.
func openAPIRequestProps(t *testing.T, op map[string]interface{}, route, method string) map[string]interface{} {
	t.Helper()
	body, ok := op["requestBody"].(map[string]interface{})
	if !ok {
		return nil
	}
	content, ok := body["content"].(map[string]interface{})
	if !ok {
		t.Fatalf("%s %s requestBody has no content", method, route)
	}
	schema, ok := content["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
	if !ok {
		t.Fatalf("%s %s has no application/json request schema", method, route)
	}
	props, _ := schema["properties"].(map[string]interface{})
	return props
}

// openAPIResponseProps returns the JSON 200 response properties of an
// operation. When the answer is a oneOf it returns one properties map per
// branch.
func openAPIResponseProps(t *testing.T, op map[string]interface{}, route, method string) []map[string]interface{} {
	t.Helper()
	responses, ok := op["responses"].(map[string]interface{})
	if !ok {
		t.Fatalf("%s %s has no responses", method, route)
	}
	content, ok := responses["200"].(map[string]interface{})["content"].(map[string]interface{})
	if !ok {
		t.Fatalf("%s %s 200 has no content", method, route)
	}
	schema, ok := content["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
	if !ok {
		t.Fatalf("%s %s 200 has no application/json schema", method, route)
	}
	if branches, ok := schema["oneOf"].([]interface{}); ok {
		out := make([]map[string]interface{}, 0, len(branches))
		for _, b := range branches {
			props, _ := b.(map[string]interface{})["properties"].(map[string]interface{})
			out = append(out, props)
		}
		return out
	}
	props, _ := schema["properties"].(map[string]interface{})
	return []map[string]interface{}{props}
}

func openAPIHasProp(props map[string]interface{}, name string) bool {
	_, ok := props[name]
	return ok
}

// The two-step sign-in and the Settings-managed enrollment are part of the
// served contract: the spec must name every auth route with the request and
// response shapes the handlers produce.
func TestOpenAPIAuthShape(t *testing.T) {
	// The password step takes no one-time code: a TOTP account answers with a
	// challenge for POST /coddy/auth/login/verify instead of a cookie.
	login := openAPIAuthOperation(t, "/coddy/auth/login", "post")
	loginReq := openAPIRequestProps(t, login, "/coddy/auth/login", "post")
	for _, name := range []string{"user", "password"} {
		if !openAPIHasProp(loginReq, name) {
			t.Errorf("login request schema misses %q", name)
		}
	}
	if openAPIHasProp(loginReq, "code") {
		t.Error("login request schema still carries `code`; the second factor moved to /coddy/auth/login/verify")
	}
	loginBranches := openAPIResponseProps(t, login, "/coddy/auth/login", "post")
	var sessionAnswer, challengeAnswer bool
	for _, props := range loginBranches {
		if openAPIHasProp(props, "ok") && openAPIHasProp(props, "user") {
			sessionAnswer = true
		}
		if openAPIHasProp(props, "second_factor") && openAPIHasProp(props, "challenge") {
			challengeAnswer = true
		}
	}
	if !sessionAnswer {
		t.Error("login 200 schema has no session answer ({ok, user})")
	}
	if !challengeAnswer {
		t.Error("login 200 schema has no two-step answer ({second_factor, challenge})")
	}

	// The verification step binds the challenge to the account and accepts a
	// TOTP or a backup code.
	verify := openAPIAuthOperation(t, "/coddy/auth/login/verify", "post")
	verifyReq := openAPIRequestProps(t, verify, "/coddy/auth/login/verify", "post")
	for _, name := range []string{"challenge", "code"} {
		if !openAPIHasProp(verifyReq, name) {
			t.Errorf("verify request schema misses %q", name)
		}
	}
	for _, props := range openAPIResponseProps(t, verify, "/coddy/auth/login/verify", "post") {
		for _, name := range []string{"ok", "user"} {
			if !openAPIHasProp(props, name) {
				t.Errorf("verify 200 schema misses %q", name)
			}
		}
	}

	// The enrollment routes are the Settings, Authentication surface: cookie
	// sign-in and same-origin only, never a bearer token.
	enrollment := map[string][]string{
		"/coddy/auth/totp/setup":              {"secret", "uri", "qr_png"},
		"/coddy/auth/totp/enable":             {"codes", "backup_codes_generation"},
		"/coddy/auth/totp/disable":            {"ok"},
		"/coddy/auth/backup-codes/regenerate": {"codes", "backup_codes_generation"},
	}
	for route, want := range enrollment {
		op := openAPIAuthOperation(t, route, "post")
		sec, ok := op["security"].([]interface{})
		if !ok || len(sec) != 1 {
			t.Errorf("%s security = %#v, want one cookie requirement", route, op["security"])
		} else if _, ok := sec[0].(map[string]interface{})["cookieAuth"]; !ok {
			t.Errorf("%s security = %#v, want cookieAuth only", route, sec[0])
		}
		for _, props := range openAPIResponseProps(t, op, route, "post") {
			for _, name := range want {
				if !openAPIHasProp(props, name) {
					t.Errorf("%s 200 schema misses %q", route, name)
				}
			}
		}
	}
	enableReq := openAPIRequestProps(t,
		openAPIAuthOperation(t, "/coddy/auth/totp/enable", "post"), "/coddy/auth/totp/enable", "post")
	for _, name := range []string{"secret", "code"} {
		if !openAPIHasProp(enableReq, name) {
			t.Errorf("totp/enable request schema misses %q", name)
		}
	}
}

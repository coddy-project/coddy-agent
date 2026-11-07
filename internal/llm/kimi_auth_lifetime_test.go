package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestKimiDeviceLoginPreservesTokenLifetime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/oauth/device_authorization" {
			_, _ = fmt.Fprint(w, `{"device_code":"device","user_code":"code","verification_uri_complete":"https://www.kimi.ai/code/authorize_device?user_code=code","expires_in":600,"interval":1}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"access_token":"access","refresh_token":"refresh","expires_in":7200}`)
	}))
	defer server.Close()
	login, err := startKimiDeviceLogin(context.Background(), "global", server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	token, err := login.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	field := reflect.ValueOf(token).FieldByName("ExpiresIn")
	if !field.IsValid() || field.Int() != 7200 {
		t.Fatalf("device token lifetime was not preserved")
	}
}

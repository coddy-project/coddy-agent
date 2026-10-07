package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func listingHandler(listing any, status int, contentType string) fakeRemoteHandler {
	return func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		switch v := listing.(type) {
		case string:
			_, _ = w.Write([]byte(v))
		default:
			_ = json.NewEncoder(w).Encode(v)
		}
	}
}

func TestListModelsOfACoddyRemote(t *testing.T) {
	remote := newFakeRemote(t, listingHandler(WireListing{Protocol: CoddyProtocol, Data: []WireModelRow{
		{ID: "writer", Revision: "r2", MaxContextTokens: 64000},
		{ID: "coder", Revision: "r1", MaxContextTokens: 200000, Multimodal: true, ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "low", AllowReasoningOff: true},
		{ID: "coder", Revision: "dup"},
		{ID: "  "},
	}}, 200, "application/json"))
	in := coddyInput(remote)
	got, err := ListModels(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	want := []ModelEntry{
		{ID: "coder", Revision: "r1", ContextWindow: 200000, Multimodal: true, ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "low", AllowReasoningOff: true},
		{ID: "writer", Revision: "r2", ContextWindow: 64000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries\n got: %+v\nwant: %+v", got, want)
	}
	rec := remote.request(1)
	if rec.method != http.MethodGet || rec.path != "/coddy/llm/models" {
		t.Fatalf("%s %s", rec.method, rec.path)
	}
	if rec.header.Get("Authorization") != "Bearer shared-token" {
		t.Fatalf("Authorization = %q", rec.header.Get("Authorization"))
	}
}

func TestListModelsOfACoddyRemoteBehindAMountAndWithoutAKey(t *testing.T) {
	remote := newFakeRemote(t, listingHandler(WireListing{Protocol: CoddyProtocol}, 200, "application/json"))
	in := coddyInput(remote)
	in.APIKey = ""
	in.BaseURL = remote.url() + "/swarm/nodes/n1/"
	got, err := ListModels(context.Background(), in)
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	rec := remote.request(1)
	if rec.path != "/swarm/nodes/n1/coddy/llm/models" || rec.header.Get("Authorization") != "" {
		t.Fatalf("path %q auth %q", rec.path, rec.header.Get("Authorization"))
	}
}

func TestListModelsCoddyChecksTheProtocolStrictly(t *testing.T) {
	remote := newFakeRemote(t, listingHandler(`{"protocol":2,"data":[{"id":"coder"}]}`, 200, "application/json"))
	_, err := ListModels(context.Background(), coddyInput(remote))
	if err == nil || CoddyErrorKind(err) != WireKindInvalid || CoddyErrorCode(err) != WireCodeProtocolMismatch {
		t.Fatalf("err %v", err)
	}
	for _, v := range []string{"2", "1"} {
		if !strings.Contains(err.Error(), v) {
			t.Errorf("the error names both versions, missing %s: %v", v, err)
		}
	}
}

func TestListModelsCoddyAnswersAClearErrorWhenTheRemoteHasNoSuchRoutes(t *testing.T) {
	cases := map[string]fakeRemoteHandler{
		"an SPA shell with 200":      listingHandler("<!doctype html><html><body>Coddy</body></html>", 200, "text/html"),
		"json that is not a listing": listingHandler(`{"data":[{"id":"gpt-4o"}]}`, 200, "application/json"),
		"an openai models list":      listingHandler(`{"object":"list","data":[{"id":"gpt-4o"}]}`, 200, "application/json"),
		"plain 404":                  listingHandler("404 page not found\n", 404, "text/plain"),
		"empty 200":                  listingHandler("", 200, ""),
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			remote := newFakeRemote(t, h)
			_, err := ListModels(context.Background(), coddyInput(remote))
			if CoddyErrorKind(err) != CoddyKindUnsupported || !strings.Contains(err.Error(), "does not offer shared models") {
				t.Fatalf("err %v", err)
			}
		})
	}
}

func TestListModelsCoddyAuthAndOtherFailures(t *testing.T) {
	t.Run("plain 401", func(t *testing.T) {
		remote := newFakeRemote(t, listingHandler("Unauthorized\n", 401, "text/plain"))
		_, err := ListModels(context.Background(), coddyInput(remote))
		if CoddyErrorKind(err) != WireKindAuth {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("coddy 403", func(t *testing.T) {
		remote := newFakeRemote(t, listingHandler(WireError{Status: 403, Kind: WireKindAuth, Message: "shared models need authentication"}, 403, "application/json"))
		_, err := ListModels(context.Background(), coddyInput(remote))
		if CoddyErrorKind(err) != WireKindAuth || !strings.Contains(err.Error(), "shared models need authentication") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("relay hop error", func(t *testing.T) {
		remote := newFakeRemote(t, listingHandler(`{"error":{"message":"swarm node \"n1\": node is registered but not reachable","node":"n1","reason":"x"}}`, 502, "application/json"))
		_, err := ListModels(context.Background(), coddyInput(remote))
		if err == nil || !strings.Contains(err.Error(), "n1") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		remote := newFakeRemote(t, listingHandler("", 200, ""))
		in := coddyInput(remote)
		remote.srv.Close()
		_, err := ListModels(context.Background(), in)
		if err == nil || CoddyErrorKind(err) != "" {
			t.Fatalf("an unreachable remote is a transport error, got kind %q: %v", CoddyErrorKind(err), err)
		}
	})
	t.Run("no api_base", func(t *testing.T) {
		_, err := ListModels(context.Background(), ProviderInput{Type: "coddy"})
		if err == nil {
			t.Fatal("no error")
		}
		var unsupported *UnsupportedProviderError
		if errors.As(err, &unsupported) {
			t.Fatal("coddy is a supported type")
		}
	})
}

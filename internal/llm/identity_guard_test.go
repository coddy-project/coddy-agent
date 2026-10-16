package llm

import (
	"net/http"
	"reflect"
	"testing"
)

func TestProvidersUseSystemTrustWithoutClientIdentity(t *testing.T) {
	if _, exists := reflect.TypeFor[ProviderInput]().FieldByName("ClientTLS"); exists {
		t.Fatal("provider-specific client TLS identity returned")
	}
	for _, typ := range []string{"coddy", "openai", "codex", "neuraldeep"} {
		t.Run(typ, func(t *testing.T) {
			rt, err := providerTransportFor(typ, "none")
			if err != nil {
				t.Fatal(err)
			}
			transport, ok := rt.(*http.Transport)
			if !ok {
				t.Fatalf("unexpected transport %T", rt)
			}
			if cfg := transport.TLSClientConfig; cfg != nil {
				if cfg.RootCAs != nil || len(cfg.Certificates) != 0 || cfg.GetClientCertificate != nil || cfg.InsecureSkipVerify {
					t.Fatal("provider transport overrides system trust or supplies a client identity")
				}
			}
		})
	}
}

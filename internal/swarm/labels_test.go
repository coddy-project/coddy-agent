package swarm

import (
	"context"
	"net/http"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The reserved label of a node whose token opens only the shared-model routes.
// internal/config cannot import this package, so it keeps its own copy of the
// key for the config check, and this holds the two equal.
func TestTheLabelKeyMatchesTheConfigCheck(t *testing.T) {
	if LabelTokenClass != config.LabelTokenClass {
		t.Fatalf("LabelTokenClass %q != config.LabelTokenClass %q", LabelTokenClass, config.LabelTokenClass)
	}
	if TokenClassSharedModels != config.TokenClassSharedModels {
		t.Fatalf("TokenClassSharedModels %q != config.TokenClassSharedModels %q", TokenClassSharedModels, config.TokenClassSharedModels)
	}
}

func cfgWithSharedToken(tokens ...string) *config.Config {
	cfg := &config.Config{}
	cfg.HTTPServer.SharedModels.Tokens = tokens
	return cfg
}

func TestDerivedLabels(t *testing.T) {
	cases := []struct {
		name   string
		cfg    *config.Config
		join   config.SwarmJoin
		kind   string
		labels map[string]string
		want   map[string]string
	}{
		{
			name: "a shared-model token labels an agent",
			cfg:  cfgWithSharedToken("share-1"),
			join: config.SwarmJoin{Token: "share-1"},
			kind: KindAgent,
			want: map[string]string{LabelTokenClass: TokenClassSharedModels},
		},
		{
			name: "the operator's own labels are kept beside it",
			cfg:  cfgWithSharedToken("share-1"),
			join: config.SwarmJoin{Token: "share-1", Labels: map[string]string{"rack": "7"}},
			kind: KindAgent,
			want: map[string]string{"rack": "7", LabelTokenClass: TokenClassSharedModels},
		},
		{
			name: "a main token is no label",
			cfg:  cfgWithSharedToken("share-1"),
			join: config.SwarmJoin{Token: "main-1", Labels: map[string]string{"rack": "7"}},
			kind: KindAgent,
			want: map[string]string{"rack": "7"},
		},
		{
			name: "no token is no label",
			cfg:  cfgWithSharedToken("share-1"),
			join: config.SwarmJoin{},
			kind: KindAgent,
			want: nil,
		},
		{
			name: "a hand-written label is dropped when the token is not a shared-model one",
			cfg:  cfgWithSharedToken("share-1"),
			join: config.SwarmJoin{Token: "main-1", Labels: map[string]string{LabelTokenClass: TokenClassSharedModels, "rack": "7"}},
			kind: KindAgent,
			want: map[string]string{"rack": "7"},
		},
		{
			name: "a hand-written label with another value is replaced by the derived one",
			cfg:  cfgWithSharedToken("share-1"),
			join: config.SwarmJoin{Token: "share-1", Labels: map[string]string{LabelTokenClass: "something-else"}},
			kind: KindAgent,
			want: map[string]string{LabelTokenClass: TokenClassSharedModels},
		},
		{
			name: "a relay never carries it, even with a shared-model token",
			cfg:  cfgWithSharedToken("share-1"),
			join: config.SwarmJoin{Token: "share-1", Labels: map[string]string{LabelTokenClass: TokenClassSharedModels}},
			kind: KindRelay,
			want: nil,
		},
		{
			name: "surrounding spaces of the token do not matter",
			cfg:  cfgWithSharedToken(" share-1 "),
			join: config.SwarmJoin{Token: "  share-1"},
			kind: KindAgent,
			want: map[string]string{LabelTokenClass: TokenClassSharedModels},
		},
		{
			name: "a blank shared-model token matches nothing",
			cfg:  cfgWithSharedToken(""),
			join: config.SwarmJoin{},
			kind: KindAgent,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var before map[string]string
			if tc.join.Labels != nil {
				before = map[string]string{}
				for k, v := range tc.join.Labels {
					before[k] = v
				}
			}
			got := DerivedLabels(tc.cfg, tc.join, tc.kind)
			if len(got) != len(tc.want) {
				t.Fatalf("labels = %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("labels = %v, want %v", got, tc.want)
				}
			}
			// The configuration is never mutated: the join's own map stays as written.
			for k, v := range before {
				if tc.join.Labels[k] != v {
					t.Fatalf("the join's labels were changed: %v, was %v", tc.join.Labels, before)
				}
			}
			if len(tc.join.Labels) != len(before) {
				t.Fatalf("the join's labels were changed: %v, was %v", tc.join.Labels, before)
			}
		})
	}
}

func TestIsSharedModelsNode(t *testing.T) {
	if !SharedModelsOnly(map[string]string{LabelTokenClass: TokenClassSharedModels}, KindAgent) {
		t.Fatal("an agent with the label is a shared-models node")
	}
	for name, tc := range map[string]struct {
		labels map[string]string
		kind   string
	}{
		"no labels":         {nil, KindAgent},
		"another value":     {map[string]string{LabelTokenClass: "other"}, KindAgent},
		"a case variant":    {map[string]string{LabelTokenClass: "Shared_Models"}, KindAgent},
		"a trailing space":  {map[string]string{LabelTokenClass: "shared_models "}, KindAgent},
		"a relay":           {map[string]string{LabelTokenClass: TokenClassSharedModels}, KindRelay},
		"another label key": {map[string]string{"coddy.token_class2": TokenClassSharedModels}, KindAgent},
	} {
		if SharedModelsOnly(tc.labels, tc.kind) {
			t.Errorf("%s: must not be a shared-models node", name)
		}
	}
}

func TestStartJoinsRegistersTheDerivedLabel(t *testing.T) {
	relay := newFakeRelay(t, "pair")
	cfg := cfgWithSharedToken("share-1")
	cfg.Swarm.Join = []config.SwarmJoin{
		{URL: relay.ts.URL, Name: "vault", PairingToken: "pair", AdvertiseURL: "http://v:1", Token: "share-1",
			Labels: map[string]string{"rack": "7"}},
		{URL: relay.ts.URL, Name: "plain", PairingToken: "pair", AdvertiseURL: "http://p:1", Token: "main-1",
			Labels: map[string]string{LabelTokenClass: TokenClassSharedModels}},
	}
	set := startJoinsForTest(t, cfg)
	defer set.Stop()
	for _, c := range set.Clients() {
		_ = c.Register(testCtx(t))
	}
	var vault, plain RegisterRequest
	for _, r := range relay.seen() {
		switch r.Name {
		case "vault":
			vault = r
		case "plain":
			plain = r
		}
	}
	if vault.Labels[LabelTokenClass] != TokenClassSharedModels || vault.Labels["rack"] != "7" {
		t.Errorf("vault registered with labels %v", vault.Labels)
	}
	if _, has := plain.Labels[LabelTokenClass]; has {
		t.Errorf("plain carries the label with a main token: %v", plain.Labels)
	}
	if cfg.Swarm.Join[1].Labels[LabelTokenClass] != TokenClassSharedModels {
		t.Error("the configuration was mutated")
	}
}

func startJoinsForTest(t *testing.T, cfg *config.Config) *JoinSet {
	t.Helper()
	set, err := StartJoins(testCtx(t), cfg, StartJoinsOptions{Kind: KindAgent, Handler: http.NotFoundHandler(), Log: quietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

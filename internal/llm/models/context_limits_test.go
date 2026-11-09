package models

import (
	"encoding/json"
	"testing"
)

func TestCatalogSeparatesContextAndInput(t *testing.T) {
	raw, err := Compact([]byte(`{"openai":{"models":{"astra":{"limit":{"context":1050000,"input":922000,"output":128000}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string][]int
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["astra"][0] != 1050000 || len(got["astra"]) < 4 || got["astra"][3] != 922000 {
		t.Fatalf("limits=%v", got)
	}
}

func TestInputBudgetReservesOutput(t *testing.T) {
	for _, tc := range []struct{ window, output, want int }{{1050000, 128000, 922000}, {200000, 32000, 168000}, {10000, 20000, 0}} {
		if got := InputBudget("gpt-6-astra", tc.window, tc.output); got != tc.want {
			t.Fatalf("%+v got %d", tc, got)
		}
	}
}

func TestOmittedOutputDoesNotConsumeDeploymentWindow(t *testing.T) {
	if got := InputBudget("gpt-6-astra", 64000, 0); got != 64000 {
		t.Fatalf("omitted output left %d", got)
	}
}

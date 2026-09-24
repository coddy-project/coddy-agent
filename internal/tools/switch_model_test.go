package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestSwitchModelDescriptionRequiresUserRequest(t *testing.T) {
	description := SwitchModelTool(nil).Definition.Description
	if !strings.Contains(description, "only when the user asks") {
		t.Fatalf("description does not restrict switching to a user request: %q", description)
	}
	for _, unsolicited := range []string{"hard bug", "routine work", "stronger model", "faster one"} {
		if strings.Contains(description, unsolicited) {
			t.Errorf("description still recommends unsolicited switching: %q", unsolicited)
		}
	}
}

func TestSwitchModelDefaultsToSessionScope(t *testing.T) {
	var got tooling.ModelSwitch
	tool := SwitchModelTool(nil)
	_, err := tool.Execute(context.Background(), `{"model":"example/model"}`, &tooling.Env{
		SwitchModel: func(_ context.Context, req tooling.ModelSwitch) (string, error) {
			got = req
			return "ok", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Session {
		t.Fatal("an unscoped user-requested model switch must last for the session")
	}
}

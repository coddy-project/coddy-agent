package extensions

import (
	"context"
	"fmt"
	"testing"

	"github.com/cucumber/godog"
)

func TestUIExtensionsFeature(t *testing.T) {
	var store *Store
	var zip []byte
	var row Entry
	suite := godog.TestSuite{Name: "ui_extensions", Options: &godog.Options{Format: "progress", Paths: []string{"../../features/ui_extensions.feature"}, TestingT: t}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
			store = NewStore(t.TempDir())
			return ctx, nil
		})
		sc.Step(`^a JavaScript extension archive$`, func() { zip = fixture(t) })
		sc.Step(`^I install the extension$`, func() error { var err error; row, err = store.Install(zip); return err })
		sc.Step(`^the extension is disabled$`, func() error {
			if row.Enabled {
				return fmt.Errorf("enabled after installation")
			}
			return nil
		})
		sc.Step(`^I approve and enable the installed digest$`, func() error { var err error; row, err = store.SetEnabled(row.ID, row.SHA256, true); return err })
		sc.Step(`^its JavaScript can be loaded$`, func() error { _, err := store.Asset(row.ID, row.SHA256, row.UI.Entry); return err })
		sc.Step(`^I disable the extension$`, func() error { _, err := store.SetEnabled(row.ID, row.SHA256, false); return err })
		sc.Step(`^its JavaScript is unavailable$`, func() error {
			if _, err := store.Asset(row.ID, row.SHA256, row.UI.Entry); err == nil {
				return fmt.Errorf("disabled JavaScript served")
			}
			return nil
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("UI extensions feature failed")
	}
}

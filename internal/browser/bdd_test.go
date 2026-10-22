package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

func TestBrowserExtensionFeature(t *testing.T) {
	var hub *Hub
	var connection *Connection
	var command Command
	var result json.RawMessage
	var callErr error
	var finished chan struct{}
	suite := godog.TestSuite{Name: "browser-extension", Options: &godog.Options{Format: "progress", Paths: []string{"../../features/browser_extension.feature"}, TestingT: t}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Step(`^a browser extension connected to Coddy$`, func() error {
			hub = NewHub()
			var err error
			connection, err = hub.Connect("browser-test", "Firefox", []string{"snapshot"})
			return err
		})
		sc.Step(`^the user shared an example tab$`, func() error {
			return hub.UpdateTabs(connection, []Tab{{ID: 1, URL: "https://example.com", Title: "Example"}})
		})
		sc.Step(`^a chat asks for the page snapshot$`, func() {
			finished = make(chan struct{})
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				result, callErr = hub.Call(ctx, "chat", connection.ID, Command{Action: "snapshot", TabID: 1})
				close(finished)
			}()
		})
		sc.Step(`^the extension receives the command for the shared tab$`, func() error {
			select {
			case command = <-connection.Commands:
			case <-time.After(time.Second):
				return fmt.Errorf("command missing")
			}
			if command.TabID != 1 || command.SessionID != "chat" {
				return fmt.Errorf("wrong target: %+v", command)
			}
			hub.Reply(connection, Reply{ID: command.ID, Result: json.RawMessage(`{"text":"Example page"}`)})
			return nil
		})
		sc.Step(`^the chat receives the page contents$`, func() error {
			<-finished
			if callErr != nil {
				return callErr
			}
			if string(result) != `{"text":"Example page"}` {
				return fmt.Errorf("wrong page: %s", result)
			}
			return nil
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("browser feature failed")
	}
}

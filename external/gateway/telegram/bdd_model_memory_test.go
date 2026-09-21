//go:build gateway || gateway.telegram

package telegram

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/cucumber/godog"
)

// A second private chat: keyed by its own user id, so it mints a session of
// its own under the individual isolation the tests run with.
const (
	secondChatID = int64(5555)
	secondUserID = int64(777777)
)

// secondChatSends delivers a text from the second chat: the same processMessage
// path the first chat takes, on its own session key.
func (w *modelSwitchWorld) secondChatSends(text string) error {
	if err := w.buildBot(); err != nil {
		return err
	}
	msg := w.f.userMessage(secondChatID, secondUserID, text)
	key := fmt.Sprintf("tg:user:%d", secondUserID)
	w.bot.processMessage(context.Background(), w.f.api, msg, key)
	return nil
}

// sessionModelIsForUser asserts the effective model of the session the given
// user's chat is bound to.
func (w *modelSwitchWorld) sessionModelIsForUser(userID int64, model string) error {
	id := w.bot.store.Peek(fmt.Sprintf("tg:user:%d", userID))
	if id == "" {
		return fmt.Errorf("user %d has no session", userID)
	}
	w.runner.mu.Lock()
	st := w.runner.onDisk[id]
	w.runner.mu.Unlock()
	if st == nil {
		return fmt.Errorf("session %s for user %d is not on disk", id, userID)
	}
	if got := st.EffectiveModelID(w.runner.cfg); got != model {
		return fmt.Errorf("session model for user %d = %q, want %q", userID, got, model)
	}
	return nil
}

func (w *modelSwitchWorld) chatSessionModelIs(model string) error {
	return w.sessionModelIsForUser(modelSwitchUserID, model)
}

func (w *modelSwitchWorld) secondChatSessionModelIs(model string) error {
	return w.sessionModelIsForUser(secondUserID, model)
}

func initializeModelMemoryScenario(sc *godog.ScenarioContext) {
	w := &modelSwitchWorld{}

	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if w.f != nil {
			w.f.close()
		}
		if w.logClose != nil {
			_ = w.logClose.Close()
		}
		if w.logDir != "" {
			_ = os.RemoveAll(w.logDir)
		}
		return ctx, err
	})

	sc.Given(`^a telegram gateway with the models "([^"]*)" and "([^"]*)"$`, w.gatewayWithModels)

	sc.When(`^the user sends "([^"]*)"$`, w.sendCommand)
	sc.When(`^the user taps the button for "([^"]*)"$`, w.tapButton)
	sc.When(`^the second chat sends "([^"]*)"$`, w.secondChatSends)

	sc.Then(`^the chat's session model is "([^"]*)"$`, w.chatSessionModelIs)
	sc.Then(`^the second chat's session model is "([^"]*)"$`, w.secondChatSessionModelIs)
}

func TestModelMemoryFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "gateway-telegram-model-memory",
		ScenarioInitializer: initializeModelMemoryScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/gateway_telegram_model_memory.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("telegram model memory feature suite failed")
	}
}

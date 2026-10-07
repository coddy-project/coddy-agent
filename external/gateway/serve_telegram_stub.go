//go:build !(gateway || gateway.telegram)

package gateway

import (
	"context"
	"errors"
)

// TelegramAvailable reports whether this binary carries the Telegram adapter.
const TelegramAvailable = false

// ServeTelegram reports that the Telegram adapter was left out of this build.
func ServeTelegram(context.Context, Options) error {
	return errors.New("gateway: Telegram is not built in (rebuild with -tags gateway.telegram, or -tags gateway for every adapter)")
}

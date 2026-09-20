package llm

import (
	"strconv"
	"time"
)

const (
	ProviderUsageUnauthorized = "unauthorized"
	ProviderUsageUnavailable  = "unavailable"
	ProviderUsageInvalid      = "invalid"
)

type ProviderUsageError struct {
	Status     int
	Kind       string
	RetryAfter time.Duration
	Detail     string
}

func (e *ProviderUsageError) Error() string {
	msg := "provider usage: " + e.Kind
	if e.Status > 0 {
		msg += " (HTTP " + strconv.Itoa(e.Status) + ")"
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

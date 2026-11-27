//go:build windows

package localipc

import (
	"sync"

	"golang.org/x/sys/windows/svc/eventlog"
)

var (
	claimTraceOnce sync.Once
	claimTraceLog  *eventlog.Log
)

func traceClaim(eventID uint32) {
	claimTraceOnce.Do(func() { claimTraceLog, _ = eventlog.Open("Coddy") })
	if claimTraceLog != nil {
		_ = claimTraceLog.Info(eventID, "Coddy local IPC startup checkpoint")
	}
}

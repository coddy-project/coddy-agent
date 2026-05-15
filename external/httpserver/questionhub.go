//go:build http

package httpserver

import (
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

type questionWaitKey struct {
	sessionID string
	requestID string
}

var (
	questionWaitsMu sync.Mutex
	questionWaits   = make(map[questionWaitKey]chan *acp.QuestionResult)
)

func registerQuestionWait(sessionID, requestID string) <-chan *acp.QuestionResult {
	questionWaitsMu.Lock()
	defer questionWaitsMu.Unlock()
	k := questionWaitKey{sessionID: sessionID, requestID: requestID}
	ch := make(chan *acp.QuestionResult, 1)
	questionWaits[k] = ch
	return ch
}

func unregisterQuestionWait(sessionID, requestID string) {
	questionWaitsMu.Lock()
	defer questionWaitsMu.Unlock()
	delete(questionWaits, questionWaitKey{sessionID: sessionID, requestID: requestID})
}

// CompleteQuestionAnswer resolves a pending HTTP/streaming question. Returns false if nothing was waiting.
func CompleteQuestionAnswer(sessionID, requestID string, res *acp.QuestionResult) bool {
	questionWaitsMu.Lock()
	defer questionWaitsMu.Unlock()
	k := questionWaitKey{sessionID: sessionID, requestID: requestID}
	ch, ok := questionWaits[k]
	if !ok {
		return false
	}
	delete(questionWaits, k)
	ch <- res
	return true
}

//go:build http

package httpserver

import (
	"testing"
	"time"
)

func TestWaitPermissionResumeDrainedBlocksUntilGoroutineFinishes(t *testing.T) {
	srv := New(nil, nil, nil, "")
	srv.permissionResumeWG.Add(1)
	block := make(chan struct{})
	go func() {
		defer srv.permissionResumeWG.Done()
		<-block
	}()

	drained := make(chan struct{})
	go func() {
		srv.waitPermissionResumeDrained()
		close(drained)
	}()

	select {
	case <-drained:
		t.Fatal("drain returned before permission resume goroutine finished")
	case <-time.After(50 * time.Millisecond):
	}

	close(block)

	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for permission resume drain")
	}
}

func TestWaitPermissionResumeDrainedNilServer(t *testing.T) {
	var srv *Server
	srv.waitPermissionResumeDrained()
}

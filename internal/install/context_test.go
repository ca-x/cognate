package install

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCanceledCopyDoesNotWaitForSessionLock(t *testing.T) {
	session := &Session{}
	session.mutex.Lock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result := make(chan error, 1)
	go func() { result <- session.CopyStage(ctx, "operation", 0, "source") }()
	select {
	case err := <-result:
		session.mutex.Unlock()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		session.mutex.Unlock()
		t.Fatal("canceled call waited for session lock")
	}
}

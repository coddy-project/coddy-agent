package wallpaper

import (
	"context"
	"io"
)

// Upload bodies never hold this lock. It covers only deduplication, publication
// and deletion, including requests served by different Server instances.
var mutationGate = make(chan struct{}, 1)

func lockMutations(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case mutationGate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			unlockMutations()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func unlockMutations() { <-mutationGate }

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return n, ctxErr
	}
	return n, err
}

func mutationError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnavailable
}

package graphqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type lifecycleKey struct{}

// Acquire serializes a whole logical operation across independent handles/processes.
// Lock files are stable lifecycle identities and must never be removed.
func (b *Backend) Acquire(ctx context.Context, id string) (context.Context, func(), error) {
	return b.acquirePath(ctx, b.path(id))
}
func (b *Backend) acquirePath(ctx context.Context, path string) (context.Context, func(), error) {
	if e := ctx.Err(); e != nil {
		return ctx, nil, e
	}
	if b.closed.Load() {
		return ctx, nil, errors.New("storage closed")
	}
	path, e := filepath.Abs(path)
	if e != nil {
		return ctx, nil, e
	}
	if held, _ := ctx.Value(lifecycleKey{}).(string); held == path {
		return ctx, func() {}, nil
	}
	f, e := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return ctx, nil, e
	}
	for {
		if e = ctx.Err(); e != nil {
			f.Close()
			return ctx, nil, e
		}
		e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if e == nil {
			break
		}
		if !errors.Is(e, syscall.EWOULDBLOCK) && !errors.Is(e, syscall.EAGAIN) {
			f.Close()
			return ctx, nil, e
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return ctx, nil, ctx.Err()
		case <-timer.C:
		}
	}
	if e = ctx.Err(); e != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return ctx, nil, e
	}
	return context.WithValue(ctx, lifecycleKey{}, path), func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

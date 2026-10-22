package cuttle

import (
	"context"
	"fmt"
	"strings"
)

func (s *Store) CreateGraph(ctx context.Context, g GraphID) error {
	if e := s.lock(ctx); e != nil {
		return e
	}
	defer s.unlock()
	if e := s.check(ctx); e != nil {
		return e
	}
	if strings.TrimSpace(string(g)) == "" || !textValid(string(g)) || len(g) > 1024 {
		return fmt.Errorf("%w: graph identifier", ErrInvalid)
	}
	ctx, release, e := s.graphLock(ctx, g)
	if e != nil {
		return e
	}
	defer release()
	return s.storage.Create(ctx, g)
}
func (s *Store) Graphs(ctx context.Context) ([]GraphID, error) {
	if e := s.lock(ctx); e != nil {
		return nil, e
	}
	defer s.unlock()
	if e := s.check(ctx); e != nil {
		return nil, e
	}
	return s.storage.List(ctx)
}

// DeleteGraph physically removes a complete graph, including history.
func (s *Store) DeleteGraph(ctx context.Context, g GraphID) error {
	if e := s.lock(ctx); e != nil {
		return e
	}
	defer s.unlock()
	if e := s.check(ctx); e != nil {
		return e
	}
	ctx, release, e := s.graphLock(ctx, g)
	if e != nil {
		return e
	}
	defer release()
	return s.storage.Delete(ctx, g)
}

package cuttle

import (
	"context"
	"errors"
	"fmt"
	backend "github.com/a1knowhow/cuttle/internal/storage/graphqlite"
	"sort"
)

type Store struct {
	gate    chan struct{}
	storage Storage
	options Options
	closed  bool
}

func Open(directory string, options ...Options) (*Store, error) {
	if len(options) > 1 {
		return nil, ErrInvalid
	}
	o := Options{}
	if len(options) == 1 {
		o = options[0]
	}
	o = defaults(o)
	storage := o.Storage
	if storage == nil {
		b, e := backend.Open(directory)
		if e != nil {
			return nil, e
		}
		storage = &diskStorage{b}
	}
	return &Store{gate: make(chan struct{}, 1), storage: storage, options: o}, nil
}
func canceled(e error) error {
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrCanceled, e)
	}
	return e
}
func (s *Store) check(ctx context.Context) error {
	if s.closed {
		return ErrClosed
	}
	return canceled(ctx.Err())
}
func (s *Store) Close() error {
	s.lock(context.Background())
	defer s.unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.storage.Close()
}
func (s *Store) Capabilities() Capabilities {
	return Capabilities{Backend: "GraphQLite 0.12.0", Dialect: "bounded GraphQLite read subset", MaxGraphs: s.options.MaxGraphs, MaxRows: s.options.MaxRows, MaxBytes: s.options.MaxBytes, Cancellation: "context cancellation through SQLite interrupt; procedures and variable paths excluded", MultiGraph: "independent per-file snapshots; aggregation, ordering, and limits per graph; no cross-graph joins"}
}
func latest(ctx context.Context, cs []Commit) (state, Revision, error) {
	st := state{}
	var rev Revision
	for _, c := range cs {
		if e := ctx.Err(); e != nil {
			return state{}, 0, canceled(e)
		}
		var delta state
		if e := decodeJSON(c.Data, &delta); e != nil {
			return state{}, 0, e
		}
		for _, vs := range [][]Version{delta.Current, delta.History} {
			for _, version := range vs {
				if version.Node != nil {
					if e := validateJSONText(version.Node.Properties, 0); e != nil {
						return state{}, 0, e
					}
				} else if version.Relationship != nil {
					if e := validateJSONText(version.Relationship.Properties, 0); e != nil {
						return state{}, 0, e
					}
				}
			}
		}
		if e := normalizeVersions(&delta); e != nil {
			return state{}, 0, e
		}
		touched := map[ID]bool{}
		for _, id := range delta.Touched {
			touched[id] = true
		}
		kept := st.Current[:0]
		for _, v := range st.Current {
			if !touched[identity(v)] {
				kept = append(kept, v)
			}
		}
		st.Current = append(kept, delta.Current...)
		st.History = append(st.History, delta.History...)
		rev = c.Token
	}
	if e := ctx.Err(); e != nil {
		return state{}, 0, canceled(e)
	}
	return st, rev, nil
}

type diskStorage struct{ b *backend.Backend }

func mapStorage(e error) error {
	switch {
	case errors.Is(e, backend.ErrMissing):
		return ErrNotFound
	case errors.Is(e, backend.ErrExists):
		return ErrExists
	case errors.Is(e, backend.ErrConflict):
		return ErrConflict
	default:
		return canceled(e)
	}
}
func (d *diskStorage) List(ctx context.Context) ([]GraphID, error) {
	ids, e := d.b.List(ctx)
	out := make([]GraphID, len(ids))
	for i, id := range ids {
		out[i] = GraphID(id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, mapStorage(e)
}
func (d *diskStorage) Create(ctx context.Context, g GraphID) error {
	return mapStorage(d.b.Create(ctx, string(g)))
}
func (d *diskStorage) Read(ctx context.Context, g GraphID) ([]Commit, error) {
	rs, e := d.b.Read(ctx, string(g))
	cs := make([]Commit, len(rs))
	for i, r := range rs {
		cs[i] = Commit{Revision(r.Token), r.RecordedAt, r.Data}
	}
	return cs, mapStorage(e)
}
func (d *diskStorage) Append(ctx context.Context, g GraphID, expected Revision, c Commit) error {
	return mapStorage(d.b.Append(ctx, string(g), uint64(expected), backend.Record{Token: uint64(c.Token), RecordedAt: c.RecordedAt, Data: c.Data}))
}
func (d *diskStorage) Delete(ctx context.Context, g GraphID) error {
	return mapStorage(d.b.Delete(ctx, string(g)))
}
func (d *diskStorage) Close() error { return d.b.Close() }

func (s *Store) lock(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return canceled(e)
	}
	select {
	case s.gate <- struct{}{}:
		if e := ctx.Err(); e != nil {
			s.unlock()
			return canceled(e)
		}
		return nil
	case <-ctx.Done():
		return canceled(ctx.Err())
	}
}
func (s *Store) unlock() { <-s.gate }
func (s *Store) graphLock(ctx context.Context, g GraphID) (context.Context, func(), error) {
	if d, ok := s.storage.(*diskStorage); ok {
		next, unlock, e := d.b.Acquire(ctx, string(g))
		return next, unlock, mapStorage(e)
	}
	return ctx, func() {}, nil
}

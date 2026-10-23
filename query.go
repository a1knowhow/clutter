package cuttle

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
)

// View captures a separate immutable commit snapshot for each explicitly selected graph.
type View struct {
	store         *Store
	nodes         []Node
	relationships []Relationship
	snapshots     []Snapshot
}

func (s *Store) View(ctx context.Context, graphs []GraphID, b Basis) (*View, error) {
	if e := s.lock(ctx); e != nil {
		return nil, e
	}
	defer s.unlock()
	if e := s.check(ctx); e != nil {
		return nil, e
	}
	if len(graphs) == 0 {
		return nil, fmt.Errorf("%w: explicit graph selection required", ErrInvalid)
	}
	if len(graphs) > s.options.MaxGraphs {
		return nil, ErrCapacity
	}
	if !b.KnownAt.IsZero() && b.Token != 0 {
		return nil, fmt.Errorf("%w: choose recorded time or token", ErrInvalid)
	}
	if b.ValidAt.IsZero() {
		b.ValidAt = s.options.Clock().UTC()
	}
	v := &View{store: s}
	seen := map[GraphID]bool{}
	for _, g := range graphs {
		if e := ctx.Err(); e != nil {
			return nil, canceled(e)
		}
		if seen[g] {
			return nil, fmt.Errorf("%w: duplicate graph", ErrInvalid)
		}
		seen[g] = true
		readCtx, release, e := s.graphLock(ctx, g)
		if e != nil {
			return nil, e
		}
		cs, e := s.storage.Read(readCtx, g)
		release()
		if e != nil {
			return nil, e
		}
		var selected *Commit
		for i := range cs {
			c := &cs[i]
			if b.Token != 0 && c.Token > b.Token {
				break
			}
			if !b.KnownAt.IsZero() && c.RecordedAt.After(b.KnownAt) {
				break
			}
			selected = c
		}
		snap := Snapshot{Graph: g, ValidAt: b.ValidAt}
		if selected != nil {
			snap.Token = selected.Token
			snap.RecordedAt = selected.RecordedAt
			selectedCommits := []Commit{}
			for _, c := range cs {
				if c.Token <= selected.Token {
					selectedCommits = append(selectedCommits, c)
				}
			}
			st, _, e := latest(ctx, selectedCommits)
			if e != nil {
				return nil, e
			}
			for _, version := range st.Current {
				if !contains(interval(version), b.ValidAt) {
					continue
				}
				if version.Node != nil {
					v.nodes = append(v.nodes, *version.Node)
				} else {
					v.relationships = append(v.relationships, *version.Relationship)
				}
			}
		}
		v.snapshots = append(v.snapshots, snap)
	}
	if len(v.nodes)+len(v.relationships) > s.options.MaxEntities {
		return nil, ErrCapacity
	}
	if e := ctx.Err(); e != nil {
		return nil, canceled(e)
	}
	return v, nil
}
func (v *View) Snapshots() []Snapshot { return append([]Snapshot(nil), v.snapshots...) }
func (v *View) check(ctx context.Context) error {
	if e := v.store.lock(ctx); e != nil {
		return e
	}
	defer v.store.unlock()
	return v.store.check(ctx)
}

type NodeQuery struct {
	view    *View
	label   string
	filters Properties
	limit   int
}

func (v *View) Nodes() *NodeQuery                           { return &NodeQuery{view: v, filters: Properties{}} }
func (q *NodeQuery) Label(label string) *NodeQuery          { q.label = label; return q }
func (q *NodeQuery) Where(key string, value any) *NodeQuery { q.filters[key] = value; return q }
func (q *NodeQuery) Limit(n int) *NodeQuery                 { q.limit = n; return q }
func (q *NodeQuery) All(ctx context.Context) ([]Node, error) {
	if e := q.view.check(ctx); e != nil {
		return nil, e
	}
	if q.limit < 0 {
		return nil, ErrInvalid
	}
	out := []Node{}
	for _, n := range q.view.nodes {
		if e := ctx.Err(); e != nil {
			return nil, canceled(e)
		}
		match := q.label == ""
		for _, l := range n.Labels {
			if l == q.label {
				match = true
			}
		}
		for k, val := range q.filters {
			if !equal(n.Properties[k], val) {
				match = false
			}
		}
		if !match {
			continue
		}
		out = append(out, n)
		if q.limit > 0 && len(out) >= q.limit {
			break
		}
	}
	return bounded(out, q.view.store.options)
}
func equal(a, b any) bool {
	if a == nil || b == nil {
		return false
	}
	aa, e := json.Marshal(a)
	if e != nil {
		return false
	}
	bb, e := json.Marshal(b)
	return e == nil && reflect.DeepEqual(aa, bb)
}
func bounded[T any](out []T, o Options) ([]T, error) {
	if len(out) > o.MaxRows {
		return nil, ErrCapacity
	}
	b, e := json.Marshal(out)
	if e != nil {
		return nil, e
	}
	if len(b) > o.MaxBytes {
		return nil, ErrCapacity
	}
	var copy []T
	if e = decodeJSON(b, &copy); e != nil {
		return nil, e
	}
	for _, item := range copy {
		switch x := any(item).(type) {
		case Node:
			if _, e := normalizeNumbers(x.Properties); e != nil {
				return nil, e
			}
		case Relationship:
			if _, e := normalizeNumbers(x.Properties); e != nil {
				return nil, e
			}
		}
	}
	return copy, nil
}

type RelationshipQuery struct {
	view     *View
	kind     string
	from, to ID
	limit    int
}

func (v *View) Relationships() *RelationshipQuery                { return &RelationshipQuery{view: v} }
func (q *RelationshipQuery) Type(kind string) *RelationshipQuery { q.kind = kind; return q }
func (q *RelationshipQuery) From(id ID) *RelationshipQuery       { q.from = id; return q }
func (q *RelationshipQuery) To(id ID) *RelationshipQuery         { q.to = id; return q }
func (q *RelationshipQuery) Limit(n int) *RelationshipQuery      { q.limit = n; return q }
func (q *RelationshipQuery) All(ctx context.Context) ([]Relationship, error) {
	if e := q.view.check(ctx); e != nil {
		return nil, e
	}
	if q.limit < 0 {
		return nil, ErrInvalid
	}
	out := []Relationship{}
	for _, r := range q.view.relationships {
		if e := ctx.Err(); e != nil {
			return nil, canceled(e)
		}
		if q.kind != "" && r.Type != q.kind || q.from != "" && r.From != q.from || q.to != "" && r.To != q.to {
			continue
		}
		out = append(out, r)
		if q.limit > 0 && len(out) >= q.limit {
			break
		}
	}
	return bounded(out, q.view.store.options)
}

package cuttle

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

var testCtx = context.Background()

func instant(s string) time.Time {
	t, e := time.Parse(time.RFC3339, s)
	if e != nil {
		panic(e)
	}
	return t
}

var start = instant("2026-10-01T00:00:00Z")

func createNode(id ID, name string) Operation {
	return Operation{Kind: "create", ID: id, Valid: Interval{From: start}, Node: &Node{Labels: []string{"Requirement"}, Properties: Properties{"name": name, "priority": 1}, Evidence: []Evidence{{Source: "tender", Revision: "r1", Locator: "page 1", Property: "name"}, {Source: "review", Revision: "r2"}}}, Reason: "import"}
}
func openTest(t *testing.T, options ...Options) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "store")
	s, e := Open(dir, options...)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if e = s.CreateGraph(testCtx, "tender"); e != nil {
		t.Fatal(e)
	}
	return s, dir
}
func applyTest(t *testing.T, s *Store, ops ...Operation) Revision {
	t.Helper()
	r, e := s.Apply(testCtx, "tender", Batch{ops})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func viewTest(t *testing.T, s *Store, b Basis) *View {
	t.Helper()
	v, e := s.View(testCtx, []GraphID{"tender"}, b)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestAtomicPersistenceEvidence(t *testing.T) {
	s, dir := openTest(t)
	edge := Operation{Kind: "create", ID: "edge", Valid: Interval{From: start}, Relationship: &Relationship{From: "a", To: "missing", Type: "DEPENDS_ON"}, Reason: "import"}
	if _, e := s.Apply(testCtx, "tender", Batch{[]Operation{createNode("a", "first"), edge}}); !errors.Is(e, ErrStranded) {
		t.Fatal(e)
	}
	h, e := s.History(testCtx, "tender", "")
	if e != nil || len(h) != 0 {
		t.Fatal(h, e)
	}
	edge.Relationship.To = "b"
	applyTest(t, s, createNode("a", "first"), createNode("b", "second"), edge)
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	v := viewTest(t, s, Basis{ValidAt: start})
	nodes, e := v.Nodes().Label("Requirement").Where("name", "first").All(testCtx)
	if e != nil || len(nodes) != 1 || len(nodes[0].Evidence) != 2 {
		t.Fatal(nodes, e)
	}
	rels, e := v.Relationships().Type("DEPENDS_ON").From("a").All(testCtx)
	if e != nil || len(rels) != 1 || rels[0].Graph != "tender" {
		t.Fatal(rels, e)
	}
	h, e = s.History(testCtx, "tender", "a")
	if e != nil || len(h) != 1 || h[0].Reason != "import" {
		t.Fatal(h, e)
	}
}
func TestConflictIntervalsAndStranding(t *testing.T) {
	s, _ := openTest(t)
	r := applyTest(t, s, createNode("a", "old"), createNode("b", "other"), Operation{Kind: "create", ID: "edge", Valid: Interval{From: start}, Relationship: &Relationship{From: "a", To: "b", Type: "LINK"}, Reason: "import"})
	op := createNode("a", "new")
	op.Kind = "correct"
	op.Expected = r + 1
	if _, e := s.Apply(testCtx, "tender", Batch{[]Operation{op}}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	op.Expected = r
	op.Valid.To = start
	if _, e := s.Apply(testCtx, "tender", Batch{[]Operation{op}}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	ret := Operation{Kind: "retract", ID: "a", Expected: r, Valid: Interval{From: start}, Reason: "unsupported"}
	if _, e := s.Apply(testCtx, "tender", Batch{[]Operation{ret}}); !errors.Is(e, ErrStranded) {
		t.Fatal(e)
	}
	applyTest(t, s, ret, Operation{Kind: "retract", ID: "edge", Expected: r, Valid: Interval{From: start}, Reason: "endpoint retracted"})
	nodes, e := viewTest(t, s, Basis{ValidAt: start}).Nodes().All(testCtx)
	if e != nil || len(nodes) != 1 {
		t.Fatal(nodes, e)
	}
	h, e := s.History(testCtx, "tender", "a")
	if e != nil || len(h) != 2 || !h[1].Retracted {
		t.Fatal(h, e)
	}
	if _, e = s.Apply(testCtx, "tender", Batch{[]Operation{createNode("a", "reuse")}}); !errors.Is(e, ErrExists) {
		t.Fatal(e)
	}
}
func TestCancellationAndClosed(t *testing.T) {
	s, _ := openTest(t)
	ctx, cancel := context.WithCancel(testCtx)
	cancel()
	if _, e := s.Apply(ctx, "tender", Batch{[]Operation{createNode("a", "x")}}); !errors.Is(e, ErrCanceled) || !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	h, e := s.History(testCtx, "tender", "")
	if e != nil || len(h) != 0 {
		t.Fatal(h, e)
	}
	v := viewTest(t, s, Basis{})
	s.Close()
	if _, e = s.Graphs(testCtx); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	if _, e = v.Nodes().All(testCtx); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
}
func TestMultiGraphCapacityAndCopies(t *testing.T) {
	s, _ := openTest(t, Options{MaxGraphs: 2})
	applyTest(t, s, createNode("same", "one"))
	if e := s.CreateGraph(testCtx, "other"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Apply(testCtx, "other", Batch{[]Operation{createNode("same", "two")}}); e != nil {
		t.Fatal(e)
	}
	v, e := s.View(testCtx, []GraphID{"tender", "other"}, Basis{ValidAt: start})
	if e != nil {
		t.Fatal(e)
	}
	nodes, e := v.Nodes().All(testCtx)
	if e != nil || len(nodes) != 2 || nodes[0].Graph == nodes[1].Graph {
		t.Fatal(nodes, e)
	}
	nodes[0].Properties["name"] = "mutated"
	again, _ := v.Nodes().All(testCtx)
	if again[0].Properties["name"] == "mutated" {
		t.Fatal("mutable view leaked")
	}
	if _, e = s.View(testCtx, []GraphID{"tender", "other", "third"}, Basis{}); !errors.Is(e, ErrCapacity) {
		t.Fatal(e)
	}
	if _, e = s.View(testCtx, []GraphID{"missing"}, Basis{}); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.View(testCtx, nil, Basis{}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
func TestIndependentStoresConflict(t *testing.T) {
	s, dir := openTest(t)
	other, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	r := applyTest(t, s, createNode("a", "old"))
	op := createNode("a", "new")
	op.Kind = "correct"
	op.Expected = r
	applyTest(t, s, op)
	if _, e = other.Apply(testCtx, "tender", Batch{[]Operation{op}}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

type blockedRead struct {
	Storage
	entered chan struct{}
	release chan struct{}
}

func (b *blockedRead) Read(ctx context.Context, g GraphID) ([]Commit, error) {
	close(b.entered)
	select {
	case <-b.release:
		return b.Storage.Read(ctx, g)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestCanceledWaitersDoNotWaitForStoreLock(t *testing.T) {
	s, _ := openTest(t)
	v := viewTest(t, s, Basis{})
	b := &blockedRead{Storage: s.storage, entered: make(chan struct{}), release: make(chan struct{})}
	s.storage = b
	holder := make(chan error, 1)
	go func() { _, e := s.History(testCtx, "tender", ""); holder <- e }()
	<-b.entered
	defer close(b.release)
	operations := []struct {
		name string
		run  func(context.Context) error
	}{{"graphs", func(ctx context.Context) error { _, e := s.Graphs(ctx); return e }}, {"view", func(ctx context.Context) error { _, e := s.View(ctx, []GraphID{"tender"}, Basis{}); return e }}, {"history", func(ctx context.Context) error { _, e := s.History(ctx, "tender", ""); return e }}, {"apply", func(ctx context.Context) error {
		_, e := s.Apply(ctx, "tender", Batch{[]Operation{createNode("a", "x")}})
		return e
	}}, {"create", func(ctx context.Context) error { return s.CreateGraph(ctx, "another") }}, {"delete", func(ctx context.Context) error { return s.DeleteGraph(ctx, "tender") }}, {"fluent", func(ctx context.Context) error { _, e := v.Nodes().All(ctx); return e }}, {"cypher", func(ctx context.Context) error { _, e := v.Cypher(ctx, "RETURN 1", nil); return e }}}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(testCtx, 10*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- op.run(ctx) }()
			select {
			case e := <-done:
				if !errors.Is(e, ErrCanceled) || !errors.Is(e, context.DeadlineExceeded) {
					t.Fatal(e)
				}
			case <-time.After(time.Second):
				t.Fatal("canceled waiter blocked on store lock")
			}
		})
	}
	select {
	case e := <-holder:
		t.Fatal("holder unexpectedly finished", e)
	default:
	}
}

type replayContext struct {
	context.Context
	cancel context.CancelFunc
	calls  int
	after  int
}

func (c *replayContext) Err() error {
	c.calls++
	if c.calls == c.after {
		c.cancel()
	}
	return c.Context.Err()
}
func TestCancellationDuringReplay(t *testing.T) {
	data, _ := json.Marshal(state{})
	cs := make([]Commit, 10)
	for i := range cs {
		cs[i] = Commit{Token: Revision(i + 1), Data: data}
	}
	ctx, cancel := context.WithCancel(testCtx)
	defer cancel()
	stepped := &replayContext{Context: ctx, cancel: cancel, after: 3}
	if _, _, e := latest(stepped, cs); !errors.Is(e, ErrCanceled) || !errors.Is(e, context.Canceled) || stepped.calls != 3 {
		t.Fatal(e, stepped.calls)
	}
	lastCtx, lastCancel := context.WithCancel(testCtx)
	defer lastCancel()
	stepped = &replayContext{Context: lastCtx, cancel: lastCancel, after: len(cs) + 1}
	if _, _, e := latest(stepped, cs); !errors.Is(e, ErrCanceled) {
		t.Fatal("missing final cancellation check", e)
	}
}
func TestMalformedOriginalStringsLeaveHistoryUnchanged(t *testing.T) {
	s, _ := openTest(t)
	r := applyTest(t, s, createNode("a", "ok"))
	bad := string([]byte{255})
	values := []any{[]string{bad}, map[string][]string{"valid": {bad}}, map[string][]string{bad: {"value"}}, map[string]map[string]string{"nested": {bad: "value"}}}
	for i, value := range values {
		op := createNode("a", "bad")
		op.Kind = "correct"
		op.Expected = r
		op.Node.Properties["nested"] = value
		if _, e := s.Apply(testCtx, "tender", Batch{[]Operation{op}}); !errors.Is(e, ErrInvalid) {
			t.Fatal(i, e)
		}
		v := viewTest(t, s, Basis{ValidAt: start})
		if _, e := v.Cypher(testCtx, "RETURN $nested AS value", Properties{"nested": value}); !errors.Is(e, ErrInvalid) {
			t.Fatal(i, e)
		}
	}
	h, e := s.History(testCtx, "tender", "a")
	if e != nil || len(h) != 1 || h[0].Node.Properties["name"] != "ok" {
		t.Fatal(h, e)
	}
}

package cuttle

import (
	"errors"
	"testing"
	"time"
)

func TestCorrectionVersusAmendment(t *testing.T) {
	recorded := instant("2026-10-10T00:00:00Z")
	s, _ := openTest(t, Options{Clock: func() time.Time { return recorded }})
	r := applyTest(t, s, createNode("deadline", "30 November"))
	recorded = instant("2026-10-20T00:00:00Z")
	op := createNode("deadline", "15 November")
	op.Kind = "correct"
	op.Expected = r
	op.Reason = "extraction correction"
	r = applyTest(t, s, op)
	validAt := instant("2026-10-15T00:00:00Z")
	before := viewTest(t, s, Basis{ValidAt: validAt, KnownAt: instant("2026-10-15T00:00:00Z")})
	after := viewTest(t, s, Basis{ValidAt: validAt})
	a, _ := before.Nodes().All(testCtx)
	b, _ := after.Nodes().All(testCtx)
	if a[0].Properties["name"] != "30 November" || b[0].Properties["name"] != "15 November" {
		t.Fatal(a, b)
	}
	tokenView := viewTest(t, s, Basis{ValidAt: validAt, Token: 1})
	a, _ = tokenView.Nodes().All(testCtx)
	if a[0].Properties["name"] != "30 November" {
		t.Fatal(a)
	}
	recorded = instant("2026-10-21T00:00:00Z")
	op.Expected = r
	op.Valid.From = instant("2026-10-18T00:00:00Z")
	op.Node.Properties["name"] = "20 November"
	op.Reason = "dated amendment"
	applyTest(t, s, op)
	a, _ = viewTest(t, s, Basis{ValidAt: validAt}).Nodes().All(testCtx)
	b, _ = viewTest(t, s, Basis{ValidAt: instant("2026-10-18T00:00:00Z")}).Nodes().All(testCtx)
	if a[0].Properties["name"] != "15 November" || b[0].Properties["name"] != "20 November" {
		t.Fatal(a, b)
	}
	if _, e := s.View(testCtx, []GraphID{"tender"}, Basis{Token: 1, KnownAt: recorded}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
func TestIntervalSplittingAndEvidenceRemoval(t *testing.T) {
	s, _ := openTest(t)
	r := applyTest(t, s, createNode("a", "old"))
	op := createNode("a", "new")
	op.Kind = "correct"
	op.Expected = r
	op.Valid = Interval{instant("2026-10-10T00:00:00Z"), instant("2026-10-20T00:00:00Z")}
	op.Node.Evidence = op.Node.Evidence[1:]
	r = applyTest(t, s, op)
	for _, tt := range []struct {
		at, want string
		supports int
	}{{"2026-10-09T00:00:00Z", "old", 2}, {"2026-10-10T00:00:00Z", "new", 1}, {"2026-10-20T00:00:00Z", "old", 2}} {
		nodes, e := viewTest(t, s, Basis{ValidAt: instant(tt.at)}).Nodes().All(testCtx)
		if e != nil || len(nodes) != 1 || nodes[0].Properties["name"] != tt.want || len(nodes[0].Evidence) != tt.supports {
			t.Fatal(tt, nodes, e)
		}
	}
	op.Expected = r
	op.Node.Evidence = nil
	op.Node.Properties = Properties{"name": "unsupported"}
	applyTest(t, s, op)
	nodes, _ := viewTest(t, s, Basis{ValidAt: op.Valid.From}).Nodes().All(testCtx)
	if len(nodes) != 1 || len(nodes[0].Evidence) != 0 {
		t.Fatal(nodes)
	}
	h, e := s.History(testCtx, "tender", "a")
	if e != nil || len(h) != 3 || len(h[0].Node.Evidence) != 2 {
		t.Fatal(h, e)
	}
}
func TestEqualClockOrderedCommitsAndIncrementalHistory(t *testing.T) {
	s, _ := openTest(t, Options{Clock: func() time.Time { return start }})
	r := applyTest(t, s, createNode("a", "1"))
	for i := 0; i < 5; i++ {
		op := createNode("a", "next")
		op.Kind = "correct"
		op.Expected = r
		r = applyTest(t, s, op)
	}
	h, e := s.History(testCtx, "tender", "a")
	if e != nil || len(h) != 6 {
		t.Fatal(h, e)
	}
	for i := 1; i < len(h); i++ {
		if h[i].Token <= h[i-1].Token || !h[i].RecordedAt.After(h[i-1].RecordedAt) {
			t.Fatal(h)
		}
	}
	cs, e := s.storage.Read(testCtx, "tender")
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range cs {
		if len(c.Data) > 2*len(cs[0].Data) {
			t.Fatalf("cumulative history duplication: first=%d latest=%d", len(cs[0].Data), len(c.Data))
		}
	}
}

func TestRetractionUsesOverlappingCurrentEvidence(t *testing.T) {
	s, _ := openTest(t)
	r := applyTest(t, s, createNode("a", "old"))
	amend := createNode("a", "new")
	amend.Kind = "correct"
	amend.Expected = r
	amend.Valid.From = instant("2026-10-18T00:00:00Z")
	amend.Node.Evidence = []Evidence{{Source: "amendment", Revision: "2"}}
	r = applyTest(t, s, amend)
	cut := Interval{instant("2026-10-05T00:00:00Z"), amend.Valid.From}
	r = applyTest(t, s, Operation{Kind: "retract", ID: "a", Expected: r, Valid: cut, Reason: "old extraction withdrawn"})
	h, e := s.History(testCtx, "tender", "a")
	if e != nil || len(h) != 3 {
		t.Fatal(h, e)
	}
	tomb := h[2]
	if !tomb.Retracted || tomb.Node.Properties["name"] != "old" || tomb.Node.Evidence[0].Source != "tender" || tomb.Node.Valid != cut || tomb.Token != r || tomb.Node.Revision != r {
		t.Fatal(tomb)
	}
	if _, e = s.Apply(testCtx, "tender", Batch{[]Operation{{Kind: "retract", ID: "a", Expected: r, Valid: cut, Reason: "already retracted"}}}); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	h, _ = s.History(testCtx, "tender", "a")
	if len(h) != 3 {
		t.Fatal(h)
	}
}
func TestRetractionSplitsAuditAtCurrentIntervals(t *testing.T) {
	s, _ := openTest(t)
	r := applyTest(t, s, createNode("a", "old"))
	amend := createNode("a", "new")
	amend.Kind = "correct"
	amend.Expected = r
	amend.Valid.From = instant("2026-10-18T00:00:00Z")
	amend.Node.Evidence = []Evidence{{Source: "amendment", Revision: "2"}}
	r = applyTest(t, s, amend)
	cut := Interval{instant("2026-10-10T00:00:00Z"), instant("2026-10-25T00:00:00Z")}
	r = applyTest(t, s, Operation{Kind: "retract", ID: "a", Expected: r, Valid: cut, Reason: "withdraw both"})
	h, _ := s.History(testCtx, "tender", "a")
	if len(h) != 4 {
		t.Fatal(h)
	}
	a, b := h[2], h[3]
	if a.Node.Valid != (Interval{cut.From, amend.Valid.From}) || a.Node.Properties["name"] != "old" || a.Node.Evidence[0].Source != "tender" || b.Node.Valid != (Interval{amend.Valid.From, cut.To}) || b.Node.Properties["name"] != "new" || b.Node.Evidence[0].Source != "amendment" || a.Node.Revision != r || b.Node.Revision != r {
		t.Fatal(a, b)
	}
}
func TestEntityLimitCountsSelectedEntities(t *testing.T) {
	s, _ := openTest(t, Options{MaxEntities: 1})
	r := applyTest(t, s, createNode("a", "old"))
	op := createNode("a", "new")
	op.Kind = "correct"
	op.Expected = r
	op.Valid.From = instant("2026-10-18T00:00:00Z")
	applyTest(t, s, op)
	for _, at := range []time.Time{start, op.Valid.From} {
		nodes, e := viewTest(t, s, Basis{ValidAt: at}).Nodes().All(testCtx)
		if e != nil || len(nodes) != 1 {
			t.Fatal(nodes, e)
		}
	}
}

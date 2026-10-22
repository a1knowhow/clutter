package cuttle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCypherFluentParityAndReadPolicy(t *testing.T) {
	s, _ := openTest(t)
	applyTest(t, s, createNode("a", "Alpha"), createNode("b", "Beta"), Operation{Kind: "create", ID: "r", Valid: Interval{From: start}, Relationship: &Relationship{From: "a", To: "b", Type: "LINK", Evidence: []Evidence{{Source: "tender", Revision: "1"}}}, Reason: "import"})
	v := viewTest(t, s, Basis{ValidAt: start})
	nodes, e := v.Nodes().Label("Requirement").Where("name", "Alpha").All(testCtx)
	if e != nil {
		t.Fatal(e)
	}
	result, e := v.Cypher(testCtx, "MATCH (n:Requirement) WHERE n.name=$name RETURN n._cuttle_id AS id, n.name AS name, n._cuttle_evidence AS evidence", Properties{"name": "Alpha"})
	if e != nil {
		t.Fatal(e)
	}
	if len(result.Rows) != len(nodes) || result.Rows[0]["id"] != string(nodes[0].ID) || result.Rows[0]["_cuttle_graph"] != "tender" {
		t.Fatal(result, nodes)
	}
	evidence, ok := result.Rows[0]["evidence"].([]any)
	if !ok || len(evidence) != 2 {
		t.Fatal(result)
	}
	r, e := v.Cypher(testCtx, "MATCH (a)-[r:LINK]->(b) RETURN a.name AS source,b.name AS target,r._cuttle_id AS id", nil)
	if e != nil || len(r.Rows) != 1 || r.Rows[0]["id"] != "r" {
		t.Fatal(r, e)
	}
	for _, q := range []string{"CREATE (:Hidden)", "MATCH (n) SET n.name='oops'", "CALL { CREATE (:Hidden) RETURN 1 } RETURN 1", "PROFILE MATCH (n) DELETE n", "RETURN load_extension('evil')", "MATCH (n) WHERE EXISTS { MATCH (m) DELETE m } RETURN n", "MATCH (n) FROM other RETURN n"} {
		if _, e = v.Cypher(testCtx, q, nil); !errors.Is(e, ErrUnsupported) {
			t.Fatal(q, e)
		}
	}
	h, _ := s.History(testCtx, "tender", "")
	if len(h) != 3 {
		t.Fatal(h)
	}
	r, e = v.Cypher(testCtx, "MATCH (n) WHERE n.name=$name RETURN n.name AS name", Properties{"name": "Alpha' CREATE (:Hidden) //"})
	if e != nil || len(r.Rows) != 0 {
		t.Fatal(r, e)
	}
}
func TestTemporalCypherParity(t *testing.T) {
	s, _ := openTest(t)
	r := applyTest(t, s, createNode("a", "before"))
	op := createNode("a", "after")
	op.Kind = "correct"
	op.Expected = r
	op.Valid.From = instant("2026-10-18T00:00:00Z")
	applyTest(t, s, op)
	for _, basis := range []Basis{{ValidAt: start}, {ValidAt: op.Valid.From}, {ValidAt: op.Valid.From, Token: r}} {
		v := viewTest(t, s, basis)
		nodes, e := v.Nodes().All(testCtx)
		if e != nil {
			t.Fatal(e)
		}
		rows, e := v.Cypher(testCtx, "MATCH (n) RETURN n.name AS name", nil)
		if e != nil || len(rows.Rows) != 1 || rows.Rows[0]["name"] != nodes[0].Properties["name"] {
			t.Fatal(basis, rows, nodes, e)
		}
	}
}
func TestQueryBoundsAndCancellation(t *testing.T) {
	s, _ := openTest(t, Options{MaxRows: 1, MaxQueryBytes: 1000})
	applyTest(t, s, createNode("a", "one"), createNode("b", "two"))
	v := viewTest(t, s, Basis{ValidAt: start})
	if _, e := v.Nodes().All(testCtx); !errors.Is(e, ErrCapacity) {
		t.Fatal(e)
	}
	if _, e := v.Cypher(testCtx, "MATCH (n) RETURN n.name AS name", nil); !errors.Is(e, ErrCapacity) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(testCtx)
	cancel()
	if _, e := v.Cypher(ctx, "MATCH (n) RETURN n", nil); !errors.Is(e, ErrCanceled) {
		t.Fatal(e)
	}
	ctx, cancel = context.WithTimeout(testCtx, time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if _, e := v.Cypher(ctx, "MATCH (n) RETURN n", nil); !errors.Is(e, ErrCanceled) {
		t.Fatal(e)
	}
}

func TestIntegerPrecision(t *testing.T) {
	s, dir := openTest(t)
	op := createNode("a", "integer")
	const large int64 = 9007199254740993
	op.Node.Properties["large"] = large
	applyTest(t, s, op)
	s.Close()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	v := viewTest(t, s, Basis{ValidAt: start})
	nodes, e := v.Nodes().All(testCtx)
	if e != nil || nodes[0].Properties["large"] != large {
		t.Fatal(nodes, e)
	}
	r, e := v.Cypher(testCtx, "MATCH (n) WHERE n.large=$number RETURN n.large AS number", Properties{"number": large})
	if e != nil || len(r.Rows) != 1 || r.Rows[0]["number"] != large {
		t.Fatal(r, e)
	}
	bad := createNode("bad", "overflow")
	bad.Node.Properties["number"] = uint64(18446744073709551615)
	if _, e = s.Apply(testCtx, "tender", Batch{[]Operation{bad}}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
func TestMultiGraphFanOut(t *testing.T) {
	s, _ := openTest(t)
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
	r, e := v.Cypher(testCtx, "MATCH (n) RETURN count(n) AS count", nil)
	if e != nil || len(r.Rows) != 2 || len(r.Snapshots) != 2 {
		t.Fatal(r, e)
	}
	for _, row := range r.Rows {
		if row["count"] != int64(1) {
			t.Fatal(r)
		}
	}
}

func TestEntityResultsHaveStableQualifiedIdentity(t *testing.T) {
	s, _ := openTest(t)
	applyTest(t, s, createNode("stable-a", "A"), createNode("stable-b", "B"), Operation{Kind: "create", ID: "stable-edge", Valid: Interval{From: start}, Relationship: &Relationship{From: "stable-a", To: "stable-b", Type: "LINK"}, Reason: "import"})
	v := viewTest(t, s, Basis{ValidAt: start})
	r, e := v.Cypher(testCtx, "MATCH (a)-[r]->(b) RETURN a,r,b", nil)
	if e != nil {
		t.Fatal(e)
	}
	node, ok := r.Rows[0]["a"].(map[string]any)
	if !ok || node["id"] != "stable-a" || node["graph"] != "tender" {
		t.Fatal(r)
	}
	edge, ok := r.Rows[0]["r"].(map[string]any)
	if !ok || edge["id"] != "stable-edge" || edge["from"] != "stable-a" || edge["graph"] != "tender" {
		t.Fatal(r)
	}
}
func TestSplitFragmentsExposeLatestIdentityRevision(t *testing.T) {
	s, _ := openTest(t)
	rev := applyTest(t, s, createNode("a", "old"))
	op := createNode("a", "new")
	op.Kind = "correct"
	op.Expected = rev
	op.Valid.From = instant("2026-10-18T00:00:00Z")
	rev = applyTest(t, s, op)
	nodes, e := viewTest(t, s, Basis{ValidAt: start}).Nodes().All(testCtx)
	if e != nil || nodes[0].Revision != rev {
		t.Fatal(nodes, e)
	}
	old := viewTest(t, s, Basis{ValidAt: start, Token: 1})
	nodes, e = old.Nodes().All(testCtx)
	if e != nil || nodes[0].Revision != 1 {
		t.Fatal(nodes, e)
	}
}

func TestOrdinaryMapsAreNeverQualified(t *testing.T) {
	s, _ := openTest(t)
	op := createNode("a", "A")
	op.Node.Properties["nested"] = map[string]any{"properties": map[string]any{"_cuttle_id": "a"}, "keep": true}
	applyTest(t, s, op)
	v := viewTest(t, s, Basis{ValidAt: start})
	for _, q := range []string{"RETURN {properties:{_cuttle_id:'a'},keep:true} AS value", "WITH {properties:{_cuttle_id:'a'},keep:true} AS value RETURN value", "MATCH (n) RETURN n.nested AS value"} {
		r, e := v.Cypher(testCtx, q, nil)
		if e != nil {
			t.Fatal(q, e)
		}
		m, ok := r.Rows[0]["value"].(map[string]any)
		if !ok || m["keep"] != true || len(m) != 2 {
			t.Fatal(q, r)
		}
	}
	for _, q := range []string{"RETURN {_cuttle_kind:'node',_cuttle_value:{properties:{_cuttle_id:'a'}},keep:true} AS value", "RETURN $value AS value"} {
		params := Properties{"value": map[string]any{"_cuttle_kind": "node", "_cuttle_value": map[string]any{"properties": map[string]any{"_cuttle_id": "a"}}, "keep": true}}
		r, e := v.Cypher(testCtx, q, params)
		if e != nil {
			t.Fatal(e)
		}
		m := r.Rows[0]["value"].(map[string]any)
		if m["keep"] != true || m["_cuttle_kind"] != "node" {
			t.Fatal(r)
		}
	}
	r, e := v.Cypher(testCtx, "MATCH (n) RETURN properties(n) AS value,n AS entity", nil)
	if e != nil {
		t.Fatal(e)
	}
	props := r.Rows[0]["value"].(map[string]any)
	if props["name"] != "A" || props["_cuttle_id"] != "a" {
		t.Fatal(r)
	}
	if r.Rows[0]["entity"].(map[string]any)["id"] != "a" {
		t.Fatal(r)
	}
}
func TestInvalidTextAndNullEqualityParity(t *testing.T) {
	s, _ := openTest(t)
	a := createNode("a", "explicit")
	a.Node.Properties["nullable"] = nil
	applyTest(t, s, a, createNode("b", "absent"))
	v := viewTest(t, s, Basis{ValidAt: start})
	for _, q := range []string{"RETURN 1 AS value\x00 CREATE (:Unexpected)", "RETURN 'bad\xff' AS value"} {
		if _, e := v.Cypher(testCtx, q, nil); !errors.Is(e, ErrInvalid) {
			t.Fatal(q, e)
		}
	}
	nodes, e := v.Nodes().Where("nullable", nil).All(testCtx)
	if e != nil || len(nodes) != 0 {
		t.Fatal(nodes, e)
	}
	r, e := v.Cypher(testCtx, "MATCH (n) WHERE n.nullable=$value RETURN n", Properties{"value": nil})
	if e != nil || len(r.Rows) != 0 {
		t.Fatal(r, e)
	}
}
func TestIndependentByteLimits(t *testing.T) {
	s, _ := openTest(t)
	v := viewTest(t, s, Basis{ValidAt: start})
	q := "RETURN 1 AS value"
	s.options.MaxRows = 100
	s.options.MaxQueryBytes = len(q)
	if _, e := v.Cypher(testCtx, q, nil); e != nil {
		t.Fatal(e)
	}
	s.options.MaxQueryBytes = len(q) - 1
	if _, e := v.Cypher(testCtx, q, nil); !errors.Is(e, ErrCapacity) {
		t.Fatal(e)
	}
	s.options.MaxQueryBytes = 1000
	s.options.MaxBytes = 512
	params := Properties{"unused": strings.Repeat("x", 499)}
	raw, _ := json.Marshal(params)
	if len(raw) != 512 {
		t.Fatal(len(raw))
	}
	if _, e := v.Cypher(testCtx, q, params); e != nil {
		t.Fatal(e)
	}
	params["unused"] = strings.Repeat("x", 500)
	if _, e := v.Cypher(testCtx, q, params); !errors.Is(e, ErrCapacity) {
		t.Fatal(e)
	}
	s.options.MaxBytes = 4096
	r, e := v.Cypher(testCtx, q, nil)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ = json.Marshal(r)
	s.options.MaxBytes = len(raw)
	if _, e = v.Cypher(testCtx, q, nil); e != nil {
		t.Fatal(e)
	}
	s.options.MaxBytes = len(raw) - 1
	if _, e = v.Cypher(testCtx, q, nil); !errors.Is(e, ErrCapacity) {
		t.Fatal(e)
	}
}
func TestConfiguredQueryTimeoutWithLiveParent(t *testing.T) {
	s, _ := openTest(t, Options{QueryTimeout: 20 * time.Millisecond})
	ops := make([]Operation, 70)
	for i := range ops {
		ops[i] = createNode(ID(fmt.Sprintf("n%d", i)), "load")
	}
	applyTest(t, s, ops...)
	v := viewTest(t, s, Basis{ValidAt: start})
	ctx, cancel := context.WithTimeout(testCtx, 5*time.Second)
	defer cancel()
	started := time.Now()
	_, e := v.Cypher(ctx, "MATCH (a),(b),(c),(d) RETURN count(a) AS total", nil)
	if !errors.Is(e, ErrCanceled) || !errors.Is(e, context.DeadlineExceeded) || ctx.Err() != nil || time.Since(started) > time.Second {
		t.Fatal(e, ctx.Err(), time.Since(started))
	}
}

func TestNestedEntityResultsAndReservedDiscriminator(t *testing.T) {
	s, _ := openTest(t)
	applyTest(t, s, createNode("a", "A"))
	v := viewTest(t, s, Basis{ValidAt: start})
	for _, q := range []string{"MATCH (n) RETURN collect(n) AS value", "MATCH (n) RETURN [n] AS value", "MATCH (n) RETURN {entity:n} AS value"} {
		r, e := v.Cypher(testCtx, q, nil)
		if e != nil {
			t.Fatal(q, e)
		}
		var entity map[string]any
		switch x := r.Rows[0]["value"].(type) {
		case []any:
			if len(x) != 1 {
				t.Fatal(q, r)
			}
			entity = x[0].(map[string]any)
		case map[string]any:
			entity = x["entity"].(map[string]any)
		default:
			t.Fatal(q, r)
		}
		if entity["id"] != "a" || entity["graph"] != "tender" {
			t.Fatal(q, r)
		}
	}
	if _, e := v.Cypher(testCtx, "RETURN {__cuttle_entity_kind:'node',properties:{_cuttle_id:'a'}} AS value", nil); !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
	bad := map[string][]string{"__cuttle_entity_kind": {"node"}}
	if _, e := v.Cypher(testCtx, "RETURN $value AS value", Properties{"value": bad}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	op := createNode("bad", "bad")
	op.Node.Properties["nested"] = bad
	if _, e := s.Apply(testCtx, "tender", Batch{[]Operation{op}}); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}

func TestReservedOutputGraphColumn(t *testing.T) {
	s, _ := openTest(t)
	v := viewTest(t, s, Basis{ValidAt: start})
	if _, e := v.Cypher(testCtx, "RETURN 'caller' AS _cuttle_graph", nil); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}

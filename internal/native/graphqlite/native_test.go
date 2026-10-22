package graphqlite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeGate(t *testing.T) {
	ctx := context.Background()
	d, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	run := func(q string, a ...any) []map[string]any {
		t.Helper()
		r, e := d.Run(ctx, q, a...)
		if e != nil {
			t.Fatal(q, e)
		}
		return r
	}
	run("BEGIN")
	run("SELECT cypher(?)", "CREATE (:Person {name:'rolled', rollback_key:1})")
	run("ROLLBACK")
	run("SELECT cypher(?)", "CREATE (:Person {name:'Alice', rollback_key:2})")
	r := run("SELECT cypher(?,?) AS result", "MATCH (n:Person) WHERE n.name=$name RETURN n.name AS name", `{"name":"Alice"}`)
	var rows []map[string]any
	if e = json.Unmarshal([]byte(r[0]["result"].(string)), &rows); e != nil || len(rows) != 1 || rows[0]["name"] != "Alice" {
		t.Fatalf("parameters/rollback: %v %v", r, e)
	}
	for _, q := range []string{"CREATE (:X)", "PROFILE CREATE (:X)", "CALL { CREATE (:X) RETURN 1 } RETURN 1", "FOREACH (x IN [1] | CREATE (:X))", "LOAD CSV FROM 'x' AS row RETURN row", "MATCH (n) WHERE EXISTS { CREATE (:X) RETURN 1 } RETURN n", "RETURN readfile('x')"} {
		if ValidateRead(q) == nil {
			t.Errorf("policy accepted %s", q)
		}
	}
	if e = ValidateRead("MATCH (n:Person) WHERE n.name=$name RETURN n.name AS name"); e != nil {
		t.Fatal(e)
	}
	run("PRAGMA query_only=ON")
	if _, e = d.Run(ctx, "SELECT cypher(?)", "CREATE (:X)"); e == nil {
		t.Fatal("native restriction allowed mutation")
	}
	run("PRAGMA query_only=OFF")
	path := filepath.Join(t.TempDir(), "other.db")
	other, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = other.Run(ctx, "SELECT cypher(?)", "CREATE (:Person {name:'Bob'})"); e != nil {
		t.Fatal(e)
	}
	other.Close()
	run("ATTACH DATABASE ? AS other", path)
	r = run("SELECT cypher(?) AS result", "MATCH (n:Person) FROM other RETURN n.name AS name")
	if !strings.Contains(r[0]["result"].(string), "Bob") {
		t.Fatal(r)
	}
	cancelCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, e = d.Run(cancelCtx, "WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1000000000) SELECT sum(x) FROM n")
	if e != context.DeadlineExceeded || time.Since(start) > time.Second {
		t.Fatalf("cancellation %v %v", e, time.Since(start))
	}
	for i := 0; i < 120; i++ {
		run("SELECT cypher(?)", "CREATE (:Load {name:'load'})")
	}
	graphCtx, graphCancel := context.WithTimeout(ctx, 5*time.Millisecond)
	graphStart := time.Now()
	_, graphErr := d.Run(graphCtx, "SELECT cypher(?)", "MATCH (a),(b),(c),(d) RETURN count(a) AS count")
	graphCancel()
	if graphErr != context.DeadlineExceeded || time.Since(graphStart) > time.Second {
		t.Fatalf("graph cancellation %v %v", graphErr, time.Since(graphStart))
	}

	run("SELECT cypher(?)", "MATCH (n) RETURN count(n) AS count")
}

func TestOpenFailureAndExistingDoesNotCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "graph.db")
	for i := 0; i < 20; i++ {
		if d, e := Open(path); e == nil {
			d.Close()
			t.Fatal("opened missing parent")
		}
	}
	path = filepath.Join(t.TempDir(), "absent.db")
	if d, e := OpenExisting(path); e == nil {
		d.Close()
		t.Fatal("opened missing graph")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("missing graph was recreated", e)
	}
}

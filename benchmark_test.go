package cuttle

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func benchmarkBatch() Batch {
	ops := make([]Operation, 100)
	for i := range ops {
		ops[i] = createNode(ID(fmt.Sprintf("requirement-%03d", i)), fmt.Sprintf("Requirement %03d", i))
	}
	return Batch{ops}
}
func BenchmarkProjectionQuery100(b *testing.B) {
	s, e := Open(b.TempDir())
	if e != nil {
		b.Fatal(e)
	}
	defer s.Close()
	if e = s.CreateGraph(testCtx, "tender"); e != nil {
		b.Fatal(e)
	}
	if _, e = s.Apply(testCtx, "tender", benchmarkBatch()); e != nil {
		b.Fatal(e)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, e := s.View(testCtx, []GraphID{"tender"}, Basis{ValidAt: start})
		if e != nil {
			b.Fatal(e)
		}
		r, e := v.Cypher(testCtx, "MATCH (n:Requirement) RETURN count(n) AS count", nil)
		if e != nil || r.Rows[0]["count"] != int64(100) {
			b.Fatal(r, e)
		}
	}
}
func BenchmarkHistoryAppendReplay100(b *testing.B) {
	root := b.TempDir()
	var bytes int64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := filepath.Join(root, fmt.Sprintf("run-%d", i))
		s, e := Open(dir)
		if e != nil {
			b.Fatal(e)
		}
		if e = s.CreateGraph(testCtx, "tender"); e != nil {
			b.Fatal(e)
		}
		rev, e := s.Apply(testCtx, "tender", benchmarkBatch())
		if e != nil {
			b.Fatal(e)
		}
		for j := 0; j < 10; j++ {
			op := createNode("requirement-000", fmt.Sprintf("revision-%d", j))
			op.Kind = "correct"
			op.Expected = rev
			rev, e = s.Apply(testCtx, "tender", Batch{[]Operation{op}})
			if e != nil {
				b.Fatal(e)
			}
		}
		v, e := s.View(testCtx, []GraphID{"tender"}, Basis{ValidAt: start})
		if e != nil {
			b.Fatal(e)
		}
		nodes, e := v.Nodes().All(testCtx)
		if e != nil || len(nodes) != 100 {
			b.Fatal(e)
		}
		s.Close()
		files, _ := filepath.Glob(filepath.Join(dir, "*.cuttle"))
		for _, f := range files {
			st, e := os.Stat(f)
			if e != nil {
				b.Fatal(e)
			}
			bytes += st.Size()
		}
	}
	b.ReportMetric(float64(bytes)/float64(b.N), "file-bytes/op")
}

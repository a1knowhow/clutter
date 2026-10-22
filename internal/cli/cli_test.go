package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluationCommands(t *testing.T) {
	dir := t.TempDir()
	run := func(want int, args ...string) map[string]any {
		t.Helper()
		var out, errOut bytes.Buffer
		code := Execute(context.Background(), append([]string{"--dir", dir}, args...), strings.NewReader(""), &out, &errOut)
		if code != want {
			t.Fatalf("%v exit %d: %s %s", args, code, out.String(), errOut.String())
		}
		data := out.Bytes()
		if code != 0 {
			data = errOut.Bytes()
		}
		var v map[string]any
		if e := json.Unmarshal(data, &v); e != nil {
			t.Fatalf("%v: %s %s", e, out.String(), errOut.String())
		}
		return v
	}
	for _, example := range []struct{ graph, label string }{{"tender", "Requirement"}, {"storybrand", "Pain"}} {
		run(0, "graph", "create", example.graph)
		run(0, "import", example.graph, "--file", filepath.Join("..", "..", "examples", example.graph, "batch.json"))
		result := run(0, "query", "--graphs", example.graph, "--valid-at", "2026-10-15T00:00:00Z", "MATCH (n:"+example.label+") RETURN n.text AS text")
		if len(result["rows"].([]any)) != 1 {
			t.Fatal(result)
		}
		history := run(0, "history", example.graph)
		if len(history["history"].([]any)) != 3 {
			t.Fatal(history)
		}
	}
	if got := run(1, "query", "--graphs", "tender", "CREATE (:Bad)")["code"]; got != "unsupported" {
		t.Fatal(got)
	}
	if got := run(1, "query", "MATCH (n) RETURN n")["code"]; got != "invalid" {
		t.Fatal(got)
	}
	if got := run(1, "graph", "create", "tender")["code"]; got != "exists" {
		t.Fatal(got)
	}
	if got := run(1, "query", "--graphs", "missing", "MATCH (n) RETURN n")["code"]; got != "not_found" {
		t.Fatal(got)
	}
	if got := run(1, "query", "--graphs", "tender", "--known-at", "2026-10-22T00:00:00Z", "--token", "1", "RETURN 1")["code"]; got != "invalid" {
		t.Fatal(got)
	}
	file := filepath.Join(t.TempDir(), "invalid.json")
	if e := os.WriteFile(file, []byte(`{"operations":[],"typo":1}`), 0600); e != nil {
		t.Fatal(e)
	}
	if got := run(1, "import", "tender", "--file", file)["code"]; got != "invalid" {
		t.Fatal(got)
	}
	if ids := run(0, "graph", "list")["graphs"].([]any); len(ids) != 2 {
		t.Fatal(ids)
	}
	run(0, "graph", "delete", "tender")
	run(1, "history", "tender")
}
func TestImportStandardInputAndRollback(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	args := []string{"--dir", dir, "graph", "create", "g"}
	if Execute(context.Background(), args, nil, &out, &errOut) != 0 {
		t.Fatal(errOut.String())
	}
	out.Reset()
	errOut.Reset()
	batch := `{"operations":[{"kind":"create","id":"edge","reason":"invalid endpoints","valid":{"from":"2026-10-01T00:00:00Z"},"relationship":{"from":"missing","to":"also-missing","type":"LINK"}}]}`
	if Execute(context.Background(), []string{"--dir", dir, "import", "g"}, strings.NewReader(batch), &out, &errOut) != 1 || !strings.Contains(errOut.String(), "stranded_relationship") {
		t.Fatal(out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if Execute(context.Background(), []string{"--dir", dir, "history", "g"}, nil, &out, &errOut) != 0 || !strings.Contains(out.String(), `"history":[]`) {
		t.Fatal(out.String(), errOut.String())
	}
}

func TestLargeIntegerRoundTrip(t *testing.T) {
	directory := t.TempDir()
	var out, errOut bytes.Buffer
	execute := func(input string, args ...string) {
		t.Helper()
		out.Reset()
		errOut.Reset()
		if code := Execute(context.Background(), append([]string{"--dir", directory}, args...), strings.NewReader(input), &out, &errOut); code != 0 {
			t.Fatalf("%v: %s", args, errOut.String())
		}
	}
	execute("", "graph", "create", "numbers")
	execute(`{"operations":[{"kind":"create","id":"large","reason":"preserve exact integer","valid":{"from":"2026-10-01T00:00:00Z"},"node":{"labels":["Number"],"properties":{"value":9007199254740993}}}]}`, "import", "numbers")
	execute("", "query", "--graphs", "numbers", "--params", `{"value":9007199254740993}`, "MATCH (n:Number) WHERE n.value=$value RETURN n.value AS value")
	if !strings.Contains(out.String(), `"value":9007199254740993`) {
		t.Fatal(out.String())
	}
}

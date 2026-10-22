package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/a1knowhow/cuttle"
)

//go:embed batch.json
var batchJSON []byte

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	ctx := context.Background()
	directory := ".cuttle-storybrand"
	if len(os.Args) > 1 {
		directory = os.Args[1]
	}
	store, e := cuttle.Open(directory)
	if e != nil {
		return e
	}
	defer store.Close()
	if e = store.CreateGraph(ctx, "storybrand"); e != nil && !errors.Is(e, cuttle.ErrExists) {
		return e
	}
	history, e := store.History(ctx, "storybrand", "")
	if e != nil {
		return e
	}
	if len(history) == 0 {
		var batch cuttle.Batch
		if e = json.Unmarshal(batchJSON, &batch); e != nil {
			return e
		}
		if _, e = store.Apply(ctx, "storybrand", batch); e != nil {
			return e
		}
	}
	view, e := store.View(ctx, []cuttle.GraphID{"storybrand"}, cuttle.Basis{})
	if e != nil {
		return e
	}
	nodes, e := view.Nodes().Label("Pain").All(ctx)
	if e != nil {
		return e
	}
	rows, e := view.Cypher(ctx, "MATCH (n:Pain) RETURN n.text AS text, n._cuttle_id AS id, n._cuttle_evidence AS evidence", nil)
	if e != nil {
		return e
	}
	if len(nodes) != len(rows.Rows) {
		return fmt.Errorf("fluent/Cypher count mismatch")
	}
	for i, n := range nodes {
		if rows.Rows[i]["text"] != n.Properties["text"] || rows.Rows[i]["id"] != string(n.ID) {
			return fmt.Errorf("fluent/Cypher value mismatch")
		}
		encoded, e := json.Marshal(rows.Rows[i]["evidence"])
		if e != nil {
			return e
		}
		var evidence []cuttle.Evidence
		if e = json.Unmarshal(encoded, &evidence); e != nil {
			return e
		}
		if !reflect.DeepEqual(evidence, n.Evidence) {
			return fmt.Errorf("fluent/Cypher evidence mismatch")
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"fluent": nodes, "cypher": rows})
}

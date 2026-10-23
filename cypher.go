package cuttle

import (
	"context"
	"encoding/json"
	"fmt"
	native "github.com/a1knowhow/cuttle/internal/native/graphqlite"
	"sort"
	"strings"
)

func identifier(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }
func propertyMap(p Properties) (string, string, error) {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fields := []string{}
	params := Properties{}
	for i, k := range keys {
		name := fmt.Sprintf("p%d", i)
		fields = append(fields, identifier(k)+":$"+name)
		params[name] = p[k]
	}
	b, e := json.Marshal(params)
	return "{" + strings.Join(fields, ",") + "}", string(b), e
}
func projection(ctx context.Context, g GraphID, nodes []Node, rels []Relationship) (*native.DB, error) {
	d, e := native.Open(":memory:")
	if e != nil {
		return nil, e
	}
	fail := func(e error) (*native.DB, error) { d.Close(); return nil, e }
	if _, e = d.Run(ctx, "BEGIN"); e != nil {
		return fail(e)
	}
	for _, n := range nodes {
		if n.Graph != g {
			continue
		}
		p := Properties{}
		for k, v := range n.Properties {
			p[k] = v
		}
		p["_cuttle_graph"] = string(g)
		p["_cuttle_id"] = string(n.ID)
		p["_cuttle_revision"] = uint64(n.Revision)
		p["_cuttle_evidence"] = n.Evidence
		props, params, e := propertyMap(p)
		if e != nil {
			return fail(e)
		}
		labels := []string{}
		for _, l := range n.Labels {
			labels = append(labels, identifier(l))
		}
		if _, e = d.Run(ctx, "SELECT cypher(?,?)", "CREATE (:"+strings.Join(labels, ":")+" "+props+")", params); e != nil {
			return fail(e)
		}
	}
	for _, r := range rels {
		if r.Graph != g {
			continue
		}
		p := Properties{}
		for k, v := range r.Properties {
			p[k] = v
		}
		p["_cuttle_graph"] = string(g)
		p["_cuttle_id"] = string(r.ID)
		p["_cuttle_revision"] = uint64(r.Revision)
		p["_cuttle_evidence"] = r.Evidence
		props, raw, e := propertyMap(p)
		if e != nil {
			return fail(e)
		}
		var params Properties
		decodeJSON([]byte(raw), &params)
		params["from"] = r.From
		params["to"] = r.To
		b, _ := json.Marshal(params)
		q := "MATCH (a),(b) WHERE a._cuttle_id=$from AND b._cuttle_id=$to CREATE (a)-[:" + identifier(r.Type) + " " + props + "]->(b)"
		if _, e = d.Run(ctx, "SELECT cypher(?,?)", q, string(b)); e != nil {
			return fail(e)
		}
	}
	if _, e = d.Run(ctx, "COMMIT"); e != nil {
		return fail(e)
	}
	if _, e = d.Run(ctx, "PRAGMA query_only=ON"); e != nil {
		return fail(e)
	}
	return d, nil
}

// Cypher executes the documented read subset independently over each selected graph.
// Every row includes _cuttle_graph; node/relationship properties carry qualified identities and evidence.
func (v *View) Cypher(ctx context.Context, text string, params Properties) (Result, error) {
	if e := v.check(ctx); e != nil {
		return Result{}, e
	}
	if !textValid(text) {
		return Result{}, fmt.Errorf("%w: query contains invalid text", ErrInvalid)
	}
	if e := validateJSONText(params, 0); e != nil {
		return Result{}, e
	}
	o := v.store.options
	if len(text) > o.MaxQueryBytes {
		return Result{}, ErrCapacity
	}
	if e := native.ValidateRead(text); e != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrUnsupported, e)
	}
	b, e := json.Marshal(params)
	if e != nil {
		return Result{}, fmt.Errorf("%w: parameters", ErrInvalid)
	}
	var checkedParams Properties
	if e = decodeJSON(b, &checkedParams); e != nil {
		return Result{}, fmt.Errorf("%w: parameters", ErrInvalid)
	}
	if e = validateJSONText(checkedParams, 0); e != nil {
		return Result{}, e
	}
	if _, e = normalizeNumbers(checkedParams); e != nil {
		return Result{}, e
	}
	if len(b) > o.MaxBytes {
		return Result{}, ErrCapacity
	}
	ctx, cancel := context.WithTimeout(ctx, o.QueryTimeout)
	defer cancel()
	result := Result{Rows: []map[string]any{}, Snapshots: v.Snapshots()}
	for _, snap := range v.snapshots {
		d, e := projection(ctx, snap.Graph, v.nodes, v.relationships)
		if e != nil {
			return Result{}, canceled(e)
		}
		rows, e := d.Run(ctx, "SELECT cypher(?,?) AS result", text, string(b))
		d.Close()
		if e != nil {
			return Result{}, canceled(e)
		}
		var parsed []map[string]any
		if len(rows) != 1 {
			return Result{}, fmt.Errorf("%w: backend output", ErrUnsupported)
		}
		raw, ok := rows[0]["result"].(string)
		if !ok {
			return Result{}, fmt.Errorf("%w: backend result format", ErrUnsupported)
		}
		if e = decodeJSON([]byte(raw), &parsed); e != nil {
			return Result{}, e
		}
		for _, row := range parsed {
			if _, exists := row["_cuttle_graph"]; exists {
				return Result{}, fmt.Errorf("%w: _cuttle_graph is a reserved output column", ErrInvalid)
			}
			if _, e = normalizeNumbers(row); e != nil {
				return Result{}, e
			}
			for key, value := range row {
				row[key] = v.qualify(value, snap.Graph)
			}
			row["_cuttle_graph"] = string(snap.Graph)
			result.Rows = append(result.Rows, row)
		}
		if len(result.Rows) > o.MaxRows {
			return Result{}, ErrCapacity
		}
	}
	out, e := json.Marshal(result)
	if e != nil {
		return Result{}, e
	}
	if len(out) > o.MaxBytes {
		return Result{}, ErrCapacity
	}
	if e := ctx.Err(); e != nil {
		return Result{}, canceled(e)
	}
	return result, nil
}

// Replace projection-local engine entities with the public stable identity values.
func (v *View) qualify(value any, g GraphID) any {
	switch x := value.(type) {
	case map[string]any:
		kind, _ := x["__cuttle_entity_kind"].(string)
		if kind == "node" || kind == "relationship" {
			if props, ok := x["properties"].(map[string]any); ok {
				if id, ok := props["_cuttle_id"].(string); ok {
					if kind == "node" {
						for _, n := range v.nodes {
							if n.Graph == g && n.ID == ID(id) {
								raw, _ := json.Marshal(n)
								var out map[string]any
								decodeJSON(raw, &out)
								normalizeNumbers(out)
								return out
							}
						}
					}
					if kind == "relationship" {
						for _, r := range v.relationships {
							if r.Graph == g && r.ID == ID(id) {
								raw, _ := json.Marshal(r)
								var out map[string]any
								decodeJSON(raw, &out)
								normalizeNumbers(out)
								return out
							}
						}
					}
				}
			}
		}
		for k, item := range x {
			x[k] = v.qualify(item, g)
		}
		return x
	case []any:
		for i, item := range x {
			x[i] = v.qualify(item, g)
		}
		return x
	}
	return value
}

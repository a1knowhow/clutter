# Cuttle

Cuttle is an embedded Go knowledge graph library with two timelines, evidence, a fluent API , read-only parameterized Cypher, and a CLI. It stores generic nodes and relationships. Callers supply extracted facts.

## Build

- Use the Go version declared in `go.mod`, a C compiler, and Go’s C bridge enabled.
- GraphQLite 0.12.0 and its generated parser compile into the executable. The pinned `go-sqlite3` dependency bundles SQLite. No graph service, external extension file, Bison, Flex, or separately installed SQLite is required at runtime.
- Native integration is tested on macOS with an ARM64 (64-bit Arm) processor. Other targets need verification.

```sh
CGO_ENABLED=1 go build -o /tmp/cuttle ./cmd/cuttle
go test ./...
go test -race ./...
go vet ./...
```

The race detector checks Go concurrency; it does not prove C memory safety. Upstream source, generated parser, commit pin and license notices are under `internal/native/graphqlite/`.

## Evaluate the examples

Run from the repository root. Every invocation reopens the same durable graph files.

```sh
/tmp/cuttle --dir /tmp/cuttle-demo graph create tender
/tmp/cuttle --dir /tmp/cuttle-demo import tender --file examples/tender/batch.json
/tmp/cuttle --dir /tmp/cuttle-demo query --graphs tender \
  --params '{"priority":"mandatory"}' \
  'MATCH (n:Requirement) WHERE n.priority=$priority RETURN n.text AS text, n._cuttle_id AS id, n._cuttle_evidence AS evidence'
/tmp/cuttle --dir /tmp/cuttle-demo history tender requirement-1

/tmp/cuttle --dir /tmp/cuttle-demo graph create storybrand
/tmp/cuttle --dir /tmp/cuttle-demo import storybrand --file examples/storybrand/batch.json
/tmp/cuttle --dir /tmp/cuttle-demo query --graphs storybrand \
  'MATCH (s:SoundBite)-[:ADDRESSES]->(p:Pain) RETURN s.text AS message, p.text AS pain'
/tmp/cuttle --dir /tmp/cuttle-demo history storybrand
/tmp/cuttle --dir /tmp/cuttle-demo graph list
```

The runnable Go examples import once, reopen safely, compare fluent and Cypher results, and output evidence through both interfaces:

```sh
go run ./examples/tender /tmp/cuttle-tender | jq
go run ./examples/storybrand /tmp/cuttle-storybrand | jq
```

Batch input and command output use JSON (JavaScript Object Notation). Signed 64-bit integer values retain their exact values, including values beyond floating-point precision; oversized integer input is rejected. Labels must be unique and nonempty; text cannot contain a zero byte; nested properties are limited to 64 levels. Evidence entries require a source and source revision, and property-specific evidence must name an existing property. Omit `--file` or use `--file -` to read standard input. Errors go to standard error as an object with `code` and `error`; failures return a nonzero exit status.

## Go usage

```go
store, err := cuttle.Open("./knowledge")
if err != nil { return err }
defer store.Close()

// Select graph identities explicitly. A view captures its basis once.
view, err := store.View(ctx, []cuttle.GraphID{"tender"}, cuttle.Basis{})
if err != nil { return err }
requirements, err := view.Nodes().Label("Requirement").
    Where("priority", "mandatory").All(ctx)
if err != nil { return err }
result, err := view.Cypher(ctx,
    "MATCH (n:Requirement) WHERE n.priority=$priority RETURN n.text AS text",
    cuttle.Properties{"priority": "mandatory"})
```

`View.Relationships().Type("SATISFIES").From("capability-1").All(ctx)` provides fluent relationship selection. Node and relationship values include graph-qualified identities, revisions, validity and evidence.

Configure limits and a caller-owned implementation of the narrow `Storage` contract with `cuttle.Options`. Cuttle supplies the default SQLite-backed implementation. Reader, writer, query and lifecycle contracts are in `interfaces.go`; portable errors support `errors.Is`.

## Versions and time

- A valid interval says when a fact applies. It is half-open: `[from, to)`. An omitted end is unbounded; a start is required.
- Recorded time says when Cuttle learned the fact. Every committed batch gets an ordered token and recorded timestamp. Content in earlier commits remains available.
- A view defaults to one captured valid-now instant and the latest known commit for each graph. Set `Basis.ValidAt` and either `Basis.KnownAt` or `Basis.Token` for historical reads. The fluent and Cypher interfaces use that same view.
- A correction keeps the assertion identity, requires its current revision in `expected`, and supplies a reason, interval and complete replacement snapshot. Unaffected valid intervals survive. Omitted properties and evidence are removed from the replacement.
- A dated amendment uses a replacement interval starting on its effective date. A correction of an earlier extraction error starts on the original effective date. Earlier recorded snapshots preserve what was believed before either change.
- A retraction hides the assertion over its selected valid interval. History retains the assertion and an explicit retraction record. Stale revisions change nothing.
- Relationships require visible endpoints throughout their valid intervals. Retract affected relationships explicitly in the same batch when removing their endpoints. Relationship endpoints and type are fixed for their identity.
- To remove a source support, correct the snapshot with the remaining evidence. Removing the final support leaves the assertion visible until callers explicitly retract it. Cuttle does not infer that unsupported means false.

Example correction after importing the tender batch:

```json
{
  "operations": [{
    "kind": "correct",
    "id": "requirement-1",
    "expected": 1,
    "reason": "Correct extraction against the source",
    "valid": {"from": "2026-10-01T00:00:00Z"},
    "node": {
      "labels": ["Requirement"],
      "properties": {"text": "Encrypt stored customer and employee data", "priority": "mandatory"},
      "evidence": [{"source": "tender-document", "revision": "v1", "locator": "section 4.2", "property": "text"}]
    }
  }]
}
```

Use `query --valid-at 2026-10-15T00:00:00Z --token 1 --graphs tender ...` to see the original import. Use the same valid instant without `--token` to see the latest corrected belief. `--known-at` accepts a recorded timestamp and is mutually exclusive with `--token`.

## Supported Cypher

The deliberately bounded GraphQLite read subset supports ordinary node/relationship `MATCH`, `OPTIONAL MATCH`, property and label filters, bound parameters, `WITH`, `RETURN`, ordering, limits, `UNION`, basic expressions, and explicitly allowed scalar/aggregate functions. Consult `internal/native/graphqlite/policy.c` for the exact parser allowlist.

- Queries are parsed before execution. Every unapproved syntax tree node is rejected, including mutations, schema changes, procedures, nested subqueries, file loading, `FOREACH`, `UNWIND`, `EXPLAIN`, `PROFILE`, `FROM`, and variable-length paths. Unsupported features return `ErrUnsupported`.
- The derived graph projection uses a dedicated connection with SQLite `query_only` enabled. Caller Cypher cannot write history or graph data.
- Properties beginning `_cuttle_` are reserved. Raw node and relationship results are converted to the same stable, graph-qualified values as fluent reads. The engine’s projection-local `id()` function is unsupported. Projected entities expose `_cuttle_graph`, `_cuttle_id`, `_cuttle_revision`, and `_cuttle_evidence`. Select these explicitly when returning scalar properties. Every result row also includes `_cuttle_graph`.
- The internal map key `__cuttle_entity_kind` is reserved at every nesting level in stored properties, parameters and Cypher map literals. Cuttle uses it to distinguish native entity results from ordinary maps, including entities inside collections.
- The output column `_cuttle_graph` is reserved for graph metadata; queries returning that column name fail with `ErrInvalid`.
- Multi-graph reads require an explicit graph list, for example `--graphs tender,storybrand`. The query runs independently over each selected graph; aggregation, ordering and limits apply separately within each graph. Results include a separate snapshot token and recorded timestamp for each file. There are no cross-graph edges, joins, or atomic multi-file transactions.
- Default limits are 10 selected graphs, 1,000 output rows, 4 megabytes of serialized output, 10,000 selected entities, 65,536 query bytes, and a five-second query timeout. These are configurable through Go options. Exceeding a limit returns `ErrCapacity`.
- Cancellation is verified through SQLite interruption for supported reads. The engine materializes results; row/output limits do not establish a general bound on native work or peak memory. Native graph algorithms are excluded from the query allowlist.

The default storage opens bounded connections per operation and does not keep every graph’s adjacency cache resident. Graph count is not a node-count capacity estimate. Large-graph performance remains unmeasured.

## Graph lifecycle

`graph create`, `graph list`, `import`, `query`, `history`, and `capabilities` support evaluation. `graph delete GRAPH` permanently removes the entire graph and its history. Assertion retraction preserves history; selective physical history purge is deferred.

Extraction, model calls, embeddings, identity resolution, permissions and PocketRAG integration are outside this library.

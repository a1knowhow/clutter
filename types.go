// Package cuttle provides embedded, bitemporal knowledge graphs.
package cuttle

import "time"

type GraphID string
type ID string
type Revision uint64
type Properties map[string]any

// Identity is qualified by graph; identifiers never imply cross-graph edges.
type Identity struct {
	Graph GraphID `json:"graph"`
	ID    ID      `json:"id"`
}

// Interval is half-open [From, To). A zero To means unbounded.
type Interval struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to,omitempty"`
}
type Evidence struct {
	Source   string `json:"source"`
	Revision string `json:"revision"`
	Locator  string `json:"locator,omitempty"`
	Property string `json:"property,omitempty"`
	Activity string `json:"activity,omitempty"`
}
type Node struct {
	Identity
	Labels     []string   `json:"labels"`
	Properties Properties `json:"properties"`
	Evidence   []Evidence `json:"evidence,omitempty"`
	Revision   Revision   `json:"revision"`
	Valid      Interval   `json:"valid"`
}
type Relationship struct {
	Identity
	From       ID         `json:"from"`
	To         ID         `json:"to"`
	Type       string     `json:"type"`
	Properties Properties `json:"properties"`
	Evidence   []Evidence `json:"evidence,omitempty"`
	Revision   Revision   `json:"revision"`
	Valid      Interval   `json:"valid"`
}
type Version struct {
	Retracted    bool          `json:"retracted,omitempty"`
	Node         *Node         `json:"node,omitempty"`
	Relationship *Relationship `json:"relationship,omitempty"`
	RecordedAt   time.Time     `json:"recorded_at"`
	Token        Revision      `json:"token"`
	Reason       string        `json:"reason"`
	Actor        string        `json:"actor,omitempty"`
}

// Basis selects valid time and recorded knowledge. Set either KnownAt or Token.
type Basis struct {
	ValidAt time.Time `json:"valid_at"`
	KnownAt time.Time `json:"known_at,omitempty"`
	Token   Revision  `json:"token,omitempty"`
}
type Snapshot struct {
	Graph      GraphID   `json:"graph"`
	Token      Revision  `json:"token"`
	RecordedAt time.Time `json:"recorded_at"`
	ValidAt    time.Time `json:"valid_at"`
}
type Result struct {
	Rows      []map[string]any `json:"rows"`
	Snapshots []Snapshot       `json:"snapshots"`
}
type Capabilities struct {
	MultiGraph   string `json:"multi_graph"`
	Backend      string `json:"backend"`
	Dialect      string `json:"dialect"`
	MaxGraphs    int    `json:"max_graphs"`
	MaxRows      int    `json:"max_rows"`
	MaxBytes     int    `json:"max_bytes"`
	Cancellation string `json:"cancellation"`
}
type Commit struct {
	Token      Revision  `json:"token"`
	RecordedAt time.Time `json:"recorded_at"`
	Data       []byte    `json:"data"`
}

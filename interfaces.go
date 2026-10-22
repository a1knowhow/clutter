package cuttle

import "context"

// Storage owns durable commits. Append must compare expected and append atomically.
// Implementations must copy byte slices, honor context, and preserve commit order.
type Storage interface {
	List(context.Context) ([]GraphID, error)
	Create(context.Context, GraphID) error
	Read(context.Context, GraphID) ([]Commit, error)
	Append(context.Context, GraphID, Revision, Commit) error
	Delete(context.Context, GraphID) error
	Close() error
}
type Reader interface {
	View(context.Context, []GraphID, Basis) (*View, error)
	History(context.Context, GraphID, ID) ([]Version, error)
}
type Writer interface {
	Apply(context.Context, GraphID, Batch) (Revision, error)
}
type Querier interface {
	Cypher(context.Context, string, Properties) (Result, error)
}
type Lifecycle interface {
	CreateGraph(context.Context, GraphID) error
	Graphs(context.Context) ([]GraphID, error)
	DeleteGraph(context.Context, GraphID) error
	Close() error
}

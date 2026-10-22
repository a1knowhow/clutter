// Package graphqlite owns durable per-graph files, opening one connection per operation.
package graphqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	native "github.com/a1knowhow/cuttle/internal/native/graphqlite"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

var ErrMissing = errors.New("graph not found")
var ErrExists = errors.New("graph exists")
var ErrConflict = errors.New("stale revision")

type Record struct {
	Token      uint64
	RecordedAt time.Time
	Data       []byte
}
type Backend struct {
	root   string
	closed atomic.Bool
}

func Open(root string) (*Backend, error) {
	if root == "" {
		return nil, errors.New("storage directory required")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	return &Backend{root: root}, nil
}
func (b *Backend) path(id string) string {
	h := sha256.Sum256([]byte(id))
	return filepath.Join(b.root, hex.EncodeToString(h[:])+".cuttle")
}
func (b *Backend) db(id string) (*native.DB, error) {
	if b.closed.Load() {
		return nil, errors.New("storage closed")
	}
	if _, e := os.Stat(b.path(id)); os.IsNotExist(e) {
		return nil, ErrMissing
	}
	return native.OpenExisting(b.path(id))
}
func (b *Backend) Create(ctx context.Context, id string) error {
	ctx, release, e := b.Acquire(ctx, id)
	if e != nil {
		return e
	}
	defer release()
	if b.closed.Load() {
		return errors.New("storage closed")
	}
	if _, e := os.Stat(b.path(id)); e == nil {
		return ErrExists
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := os.CreateTemp(b.root, ".initializing-*")
	if e != nil {
		return e
	}
	temp := f.Name()
	f.Close()
	defer os.Remove(temp)
	d, e := native.Open(temp)
	if e != nil {
		return e
	}
	defer d.Close()
	for _, q := range []string{"CREATE TABLE cuttle_graph(id TEXT NOT NULL)", "CREATE TABLE cuttle_commits(token INTEGER PRIMARY KEY, recorded TEXT NOT NULL, data TEXT NOT NULL)"} {
		if _, e = d.Run(ctx, q); e != nil {
			return e
		}
	}
	if _, e = d.Run(ctx, "INSERT INTO cuttle_graph VALUES (?)", id); e != nil {
		return e
	}
	if e = d.Close(); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = os.Link(temp, b.path(id)); os.IsExist(e) {
		return ErrExists
	}
	return e
}
func (b *Backend) List(ctx context.Context) ([]string, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if b.closed.Load() {
		return nil, errors.New("storage closed")
	}
	paths, e := filepath.Glob(filepath.Join(b.root, "*.cuttle"))
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for _, p := range paths {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		readCtx, release, e := b.acquirePath(ctx, p)
		if e != nil {
			return nil, e
		}
		d, e := native.OpenExisting(p)
		if e != nil {
			release()
			if _, statErr := os.Stat(p); os.IsNotExist(statErr) {
				continue
			}
			return nil, e
		}
		r, e := d.Run(readCtx, "SELECT id FROM cuttle_graph")
		d.Close()
		release()
		if e != nil {
			return nil, e
		}
		if len(r) != 1 {
			return nil, fmt.Errorf("corrupt graph %s", p)
		}
		ids = append(ids, r[0]["id"].(string))
	}
	return ids, nil
}
func (b *Backend) Read(ctx context.Context, id string) ([]Record, error) {
	ctx, release, e := b.Acquire(ctx, id)
	if e != nil {
		return nil, e
	}
	defer release()
	d, e := b.db(id)
	if e != nil {
		return nil, e
	}
	defer d.Close()
	r, e := d.Run(ctx, "SELECT token,recorded,data FROM cuttle_commits ORDER BY token")
	if e != nil {
		return nil, e
	}
	records := make([]Record, 0, len(r))
	for _, v := range r {
		at, e := time.Parse(time.RFC3339Nano, v["recorded"].(string))
		if e != nil {
			return nil, e
		}
		records = append(records, Record{uint64(v["token"].(int64)), at, []byte(v["data"].(string))})
	}
	return records, nil
}
func (b *Backend) Append(ctx context.Context, id string, expected uint64, r Record) error {
	ctx, release, e := b.Acquire(ctx, id)
	if e != nil {
		return e
	}
	defer release()
	d, e := b.db(id)
	if e != nil {
		return e
	}
	defer d.Close()
	if _, e = d.Run(ctx, "BEGIN IMMEDIATE"); e != nil {
		return e
	}
	defer d.Run(context.Background(), "ROLLBACK")
	rows, e := d.Run(ctx, "SELECT coalesce(max(token),0) AS token FROM cuttle_commits")
	if e != nil {
		return e
	}
	if uint64(rows[0]["token"].(int64)) != expected || r.Token != expected+1 {
		return ErrConflict
	}
	if _, e = d.Run(ctx, "INSERT INTO cuttle_commits VALUES (?,?,?)", int64(r.Token), r.RecordedAt.UTC().Format(time.RFC3339Nano), string(r.Data)); e != nil {
		return e
	}
	_, e = d.Run(ctx, "COMMIT")
	return e
}
func (b *Backend) Delete(ctx context.Context, id string) error {
	ctx, release, e := b.Acquire(ctx, id)
	if e != nil {
		return e
	}
	defer release()
	if e := ctx.Err(); e != nil {
		return e
	}
	if b.closed.Load() {
		return errors.New("storage closed")
	}
	e = os.Remove(b.path(id))
	if os.IsNotExist(e) {
		return ErrMissing
	}
	return e
}
func (b *Backend) Close() error { b.closed.Store(true); return nil }

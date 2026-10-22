// Package graphqlite embeds the pinned GraphQLite engine through cgo.
package graphqlite

/*
#cgo CFLAGS: -DSQLITE_CORE -I${SRCDIR}/vendor -I${SRCDIR}/vendor/src/include -I${SRCDIR}/vendor/generated -Wno-incompatible-pointer-types-discards-qualifiers -Wno-deprecated-declarations
#cgo LDFLAGS: -lm
#include <stdlib.h>
#include <sqlite3.h>
int sqlite3_graphqlite_init(sqlite3*, char**, const void*);
int cuttle_policy(const char*);
static int bind_text(sqlite3_stmt*s,int n,const char*v){return sqlite3_bind_text(s,n,v,-1,SQLITE_TRANSIENT);}
*/
import "C"
import (
	"context"
	"errors"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"sync"
	"unsafe"
)

type DB struct {
	mu sync.Mutex
	db *C.sqlite3
}

func Open(path string) (*DB, error) { return open(path, true) }

// OpenExisting never creates a missing durable graph file.
func OpenExisting(path string) (*DB, error) { return open(path, false) }
func open(path string, create bool) (*DB, error) {
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	d := &DB{}
	flags := C.int(C.SQLITE_OPEN_READWRITE | C.SQLITE_OPEN_FULLMUTEX)
	if create {
		flags |= C.SQLITE_OPEN_CREATE
	}
	if C.sqlite3_open_v2(p, &d.db, flags, nil) != C.SQLITE_OK {
		e := d.err()
		if d.db != nil {
			C.sqlite3_close(d.db)
			d.db = nil
		}
		return nil, e
	}
	C.sqlite3_busy_timeout(d.db, 50)
	if C.sqlite3_graphqlite_init(d.db, nil, nil) != C.SQLITE_OK {
		e := d.err()
		d.Close()
		return nil, e
	}
	return d, nil
}
func (d *DB) err() error { return errors.New(C.GoString(C.sqlite3_errmsg(d.db))) }
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.db == nil {
		return nil
	}
	if C.sqlite3_close(d.db) != C.SQLITE_OK {
		return d.err()
	}
	d.db = nil
	return nil
}
func (d *DB) Run(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if d.db == nil {
		return nil, errors.New("native database closed")
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			C.sqlite3_interrupt(d.db)
		case <-done:
		}
	}()
	defer func() { close(done); <-stopped }()
	q := C.CString(query)
	defer C.free(unsafe.Pointer(q))
	var stmt *C.sqlite3_stmt
	if C.sqlite3_prepare_v2(d.db, q, -1, &stmt, nil) != C.SQLITE_OK {
		return nil, d.err()
	}
	defer C.sqlite3_finalize(stmt)
	if int(C.sqlite3_bind_parameter_count(stmt)) != len(args) {
		return nil, fmt.Errorf("parameter count mismatch")
	}
	for i, v := range args {
		var rc C.int
		switch x := v.(type) {
		case nil:
			rc = C.sqlite3_bind_null(stmt, C.int(i+1))
		case string:
			p := C.CString(x)
			rc = C.bind_text(stmt, C.int(i+1), p)
			C.free(unsafe.Pointer(p))
		case int:
			rc = C.sqlite3_bind_int64(stmt, C.int(i+1), C.sqlite3_int64(x))
		case int64:
			rc = C.sqlite3_bind_int64(stmt, C.int(i+1), C.sqlite3_int64(x))
		case float64:
			rc = C.sqlite3_bind_double(stmt, C.int(i+1), C.double(x))
		default:
			return nil, fmt.Errorf("unsupported native parameter %T", v)
		}
		if rc != C.SQLITE_OK {
			return nil, d.err()
		}
	}
	rows := []map[string]any{}
	for {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		rc := C.sqlite3_step(stmt)
		if rc == C.SQLITE_DONE {
			break
		}
		if rc != C.SQLITE_ROW {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			return nil, d.err()
		}
		row := map[string]any{}
		for i := C.int(0); i < C.sqlite3_column_count(stmt); i++ {
			name := C.GoString(C.sqlite3_column_name(stmt, i))
			switch C.sqlite3_column_type(stmt, i) {
			case C.SQLITE_NULL:
				row[name] = nil
			case C.SQLITE_INTEGER:
				row[name] = int64(C.sqlite3_column_int64(stmt, i))
			case C.SQLITE_FLOAT:
				row[name] = float64(C.sqlite3_column_double(stmt, i))
			default:
				row[name] = C.GoString((*C.char)(unsafe.Pointer(C.sqlite3_column_text(stmt, i))))
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ValidateRead parses the complete query and accepts only explicitly supported read constructs.
func ValidateRead(query string) error {
	q := C.CString(query)
	defer C.free(unsafe.Pointer(q))
	switch C.cuttle_policy(q) {
	case 0:
		return nil
	case 1:
		return errors.New("invalid Cypher syntax")
	default:
		return errors.New("unsupported or prohibited Cypher construct")
	}
}

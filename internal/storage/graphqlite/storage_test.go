package graphqlite

import (
	"context"
	"errors"
	"fmt"
	native "github.com/a1knowhow/cuttle/internal/native/graphqlite"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()

func backend(t *testing.T, root string) *Backend {
	t.Helper()
	b, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func TestIndependentHandlesSerializeAndCancelLifecycleWait(t *testing.T) {
	root := t.TempDir()
	a, b := backend(t, root), backend(t, root)
	if e := a.Create(ctx, "g"); e != nil {
		t.Fatal(e)
	}
	held, release, e := a.Acquire(ctx, "g")
	if e != nil {
		t.Fatal(e)
	}
	waiting, cancel := context.WithTimeout(ctx, 15*time.Millisecond)
	defer cancel()
	began := time.Now()
	if e = b.Delete(waiting, "g"); !errors.Is(e, context.DeadlineExceeded) || time.Since(began) > time.Second {
		release()
		t.Fatal(e)
	}
	if e = a.Append(held, "g", 0, Record{Token: 1, RecordedAt: time.Now(), Data: []byte("one")}); e != nil {
		release()
		t.Fatal(e)
	}
	release()
	errs := make(chan error, 2)
	for _, store := range []*Backend{a, b} {
		go func(s *Backend) {
			errs <- s.Append(ctx, "g", 1, Record{Token: 2, RecordedAt: time.Now(), Data: []byte("two")})
		}(store)
	}
	first, second := <-errs, <-errs
	if !((first == nil && errors.Is(second, ErrConflict)) || (second == nil && errors.Is(first, ErrConflict))) {
		t.Fatal(first, second)
	}
	if e = b.Delete(ctx, "g"); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Read(ctx, "g"); !errors.Is(e, ErrMissing) {
		t.Fatal(e)
	}
	if e = a.Append(ctx, "g", 0, Record{Token: 1}); !errors.Is(e, ErrMissing) {
		t.Fatal(e)
	}
	if _, e = os.Stat(a.path("g")); !os.IsNotExist(e) {
		t.Fatal("deleted graph recreated", e)
	}
	if _, e = os.Stat(a.path("g") + ".lock"); e != nil {
		t.Fatal("stable lock file removed", e)
	}
}

func TestCreatePublishesOnlyInitializedGraph(t *testing.T) {
	root := t.TempDir()
	b := backend(t, root)
	const total = 15
	done := make(chan struct{})
	fail := make(chan error, 1)
	go func() {
		defer close(done)
		for i := 0; i < total; i++ {
			if e := b.Create(ctx, fmt.Sprintf("g%d", i)); e != nil {
				fail <- e
				return
			}
		}
	}()
	observed := map[string]bool{}
	inspect := func() {
		paths, e := filepath.Glob(filepath.Join(root, "*.cuttle"))
		if e != nil {
			t.Fatal(e)
		}
		for _, p := range paths {
			if observed[p] {
				continue
			}
			d, e := native.OpenExisting(p)
			if e != nil {
				t.Fatal(e)
			}
			rows, e := d.Run(ctx, "SELECT id FROM cuttle_graph")
			d.Close()
			if e != nil || len(rows) != 1 {
				t.Fatalf("uninitialized published graph %s: %v %v", p, rows, e)
			}
			observed[p] = true
		}
	}
	for {
		inspect()
		select {
		case <-done:
			inspect()
			select {
			case e := <-fail:
				t.Fatal(e)
			default:
			}
			if len(observed) != total {
				t.Fatal(len(observed))
			}
			return
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestLifecycleProcessHelper(t *testing.T) {
	if os.Getenv("CUTTLE_LOCK_HELPER") != "1" {
		return
	}
	b, e := Open(os.Getenv("CUTTLE_LOCK_ROOT"))
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	if e = os.WriteFile(filepath.Join(b.root, "process-started"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	if e = b.Delete(ctx, "g"); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(b.root, "process-finished"), nil, 0600); e != nil {
		t.Fatal(e)
	}
}

func TestIndependentProcessDeleteWaitsForWholeOperation(t *testing.T) {
	root := t.TempDir()
	b := backend(t, root)
	if e := b.Create(ctx, "g"); e != nil {
		t.Fatal(e)
	}
	held, release, e := b.Acquire(ctx, "g")
	if e != nil {
		t.Fatal(e)
	}
	var once sync.Once
	unlock := func() { once.Do(release) }
	defer unlock()
	processCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(processCtx, os.Args[0], "-test.run=^TestLifecycleProcessHelper$")
	cmd.Env = append(os.Environ(), "CUTTLE_LOCK_HELPER=1", "CUTTLE_LOCK_ROOT="+root)
	finished := make(chan error, 1)
	go func() {
		out, e := cmd.CombinedOutput()
		if e != nil {
			finished <- fmt.Errorf("%w: %s", e, out)
		} else {
			finished <- nil
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e = os.Stat(filepath.Join(root, "process-started")); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not start")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(25 * time.Millisecond)
	if _, e = os.Stat(filepath.Join(root, "process-finished")); !os.IsNotExist(e) {
		t.Fatal("delete bypassed lifecycle lock", e)
	}
	if e = b.Append(held, "g", 0, Record{Token: 1, RecordedAt: time.Now(), Data: []byte("committed before deletion")}); e != nil {
		t.Fatal(e)
	}
	unlock()
	if e = <-finished; e != nil {
		t.Fatal(e)
	}
	if _, e = b.Read(ctx, "g"); !errors.Is(e, ErrMissing) {
		t.Fatal(e)
	}
	if _, e = os.Stat(b.path("g")); !os.IsNotExist(e) {
		t.Fatal("read recreated deleted graph", e)
	}
}

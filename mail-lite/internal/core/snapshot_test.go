package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestSnapshotResumeConsistentAndIncrementalCatchup(t *testing.T) {
	a := testApp(t, "primary")
	records := []Record{}
	for i := 0; i < 2100; i++ {
		r, _ := recordOf(Account{ID: fmt.Sprintf("a%04d", i), Email: fmt.Sprintf("a%04d@example.net", i), Provider: "outlook", Status: "active", RefreshToken: "secret"})
		records = append(records, r)
	}
	if e := a.store.Put(context.Background(), records, nil); e != nil {
		t.Fatal(e)
	}
	n := Node{ID: "resume", Token: randomToken()}
	a.store.AuxPut("node", n.ID, n)
	// A real HTTP failure after one page leaves durable stage progress.
	var fail atomic.Bool
	fail.Store(true)
	var pages atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/snapshot" {
			count := pages.Add(1)
			if fail.Load() && count == 2 {
				http.Error(w, "simulated disconnect", 503)
				return
			}
		}
		a.Handler().ServeHTTP(w, r)
	}))
	defer server.Close()
	replica := testApp(t, "replica")
	replica.cfg.MasterURL = server.URL
	replica.cfg.NodeID = n.ID
	replica.cfg.NodeToken = n.Token
	if replica.syncOnce(context.Background()) == nil {
		t.Fatal("expected interrupt")
	}
	if replica.isReady() {
		t.Fatal("partial snapshot exposed")
	}
	stage, e := OpenStore(filepath.Join(replica.cfg.DataDir, "stage.db"), replica.key)
	if e != nil {
		t.Fatal(e)
	}
	progress := stage.Meta("snapshot_progress")
	stage.Close()
	if progress == "" {
		t.Fatal("no persisted progress")
	}
	// Concurrent primary edits must not change later pages of the snapshot.
	last, _ := a.store.Account(context.Background(), "a2099")
	last.Note = "after snapshot"
	a.store.Save(context.Background(), last, nil)
	a.store.Delete(context.Background(), "account", []string{"a1500"})
	fail.Store(false)
	if e = replica.syncOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !replica.isReady() || replica.store.Cursor() != a.store.Cursor() {
		t.Fatal("did not catch up")
	}
	got, _ := replica.store.Account(context.Background(), "a2099")
	if got.Note != "after snapshot" {
		t.Fatal("incremental update missing")
	}
	if _, e = replica.store.Account(context.Background(), "a1500"); e != ErrNotFound {
		t.Fatal("tombstone missing", e)
	}
	if pages.Load() != 4 {
		t.Fatalf("expected resume at page 2, requested %d pages", pages.Load())
	}
}

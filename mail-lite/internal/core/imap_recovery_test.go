package core

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

func TestIMAPLatestAndOldestSelectDifferentSequenceRanges(t *testing.T) {
	account := Account{ID: "range", Provider: "icloud"}
	for _, tc := range []struct {
		order, command string
		firstUID       uint32
	}{
		{"latest", "FETCH 4:5", 105},
		{"oldest", "FETCH 1:2", 101},
	} {
		t.Run(tc.order, func(t *testing.T) {
			wire := &imapWire{validity: 42, messageCount: 5, rangeHeaders: true}
			backend := &liveBackend{imapDial: wire.connector(t)}
			items, err := backend.imapListOrder(context.Background(), account, nil, 2, tc.order)
			if err != nil || len(items) != 4 || items[0].Source.UID != tc.firstUID || !strings.Contains(wire.all(), tc.command) {
				t.Fatalf("incorrect %s range: items=%+v err=%v commands=%s", tc.order, items, err, wire.all())
			}
		})
	}
}

func TestIMAPRejectedBatchRetriesEachMessageWithoutSkipping(t *testing.T) {
	wire := &imapWire{validity: 42, messageCount: 120, rangeHeaders: true, rejectBatch: true}
	backend := &liveBackend{imapDial: wire.connector(t)}
	items, err := backend.imapListOrder(context.Background(), Account{ID: "batch", Provider: "icloud"}, nil, 120, "latest")
	if err != nil || len(items) != 240 {
		t.Fatalf("single-message fallback lost mail: len=%d err=%v", len(items), err)
	}
	commands := wire.all()
	for _, batch := range []string{"FETCH 1:50", "FETCH 51:100", "FETCH 101:120"} {
		if !strings.Contains(commands, batch) {
			t.Fatalf("missing bounded batch %q", batch)
		}
	}
	if strings.Contains(commands, "FETCH 1:120") {
		t.Fatal("unbounded message FETCH was sent")
	}
	seen := map[string]bool{}
	for _, item := range items {
		key := fmt.Sprintf("%s:%d", item.Folder, item.Source.UID)
		if seen[key] {
			t.Fatalf("duplicate message after retry: %s", key)
		}
		seen[key] = true
	}
}

func TestIMAPReadRetriesOldServerAfterFetchFailure(t *testing.T) {
	newWire := &imapWire{validity: 42, fetchFailure: "new-server-busy"}
	oldWire := &imapWire{validity: 42, rangeHeaders: true}
	var hosts []string
	backend := &liveBackend{imapDialHost: func(ctx context.Context, account Account, proxies []string, host string) (*client.Client, func(), error) {
		hosts = append(hosts, host)
		if host == "outlook.live.com" {
			return newWire.connector(t)(ctx, account, proxies)
		}
		return oldWire.connector(t)(ctx, account, proxies)
	}}
	items, err := backend.imapListOrder(context.Background(), Account{ID: "fallback", Provider: "outlook"}, nil, 2, "latest")
	if err != nil || len(items) != 4 || !reflect.DeepEqual(hosts, []string{"outlook.live.com", "outlook.office365.com"}) {
		t.Fatalf("new/old server fallback failed: len=%d hosts=%v err=%v", len(items), hosts, err)
	}
	if !strings.Contains(newWire.all(), "FETCH 2:3") || !strings.Contains(newWire.all(), "FETCH 2 ") {
		t.Fatalf("batch failure did not attempt per-message read: %s", newWire.all())
	}
}

func TestIMAPFailedJunkFetchNeverReturnsCompleteInboxOnlyResult(t *testing.T) {
	wire := &imapWire{validity: 42, rangeHeaders: true, failJunk: true}
	backend := &liveBackend{imapDial: wire.connector(t)}
	items, err := backend.imapListOrder(context.Background(), Account{ID: "incomplete", Provider: "outlook"}, nil, 2, "latest")
	if err == nil || len(items) != 0 || !strings.Contains(err.Error(), "junkemail") || !strings.Contains(err.Error(), "fixture FETCH failed") || !strings.Contains(err.Error(), "outlook.live.com") || !strings.Contains(err.Error(), "outlook.office365.com") {
		t.Fatalf("incomplete result was silently accepted or cause lost: items=%v err=%v", items, err)
	}
}

func TestIMAPMetadataSnapshotKeepsEfficientBatchSize(t *testing.T) {
	if got := imapFetchBatchSize([]imap.FetchItem{imap.FetchUid, imap.FetchInternalDate}); got != 1000 {
		t.Fatalf("metadata batch size = %d, want 1000", got)
	}
	section := &imap.BodySectionName{BodyPartName: imap.BodyPartName{Specifier: imap.HeaderSpecifier}, Peek: true}
	if got := imapFetchBatchSize([]imap.FetchItem{imap.FetchUid, imap.FetchInternalDate, section.FetchItem()}); got != 50 {
		t.Fatalf("header batch size = %d, want 50", got)
	}
	wire := &imapWire{validity: 42, missing: true}
	c, close, err := wire.connector(t)(context.Background(), Account{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	if _, err = c.Select("INBOX", true); err != nil {
		t.Fatal(err)
	}
	set := new(imap.SeqSet)
	set.AddRange(1, 500)
	if _, err = fetchIMAP(c, set, []imap.FetchItem{imap.FetchUid, imap.FetchInternalDate}, true); err != nil {
		t.Fatal(err)
	}
	commands := wire.all()
	if !strings.Contains(commands, "UID FETCH 1:500") || strings.Contains(commands, "UID FETCH 1:50 ") {
		t.Fatalf("lightweight metadata snapshot was split into small header batches: %s", commands)
	}
}

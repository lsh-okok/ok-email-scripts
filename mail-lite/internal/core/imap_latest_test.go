package core

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

type latestIMAPTestMessage struct {
	uid                        uint32
	received                   time.Time
	to, proof, subject         string
	missingHeader, missingBody bool
	mutateDetail               bool
	detailTo, detailProof      string
}

func (m latestIMAPTestMessage) raw(body bool) string {
	to, proof := m.to, m.proof
	if body && m.mutateDetail {
		to, proof = m.detailTo, m.detailProof
	}
	value := "From: sender@example.net\r\nTo: " + to + "\r\nSubject: " + m.subject + "\r\n"
	if proof != "" {
		value += "X-iCloud-HME: " + proof + "\r\n"
	}
	if body {
		value += "MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nBody for " + m.subject
	} else {
		value += "\r\n"
	}
	return value
}

type latestIMAPWire struct {
	mu                             sync.Mutex
	mailboxes                      map[string][]latestIMAPTestMessage
	commands                       []string
	searched                       map[string]bool
	dials, metadata, headers, body int
	examines, driftOnExamine       int
}

func (wire *latestIMAPWire) connector(t *testing.T) imapConnector {
	t.Helper()
	return func(ctx context.Context, _ Account, _ []string) (*client.Client, func(), error) {
		wire.mu.Lock()
		wire.dials++
		if wire.searched == nil {
			wire.searched = map[string]bool{}
		}
		wire.mu.Unlock()
		left, right := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer right.Close()
			fmt.Fprint(right, "* PREAUTH [CAPABILITY IMAP4rev1] latest fixture\r\n")
			reader := bufio.NewReader(right)
			selected := "INBOX"
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				fields := strings.Fields(line)
				if len(fields) < 2 {
					return
				}
				tag := fields[0]
				command := strings.TrimSpace(strings.TrimPrefix(line, tag))
				upper := strings.ToUpper(command)
				wire.mu.Lock()
				wire.commands = append(wire.commands, command)
				wire.mu.Unlock()
				switch {
				case strings.HasPrefix(upper, "CAPABILITY"):
					fmt.Fprint(right, "* CAPABILITY IMAP4rev1\r\n")
				case strings.HasPrefix(upper, "LIST"):
					fmt.Fprint(right, "* LIST (\\HasNoChildren) \"/\" \"INBOX\"\r\n* LIST (\\Junk) \"/\" \"Junk\"\r\n")
				case strings.HasPrefix(upper, "EXAMINE") || strings.HasPrefix(upper, "SELECT"):
					selected = strings.Trim(fields[2], "\"")
					wire.mu.Lock()
					wire.examines++
					validity := 42
					if wire.driftOnExamine > 0 && wire.examines >= wire.driftOnExamine {
						validity++
					}
					wire.mu.Unlock()
					fmt.Fprintf(right, "* FLAGS (\\Seen \\Deleted)\r\n* %d EXISTS\r\n* OK [UIDVALIDITY %d] stable\r\n", len(wire.mailboxes[selected]), validity)
				case strings.HasPrefix(upper, "UID SEARCH"):
					wire.mu.Lock()
					wire.searched[selected] = true
					wire.mu.Unlock()
					fmt.Fprint(right, "* SEARCH")
					// Deliberately include substring/forwarding false positives.
					// The production path must enforce exact recipient rules itself.
					for _, item := range wire.mailboxes[selected] {
						fmt.Fprintf(right, " %d", item.uid)
					}
					fmt.Fprint(right, "\r\n")
				case strings.HasPrefix(upper, "UID FETCH"):
					set, err := imap.ParseSeqSet(fields[3])
					if err != nil {
						t.Errorf("invalid UID set: %s", fields[3])
						return
					}
					header := strings.Contains(upper, "HEADER")
					body := strings.Contains(upper, "BODY.PEEK[]")
					items := wire.mailboxes[selected]
					// Reverse result order, with unrelated sequence numbers, so
					// callers must use UID/INTERNALDATE rather than response order.
					for i := len(items) - 1; i >= 0; i-- {
						item := items[i]
						wanted := false
						for _, span := range set.Set {
							wanted = wanted || item.uid >= span.Start && item.uid <= span.Stop
						}
						if !wanted {
							continue
						}
						wire.mu.Lock()
						switch {
						case header:
							wire.headers++
						case body:
							wire.body++
						default:
							wire.metadata++
						}
						wire.mu.Unlock()
						if header && item.missingHeader || body && item.missingBody {
							continue
						}
						date := item.received.Format("02-Jan-2006 15:04:05 -0700")
						if header || body {
							name := "BODY[HEADER]"
							if body {
								name = "BODY[]<0>"
							}
							value := item.raw(body)
							fmt.Fprintf(right, "* %d FETCH (UID %d INTERNALDATE \"%s\" %s {%d}\r\n%s)\r\n", i+1, item.uid, date, name, len(value), value)
						} else {
							fmt.Fprintf(right, "* %d FETCH (UID %d INTERNALDATE \"%s\")\r\n", i+1, item.uid, date)
						}
					}
				default:
					t.Errorf("unexpected latest IMAP command: %s", command)
				}
				fmt.Fprintf(right, "%s OK done\r\n", tag)
			}
		}()
		stop := context.AfterFunc(ctx, func() { left.Close() })
		connection, err := client.New(left)
		if err != nil {
			stop()
			left.Close()
			return nil, nil, err
		}
		return connection, func() { stop(); connection.Terminate(); <-done }, nil
	}
}

func (wire *latestIMAPWire) assertReadOnly(t *testing.T) {
	t.Helper()
	for _, command := range wire.commands {
		upper := strings.ToUpper(command)
		if strings.HasPrefix(upper, "SELECT") || strings.Contains(upper, "STORE") || strings.Contains(upper, "EXPUNGE") || strings.Contains(upper, "MOVE") || strings.Contains(upper, "COPY") {
			t.Fatalf("latest lookup mutated the mailbox: %s", command)
		}
		if strings.Contains(upper, "BODY[") || strings.Contains(upper, "RFC822") {
			t.Fatalf("latest lookup did not use PEEK: %s", command)
		}
	}
}

func latestIMAPTestAccount() Account {
	return Account{ID: "latest-icloud", Provider: "icloud", Status: "active", Email: "main@icloud.com", Password: "fixture-password"}
}

func TestIMAPLatestRecipientOneConnectionAndDateOrdering(t *testing.T) {
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	wire := &latestIMAPWire{mailboxes: map[string][]latestIMAPTestMessage{"INBOX": {}, "Junk": {}}}
	for i := 0; i < 120; i++ {
		wire.mailboxes["INBOX"] = append(wire.mailboxes["INBOX"], latestIMAPTestMessage{uid: uint32(500 + i), received: base.Add(-time.Duration(i+1) * time.Minute), to: "child@example.net", subject: fmt.Sprintf("older-%d", i)})
	}
	// The lowest UID contains the newest message. It lives in Junk, so merely
	// choosing the highest inbox UID (or omitting Junk) returns the wrong body.
	wire.mailboxes["Junk"] = []latestIMAPTestMessage{{uid: 2, received: base, to: "child@example.net", subject: "newest-in-junk"}, {uid: 9999, received: base.Add(-30 * time.Second), to: "child@example.net", subject: "old-high-uid"}}
	backend := &liveBackend{imapDial: wire.connector(t)}
	items, err := backend.LatestRecipient(context.Background(), latestIMAPTestAccount(), nil, "child@example.net")
	if err != nil || len(items) != 1 || items[0].Subject != "newest-in-junk" || items[0].Text != "Body for newest-in-junk" || items[0].Source.UID != 2 || items[0].Source.UIDValidity != 42 || items[0].Folder != "junkemail" {
		t.Fatalf("wrong newest message: items=%+v err=%v", items, err)
	}
	if wire.dials != 1 || !wire.searched["INBOX"] || !wire.searched["Junk"] || wire.metadata != 120 || wire.headers > 20 || wire.body != 1 {
		t.Fatalf("unexpected work: dials=%d searched=%v metadata=%d headers=%d body=%d", wire.dials, wire.searched, wire.metadata, wire.headers, wire.body)
	}
	wire.assertReadOnly(t)
}

func TestIMAPLatestRecipientSmallFoldersCombineDateAndHeaderReads(t *testing.T) {
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	wire := &latestIMAPWire{mailboxes: map[string][]latestIMAPTestMessage{
		"INBOX": {
			{uid: 101, received: base.Add(-time.Hour), to: "child@example.net", subject: "old-inbox"},
			{uid: 2, received: base.Add(-time.Minute), to: "child@example.net", subject: "new-inbox"},
		},
		"Junk": {{uid: 1, received: base, to: "child@example.net", subject: "newest-junk"}},
	}}
	backend := &liveBackend{imapDial: wire.connector(t)}
	items, err := backend.LatestRecipient(context.Background(), latestIMAPTestAccount(), nil, "child@example.net")
	if err != nil || len(items) != 1 || items[0].Subject != "newest-junk" || wire.dials != 1 || wire.metadata != 0 || wire.headers != 3 || wire.body != 1 || wire.examines != 3 {
		t.Fatalf("small folders repeated metadata/header reads: items=%+v err=%v dials=%d metadata=%d headers=%d body=%d examines=%d", items, err, wire.dials, wire.metadata, wire.headers, wire.body, wire.examines)
	}
	headerCommands := 0
	for _, command := range wire.commands {
		upper := strings.ToUpper(command)
		if strings.HasPrefix(upper, "UID FETCH") && strings.Contains(upper, "HEADER") {
			headerCommands++
			if !strings.Contains(upper, "INTERNALDATE") {
				t.Fatalf("small snapshot split headers from receiving dates: %s", command)
			}
		}
	}
	if headerCommands != 2 {
		t.Fatalf("small folders should each fetch headers exactly once: %v", wire.commands)
	}
	wire.assertReadOnly(t)
}

func TestIMAPLatestRecipientVerifiesHeadersAndBody(t *testing.T) {
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	wire := &latestIMAPWire{mailboxes: map[string][]latestIMAPTestMessage{"INBOX": {
		{uid: 10, received: base, to: "not-hide@icloud.com", proof: "p=hide@icloud.com", subject: "substring"},
		{uid: 20, received: base.Add(-time.Minute), to: "hide@icloud.com", proof: "p=wrong@icloud.com", subject: "wrong-proof"},
		{uid: 30, received: base.Add(-2 * time.Minute), to: "hide@icloud.com", proof: "p=hide@icloud.com", subject: "changed-body", mutateDetail: true, detailTo: "hide@icloud.com", detailProof: "p=other@icloud.com"},
		{uid: 40, received: base.Add(-3 * time.Minute), to: "hide@icloud.com", proof: "v=1; p=hide@icloud.com; k=fixture", subject: "verified-forwarded"},
	}, "Junk": {}}}
	backend := &liveBackend{imapDial: wire.connector(t)}
	items, err := backend.LatestRecipient(context.Background(), latestIMAPTestAccount(), nil, "hide@icloud.com")
	if err != nil || len(items) != 1 || items[0].Subject != "verified-forwarded" || !matchesAccountRecipient(latestIMAPTestAccount(), items[0], "hide@icloud.com") {
		t.Fatalf("recipient checks failed: items=%+v err=%v", items, err)
	}
	if wire.dials != 1 || wire.body != 2 {
		t.Fatalf("invalid headers should be rejected before body fetch, changed bodies rechecked: dials=%d bodies=%d", wire.dials, wire.body)
	}
	wire.assertReadOnly(t)
}

func TestIMAPLatestRecipientContinuesPastHeaderBatch(t *testing.T) {
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	wire := &latestIMAPWire{mailboxes: map[string][]latestIMAPTestMessage{"INBOX": {}, "Junk": {}}}
	for i := 0; i < 25; i++ {
		wire.mailboxes["INBOX"] = append(wire.mailboxes["INBOX"], latestIMAPTestMessage{uid: uint32(i + 1), received: base.Add(-time.Duration(i) * time.Minute), to: "not-child@example.net", subject: "substring-false-positive"})
	}
	wire.mailboxes["INBOX"] = append(wire.mailboxes["INBOX"], latestIMAPTestMessage{uid: 26, received: base.Add(-time.Hour), to: "child@example.net", subject: "older-exact-match"})
	backend := &liveBackend{imapDial: wire.connector(t)}
	items, err := backend.LatestRecipient(context.Background(), latestIMAPTestAccount(), nil, "child@example.net")
	if err != nil || len(items) != 1 || items[0].Subject != "older-exact-match" || wire.headers != 26 || wire.body != 1 || wire.dials != 1 {
		t.Fatalf("candidate batching lost an exact match: items=%+v err=%v headers=%d body=%d dials=%d", items, err, wire.headers, wire.body, wire.dials)
	}
	wire.assertReadOnly(t)
}

func TestIMAPLatestRecipientSkipsDeletedCandidates(t *testing.T) {
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	wire := &latestIMAPWire{mailboxes: map[string][]latestIMAPTestMessage{"INBOX": {
		{uid: 1, received: base, to: "child@example.net", subject: "header-deleted", missingHeader: true},
		{uid: 2, received: base.Add(-time.Minute), to: "child@example.net", subject: "body-deleted", missingBody: true},
		{uid: 3, received: base.Add(-2 * time.Minute), to: "child@example.net", subject: "remaining"},
	}, "Junk": {}}}
	backend := &liveBackend{imapDial: wire.connector(t)}
	items, err := backend.LatestRecipient(context.Background(), latestIMAPTestAccount(), nil, "child@example.net")
	if err != nil || len(items) != 1 || items[0].Subject != "remaining" || wire.dials != 1 {
		t.Fatalf("deleted candidates changed recipient selection: items=%+v err=%v dials=%d", items, err, wire.dials)
	}
	wire.assertReadOnly(t)
}

func TestIMAPLatestRecipientNoMatchesAndUIDDrift(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		wire := &latestIMAPWire{mailboxes: map[string][]latestIMAPTestMessage{"INBOX": {}, "Junk": {}}}
		backend := &liveBackend{imapDial: wire.connector(t)}
		items, err := backend.LatestRecipient(context.Background(), latestIMAPTestAccount(), nil, "child@example.net")
		if err != nil || len(items) != 0 || wire.dials != 1 || wire.headers != 0 || wire.body != 0 || !wire.searched["INBOX"] || !wire.searched["Junk"] {
			t.Fatalf("empty lookup did unnecessary work: items=%v err=%v dials=%d headers=%d body=%d search=%v", items, err, wire.dials, wire.headers, wire.body, wire.searched)
		}
		wire.assertReadOnly(t)
	})
	for _, test := range []struct{ count, drift int }{{1, 3}, {21, 3}, {21, 4}} {
		t.Run(fmt.Sprintf("%d-messages-uid-drift-on-examine-%d", test.count, test.drift), func(t *testing.T) {
			wire := &latestIMAPWire{driftOnExamine: test.drift, mailboxes: map[string][]latestIMAPTestMessage{"INBOX": {}, "Junk": {}}}
			for i := 0; i < test.count; i++ {
				wire.mailboxes["INBOX"] = append(wire.mailboxes["INBOX"], latestIMAPTestMessage{uid: uint32(i + 1), received: time.Now().UTC().Add(-time.Duration(i) * time.Minute), to: "child@example.net", subject: "candidate"})
			}
			backend := &liveBackend{imapDial: wire.connector(t)}
			items, err := backend.LatestRecipient(context.Background(), latestIMAPTestAccount(), nil, "child@example.net")
			if err == nil || !strings.Contains(err.Error(), "UID") || len(items) != 0 || wire.body != 0 {
				t.Fatalf("UID drift must stop before body fetch: items=%v err=%v body=%d", items, err, wire.body)
			}
			wire.assertReadOnly(t)
		})
	}
}

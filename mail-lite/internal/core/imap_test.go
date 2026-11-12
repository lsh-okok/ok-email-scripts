package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

type imapWire struct {
	mu           sync.Mutex
	commands     []string
	capabilities string
	validity     uint32
	missing      bool
	headerMode   bool
	body         string
	messageCount uint32
	rangeHeaders bool
	rejectBatch  bool
	fetchFailure string
	failJunk     bool
}

func (w *imapWire) connector(t *testing.T) imapConnector {
	return func(ctx context.Context, a Account, _ []string) (*client.Client, func(), error) {
		left, right := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer right.Close()
			fmt.Fprintf(right, "* PREAUTH [CAPABILITY IMAP4rev1 %s] mock\r\n", w.capabilities)
			reader := bufio.NewReader(right)
			junkSelected := false
			for {
				line, e := reader.ReadString('\n')
				if e != nil {
					return
				}
				fields := strings.Fields(line)
				if len(fields) < 2 {
					return
				}
				tag := fields[0]
				command := strings.TrimSpace(strings.TrimPrefix(line, tag))
				w.mu.Lock()
				w.commands = append(w.commands, command)
				w.mu.Unlock()
				upper := strings.ToUpper(command)
				if strings.HasPrefix(upper, "FETCH") || strings.HasPrefix(upper, "UID FETCH") {
					rangeIndex := 2
					if strings.HasPrefix(upper, "UID FETCH") {
						rangeIndex = 3
					}
					requested := fields[rangeIndex]
					if w.fetchFailure != "" || w.failJunk && junkSelected {
						fmt.Fprintf(right, "%s NO %s\r\n", tag, "fixture FETCH failed "+w.fetchFailure)
						continue
					}
					if w.rejectBatch && strings.ContainsAny(requested, ":,") {
						fmt.Fprintf(right, "%s NO fixture batch FETCH rejected\r\n", tag)
						continue
					}
					if w.rangeHeaders {
						set, err := imap.ParseSeqSet(requested)
						if err != nil {
							return
						}
						for _, span := range set.Set {
							for number := span.Start; number <= span.Stop; number++ {
								header := fmt.Sprintf("From: verify@example.net\r\nTo: child@example.net\r\nSubject: message %d\r\n\r\n", number)
								fmt.Fprintf(right, "* %d FETCH (UID %d INTERNALDATE \"%02d-Oct-2026 12:00:00 +0000\" BODY[HEADER] {%d}\r\n%s)\r\n", number, number+100, number%28+1, len(header), header)
							}
						}
						fmt.Fprintf(right, "%s OK done\r\n", tag)
						continue
					}
				}
				switch {
				case strings.HasPrefix(upper, "CAPABILITY"):
					fmt.Fprintf(right, "* CAPABILITY IMAP4rev1 %s\r\n", w.capabilities)
				case strings.HasPrefix(upper, "LIST"):
					fmt.Fprint(right, "* LIST (\\HasNoChildren) \"/\" \"INBOX\"\r\n* LIST (\\Junk) \"/\" \"Junk\"\r\n* LIST (\\Trash) \"/\" \"Trash\"\r\n")
				case strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "EXAMINE"):
					junkSelected = strings.Contains(upper, "JUNK")
					total := w.messageCount
					if total == 0 {
						total = 3
					}
					fmt.Fprintf(right, "* FLAGS (\\Seen \\Deleted)\r\n* %d EXISTS\r\n* OK [UIDVALIDITY %d] stable\r\n", total, w.validity)
				case strings.HasPrefix(upper, "UID FETCH"):
					if !w.missing {
						uid := fields[3]
						if w.body != "" {
							fmt.Fprintf(right, "* 1 FETCH (UID %s INTERNALDATE \"24-Oct-2026 12:00:00 +0000\" BODY[]<0> {%d}\r\n%s)\r\n", uid, len(w.body), w.body)
						} else {
							fmt.Fprintf(right, "* 1 FETCH (UID %s)\r\n", uid)
						}
					}
				case strings.HasPrefix(upper, "FETCH") && w.headerMode:
					for index, uid := range []int{22, 11} {
						header := fmt.Sprintf("From: verify@example.net\r\nTo: recipient%03d@example.net\r\nSubject: message %d\r\n\r\n", uid, uid)
						fmt.Fprintf(right, "* %d FETCH (UID %d INTERNALDATE \"%02d-Oct-2026 12:00:00 +0000\" BODY[HEADER] {%d}\r\n%s)\r\n", index+2, uid, uid%20+1, len(header), header)
					}
				}
				fmt.Fprintf(right, "%s OK done\r\n", tag)
			}
		}()
		c, e := client.New(left)
		if e != nil {
			left.Close()
			return nil, nil, e
		}
		return c, func() { c.Terminate(); <-done }, nil
	}
}
func (w *imapWire) all() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.commands, "\n")
}
func TestIMAPUIDValidityAndTargetedMove(t *testing.T) {
	a := testApp(t, "primary")
	account := Account{ID: "icloud", Provider: "icloud", Email: "main@icloud.com", Password: "fake"}
	for _, cap := range []string{"MOVE UIDPLUS", "UIDPLUS", ""} {
		t.Run(cap, func(t *testing.T) {
			wire := &imapWire{capabilities: cap, validity: 42}
			b := &liveBackend{app: a, imapDial: wire.connector(t)}
			refs := []MessageRef{{AccountID: account.ID, Protocol: "imap", Folder: "inbox", Mailbox: "INBOX", UID: 10, UIDValidity: 42}, {AccountID: account.ID, Protocol: "imap", Folder: "inbox", Mailbox: "INBOX", UID: 11, UIDValidity: 99}}
			results, e := b.imapDelete(context.Background(), account, []string{""}, refs)
			if e != nil || len(results) != 2 {
				t.Fatal(e, results)
			}
			if results[1].Success || !strings.Contains(results[1].Error, "UID") {
				t.Fatal("UIDVALIDITY mutation", results)
			}
			if results[0].Success != (cap != "") {
				t.Fatal(results)
			}
			commands := wire.all()
			if strings.Contains(commands, "UID MOVE 11") || strings.Contains(commands, "UID COPY 11") || strings.Contains(commands, "\nEXPUNGE") || strings.Contains(commands, "CLOSE") {
				t.Fatal("unsafe operation", commands)
			}
			if cap == "UIDPLUS" && (!strings.Contains(commands, "UID EXPUNGE 10") || strings.Contains(commands, "UID EXPUNGE 10,11")) {
				t.Fatal("not targeted", commands)
			}
			if cap == "" && strings.Contains(commands, "UID COPY") {
				t.Fatal("copy without safe expunge", commands)
			}
		})
	}
}
func TestIMAPMissingUIDIdempotent(t *testing.T) {
	a := testApp(t, "primary")
	wire := &imapWire{capabilities: "MOVE UIDPLUS", validity: 42, missing: true}
	b := &liveBackend{app: a, imapDial: wire.connector(t)}
	out, e := b.imapDelete(context.Background(), Account{ID: "a"}, nil, []MessageRef{{AccountID: "a", Protocol: "imap", Folder: "inbox", Mailbox: "INBOX", UID: 3, UIDValidity: 42}})
	if e != nil || len(out) != 1 || !out[0].Success || strings.Contains(wire.all(), "UID MOVE") {
		t.Fatal(out, e, wire.all())
	}
}
func TestIMAPBatchUIDMappingSortingAndScanBound(t *testing.T) {
	a := testApp(t, "primary")
	wire := &imapWire{capabilities: "MOVE UIDPLUS", validity: 42, headerMode: true}
	b := &liveBackend{app: a, imapDial: wire.connector(t)}
	out, e := b.imapList(context.Background(), Account{ID: "a"}, nil, 2)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range out {
		if m.Source.UID == 0 || m.Source.UIDValidity != 42 || !recipientMatches(m, fmt.Sprintf("recipient%03d@example.net", m.Source.UID)) {
			t.Fatal("UID/header mapping mismatch", m)
		}
	}
	for i := 1; i < len(out); i++ {
		if out[i].ReceivedAt.After(out[i-1].ReceivedAt) {
			t.Fatal("not newest first")
		}
	}
	if !strings.Contains(wire.all(), "FETCH 2:3") {
		t.Fatal("scan range wrong", wire.all())
	}
	if out[0].ReceivedAt.IsZero() || time.Since(out[0].ReceivedAt) < 0 {
		t.Fatal("missing timestamp")
	}
}

func TestIMAPDetailPreservesForwardedRecipientHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, to, header, value string
		allowed                 bool
	}{
		{"hide-my-email", "hide@icloud.com", "X-iCloud-HME", "v=1; p=hide@icloud.com; k=private-key", true},
		{"return-path", "hide@icloud.com", "Return-Path", "<hide=icloud.com@private-relay.example>", true},
		{"wrong-forwarding-header", "hide@icloud.com", "X-iCloud-HME", "p=other@icloud.com", false},
		{"wrong-recipient", "other@icloud.com", "X-iCloud-HME", "p=hide@icloud.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t, "primary")
			account := accountFixture(t, a, "forwarded-imap")
			account.Provider, account.Email, account.Password = "icloud", "main@icloud.com", "fake"
			if _, err := a.store.Save(context.Background(), account, nil); err != nil {
				t.Fatal(err)
			}
			account, err := a.store.Account(context.Background(), account.ID)
			if err != nil {
				t.Fatal(err)
			}
			wire := &imapWire{validity: 42, body: fmt.Sprintf("From: sender@example.net\r\nTo: %s\r\nSubject: forwarded message\r\n%s: %s\r\nX-Private-Header: first\r\nx-private-header: second\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nMessage body", tc.to, tc.header, tc.value)}
			backend := &liveBackend{app: a, imapDial: wire.connector(t)}
			source := MessageRef{AccountID: account.ID, Protocol: "imap", Folder: "inbox", Mailbox: "INBOX", UID: 10, UIDValidity: 42}
			detail, err := backend.imapDetail(context.Background(), account, nil, source)
			if err != nil || detail.Text != "Message body" {
				t.Fatalf("IMAP detail failed: %+v (%v)", detail, err)
			}
			values := detail.Headers[strings.ToLower(tc.header)]
			if len(values) != 1 || values[0] != tc.value || len(detail.Headers["x-private-header"]) != 2 {
				t.Fatalf("detail lost or overwrote forwarding headers: %+v", detail.Headers)
			}
			if got := matchesAccountRecipient(account, detail, "hide@icloud.com"); got != tc.allowed {
				t.Fatalf("recipient validation returned %v, want %v", got, tc.allowed)
			}

			a.mail.backend = backend
			link := linkFixture(t, a, account.ID, "hide@icloud.com")
			w := request(t, a.Handler(), http.MethodGet, "/api/mailboxes/"+link.Token+"/messages?ref="+url.QueryEscape(signRef(a.key, source)), nil, nil, "")
			if !tc.allowed {
				if w.Code != http.StatusNotFound {
					t.Fatalf("unrelated forwarded message was exposed: %d %s", w.Code, w.Body.String())
				}
				return
			}
			var payload struct {
				Items []Message `json:"items"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &payload); w.Code != http.StatusOK || err != nil || len(payload.Items) != 1 || payload.Items[0].Text != "Message body" {
				t.Fatalf("public IMAP ref detail failed: %d %s (%v)", w.Code, w.Body.String(), err)
			}
			if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), tc.value) {
				t.Fatalf("public detail leaked forwarding headers: %s", w.Body.String())
			}
		})
	}
}

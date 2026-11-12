package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/client"
)

type adminIMAPTestMessage struct {
	uid      uint32
	subject  string
	received time.Time
}

type adminIMAPWire struct {
	mu            sync.Mutex
	mailboxes     map[string][]adminIMAPTestMessage
	validity      uint32
	headerCount   int
	dateCount     int
	failHeaders   bool
	bodyRequested bool
}

func (wire *adminIMAPWire) connector(t *testing.T) imapConnector {
	return func(ctx context.Context, _ Account, _ []string) (*client.Client, func(), error) {
		left, right := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer right.Close()
			fmt.Fprint(right, "* PREAUTH [CAPABILITY IMAP4rev1] mock\r\n")
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
				tag, upper := fields[0], strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(line, fields[0])))
				wire.mu.Lock()
				validity := wire.validity
				items := append([]adminIMAPTestMessage{}, wire.mailboxes[selected]...)
				fail := wire.failHeaders
				wire.mu.Unlock()
				switch {
				case strings.HasPrefix(upper, "CAPABILITY"):
					fmt.Fprint(right, "* CAPABILITY IMAP4rev1\r\n")
				case strings.HasPrefix(upper, "LIST"):
					fmt.Fprint(right, "* LIST (\\HasNoChildren) \"/\" \"INBOX\"\r\n* LIST (\\Junk) \"/\" \"Junk\"\r\n")
				case strings.HasPrefix(upper, "EXAMINE") || strings.HasPrefix(upper, "SELECT"):
					selected = strings.Trim(fields[2], "\"")
					wire.mu.Lock()
					count := len(wire.mailboxes[selected])
					wire.mu.Unlock()
					fmt.Fprintf(right, "* FLAGS (\\Seen \\Deleted)\r\n* %d EXISTS\r\n* OK [UIDVALIDITY %d] stable\r\n", count, validity)
				case strings.HasPrefix(upper, "UID SEARCH"):
					fmt.Fprint(right, "* SEARCH")
					for _, item := range items {
						fmt.Fprintf(right, " %d", item.uid)
					}
					fmt.Fprint(right, "\r\n")
				case strings.HasPrefix(upper, "UID FETCH"):
					header := strings.Contains(upper, "HEADER")
					if header && fail {
						fmt.Fprintf(right, "%s NO temporary header failure\r\n", tag)
						continue
					}
					wire.mu.Lock()
					if !header {
						wire.dateCount++
					}
					wire.bodyRequested = wire.bodyRequested || strings.Contains(upper, "BODY.PEEK[]") || strings.Contains(upper, "RFC822")
					wire.mu.Unlock()
					// Intentionally send FETCH results in reverse order and use
					// unrelated sequence positions; pagination must follow UIDs.
					for i := len(items) - 1; i >= 0; i-- {
						item := items[i]
						if !adminIMAPTestContains(fields[3], item.uid) {
							continue
						}
						date := item.received.Format("02-Jan-2006 15:04:05 -0700")
						if header {
							wire.mu.Lock()
							wire.headerCount++
							wire.mu.Unlock()
							value := "From: sender@example.net\r\nTo: child@example.net\r\nSubject: " + item.subject + "\r\n\r\n"
							fmt.Fprintf(right, "* %d FETCH (UID %d INTERNALDATE \"%s\" BODY[HEADER] {%d}\r\n%s)\r\n", i+1, item.uid, date, len(value), value)
						} else {
							fmt.Fprintf(right, "* %d FETCH (UID %d INTERNALDATE \"%s\")\r\n", i+1, item.uid, date)
						}
					}
				default:
					t.Errorf("unexpected IMAP command %s", line)
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

func adminIMAPTestContains(set string, uid uint32) bool {
	for _, part := range strings.Split(set, ",") {
		bounds := strings.Split(part, ":")
		first, _ := strconv.ParseUint(bounds[0], 10, 32)
		last := first
		if len(bounds) == 2 {
			last, _ = strconv.ParseUint(bounds[1], 10, 32)
		}
		if uint64(uid) >= first && uint64(uid) <= last {
			return true
		}
	}
	return false
}

func TestAdminIMAPUIDSnapshotPagination(t *testing.T) {
	for _, order := range []string{"latest", "oldest"} {
		t.Run(order, func(t *testing.T) {
			a := testApp(t, "primary")
			account := accountFixture(t, a, "imap-admin")
			account.Provider, account.Password = "icloud", "app-password"
			if _, err := a.store.Save(context.Background(), account, nil); err != nil {
				t.Fatal(err)
			}
			wire := &adminIMAPWire{validity: 42, mailboxes: map[string][]adminIMAPTestMessage{"INBOX": {}, "Junk": {}}}
			base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
			for i := 0; i < 47; i++ {
				mailbox := "INBOX"
				if i%2 != 0 {
					mailbox = "Junk"
				}
				wire.mailboxes[mailbox] = append(wire.mailboxes[mailbox], adminIMAPTestMessage{uid: uint32(100 + i*17%47), subject: fmt.Sprintf("subject-%02d", i), received: base.Add(-time.Duration(i) * time.Minute)})
			}
			a.mail.backend = &liveBackend{app: a, imapDial: wire.connector(t)}
			cookie, _ := loginFixture(t, a)
			path := "/api/admin/accounts/" + account.ID + "/messages"
			get := func(query string) (int, adminMailPage, string) {
				w := request(t, a.Handler(), http.MethodGet, path+query, nil, cookie, "")
				var page adminMailPage
				if w.Code == 200 {
					json.Unmarshal(w.Body.Bytes(), &page)
				}
				return w.Code, page, w.Body.String()
			}
			status, page, body := get("?order=" + order)
			if status != 200 || len(page.Items) != 20 {
				t.Fatalf("first IMAP page: %d %s", status, body)
			}
			wire.mu.Lock()
			if wire.headerCount != 21 || wire.dateCount != 2 || wire.bodyRequested {
				t.Errorf("expected small UID/date snapshot + 21 headers, got headers=%d dates=%d body=%v", wire.headerCount, wire.dateCount, wire.bodyRequested)
			}
			wire.mailboxes["INBOX"] = append(wire.mailboxes["INBOX"], adminIMAPTestMessage{uid: 999, subject: "new arrival", received: time.Now().UTC()})
			wire.failHeaders = true
			wire.mu.Unlock()
			all := append([]Message{}, page.Items...)
			cursor := page.NextCursor
			if status, _, body := get("?cursor=" + url.QueryEscape(cursor)); status != 400 || !strings.Contains(body, "摘要读取失败") {
				t.Fatalf("IMAP failure should remain explicit: %d %s", status, body)
			}
			wire.mu.Lock()
			wire.failHeaders = false
			wire.mu.Unlock()
			for page.HasMore {
				status, page, body = get("?cursor=" + url.QueryEscape(cursor))
				if status != 200 {
					t.Fatalf("IMAP continuation: %d %s", status, body)
				}
				all = append(all, page.Items...)
				cursor = page.NextCursor
			}
			if len(all) != 47 {
				t.Fatalf("IMAP merged %d messages, want 47", len(all))
			}
			seen := make(map[string]bool)
			for i, item := range all {
				want := i
				if order == "oldest" {
					want = 46 - i
				}
				ref, err := parseRef(a.key, item.Ref, account.ID)
				wantUID := uint32(100 + want*17%47)
				if item.Subject != fmt.Sprintf("subject-%02d", want) || seen[item.Ref] || err != nil || ref.UID != wantUID || ref.UIDValidity != 42 || item.MessageID != strconv.FormatUint(uint64(wantUID), 10) || item.HTML != "" || item.Text != "" {
					t.Fatalf("unstable IMAP page at %d: %+v", i, item)
				}
				seen[item.Ref] = true
			}
			status, refreshed, body := get("?order=latest")
			if status != 200 || refreshed.Items[0].Subject != "new arrival" {
				t.Fatalf("refresh did not find new IMAP UID: %d %s", status, body)
			}
			wire.mu.Lock()
			wire.validity++
			wire.mu.Unlock()
			if status, _, _ := get("?cursor=" + url.QueryEscape(refreshed.NextCursor)); status != 400 {
				t.Fatal("IMAP UIDVALIDITY change did not reject cursor")
			}
		})
	}
}

package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type forwardedRecipientMail struct {
	header string
	value  string
}

func (f forwardedRecipientMail) List(_ context.Context, account Account, _ []string, _ int) ([]Message, error) {
	base := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	items := []Message{
		{Subject: "latest forwarded mail", To: "hide@icloud.com", Folder: "inbox"},
		{Subject: "older forwarded mail", To: "hide@icloud.com", Folder: "junkemail"},
		{Subject: "different recipient", To: "other@icloud.com", Folder: "inbox"},
	}
	for i := range items {
		items[i].From = "sender@example.net"
		items[i].ReceivedAt = base.Add(-time.Duration(i) * time.Minute)
		items[i].Headers = map[string][]string{f.header: {f.value}, "x-private-header": {"private forwarding metadata"}}
		items[i].Text, items[i].HTML = "private message body", "<p>private message body</p>"
		items[i].Source = MessageRef{AccountID: account.ID, Protocol: "graph", Folder: items[i].Folder, ID: items[i].Subject}
	}
	return items, nil
}

func (forwardedRecipientMail) Detail(context.Context, Account, []string, MessageRef) (Message, error) {
	return Message{}, nil
}

func (forwardedRecipientMail) Delete(context.Context, Account, []string, []MessageRef) ([]DeleteResult, error) {
	return nil, nil
}

func TestPublicPaginationPreservesForwardedICloudRecipientChecks(t *testing.T) {
	for _, tc := range []struct {
		name, header, value string
	}{
		{"hide-my-email", "x-icloud-hme", "v=1; p=hide@icloud.com; k=private-forwarding-key"},
		{"return-path", "return-path", "<hide=icloud.com@private-forwarding-relay.example>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t, "primary")
			account := accountFixture(t, a, "forwarded")
			link := linkFixture(t, a, account.ID, "hide@icloud.com")
			a.mail.backend = forwardedRecipientMail{header: tc.header, value: tc.value}
			path := "/api/mailboxes/" + link.Token + "/messages?limit=1"

			// Recipient verification runs again at the public boundary. Headers
			// must survive the internal snapshot, then be removed from its result.
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.SetPathValue("token", link.Token)
			_, page, err := a.publicPageRequest(context.Background(), r, 1, "latest")
			if err != nil || len(page.Items) != 1 || len(page.Items[0].Headers) != 0 {
				t.Fatalf("public summary retained private headers or lost matching mail: %+v (%v)", page.Items, err)
			}

			var cursor string
			for i, want := range []string{"latest forwarded mail", "older forwarded mail"} {
				target := path
				if cursor != "" {
					target += "&cursor=" + url.QueryEscape(cursor)
				}
				w := request(t, a.Handler(), http.MethodGet, target, nil, nil, "")
				var payload struct {
					Items []Message `json:"items"`
					Next  string    `json:"next_cursor"`
					More  bool      `json:"has_more"`
				}
				if err = json.Unmarshal(w.Body.Bytes(), &payload); w.Code != http.StatusOK || err != nil || len(payload.Items) != 1 || payload.Items[0].Subject != want || payload.More != (i == 0) {
					t.Fatalf("forwarded recipient page %d: %d %s (%v)", i+1, w.Code, w.Body.String(), err)
				}
				if payload.Items[0].Text != "" || payload.Items[0].HTML != "" || strings.Contains(w.Body.String(), tc.header) || strings.Contains(w.Body.String(), "private") {
					t.Fatalf("public summary leaked headers or body: %s", w.Body.String())
				}
				cursor = payload.Next
			}
			if cursor != "" {
				t.Fatal("different recipient was included in the pagination result")
			}
		})
	}
}

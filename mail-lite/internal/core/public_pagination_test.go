package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type paginationMail struct{ details atomic.Int64 }

func (p *paginationMail) List(_ context.Context, a Account, _ []string, _ int) ([]Message, error) {
	items := make([]Message, 45)
	base := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for i := range items {
		items[i] = Message{Subject: fmt.Sprintf("subject-%02d", i), From: "sender@example.net", To: "child@example.net", ReceivedAt: base.Add(-time.Duration(i) * time.Minute), Folder: "inbox", Preview: "preview", Source: MessageRef{AccountID: a.ID, Protocol: "graph", Folder: "inbox", ID: fmt.Sprint(i)}}
	}
	return items, nil
}
func (p *paginationMail) Detail(_ context.Context, _ Account, _ []string, r MessageRef) (Message, error) {
	p.details.Add(1)
	return Message{Subject: "subject-" + r.ID, From: "sender@example.net", To: "child@example.net", Text: "body", Source: r}, nil
}
func (p *paginationMail) Delete(context.Context, Account, []string, []MessageRef) ([]DeleteResult, error) {
	return nil, nil
}

func TestPublicPaginationCursorAndOrder(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, "page")
	link := linkFixture(t, a, account.ID, "child@example.net")
	fake := &paginationMail{}
	a.mail.backend = fake
	h := a.Handler()
	var first struct {
		Order string    `json:"order"`
		Items []Message `json:"items"`
		Next  string    `json:"next_cursor"`
		More  bool      `json:"has_more"`
	}
	w := request(t, h, http.MethodGet, "/api/mailboxes/"+link.Token+"/messages", nil, nil, "")
	if e := json.Unmarshal(w.Body.Bytes(), &first); w.Code != 200 || e != nil || first.Order != "latest" || len(first.Items) != 20 || !first.More || first.Next == "" {
		t.Fatalf("first page: %d %s (%v)", w.Code, w.Body.String(), e)
	}
	if first.Items[0].Subject != "subject-00" || first.Items[19].Subject != "subject-19" || first.Items[0].Text != "" || fake.details.Load() != 0 {
		t.Fatalf("unexpected latest summaries: %+v details=%d", first.Items[:1], fake.details.Load())
	}
	var second struct {
		Items []Message `json:"items"`
		Next  string    `json:"next_cursor"`
		More  bool      `json:"has_more"`
	}
	w = request(t, h, http.MethodGet, "/api/mailboxes/"+link.Token+"/messages?limit=20&cursor="+first.Next, nil, nil, "")
	if e := json.Unmarshal(w.Body.Bytes(), &second); w.Code != 200 || e != nil || len(second.Items) != 20 || !second.More || second.Items[0].Subject != "subject-20" {
		t.Fatalf("second page: %d %s (%v)", w.Code, w.Body.String(), e)
	}
	var third struct {
		Items []Message `json:"items"`
		More  bool      `json:"has_more"`
	}
	w = request(t, h, http.MethodGet, "/api/mailboxes/"+link.Token+"/messages?cursor="+second.Next, nil, nil, "")
	if e := json.Unmarshal(w.Body.Bytes(), &third); w.Code != 200 || e != nil || len(third.Items) != 5 || third.More || third.Items[0].Subject != "subject-40" {
		t.Fatalf("third page: %d %s (%v)", w.Code, w.Body.String(), e)
	}
	var oldest struct {
		Items []Message `json:"items"`
	}
	w = request(t, h, http.MethodGet, "/api/mailboxes/"+link.Token+"/messages?order=oldest", nil, nil, "")
	if e := json.Unmarshal(w.Body.Bytes(), &oldest); w.Code != 200 || e != nil || len(oldest.Items) != 20 || oldest.Items[0].Subject != "subject-44" || oldest.Items[19].Subject != "subject-25" {
		t.Fatalf("oldest page: %d %s (%v)", w.Code, w.Body.String(), e)
	}
	if w = request(t, h, http.MethodGet, "/api/mailboxes/"+link.Token+"/messages?cursor="+strings.TrimSuffix(first.Next, "A")+"A", nil, nil, ""); w.Code != 400 {
		t.Fatalf("tampered cursor returned %d", w.Code)
	}
	if w = request(t, h, http.MethodGet, "/api/mailboxes/"+link.Token+"/messages?ref="+paginationEscape(first.Items[0].Ref), nil, nil, ""); w.Code != 200 || fake.details.Load() != 1 {
		t.Fatalf("single detail did not load lazily: %d details=%d", w.Code, fake.details.Load())
	}
	w = request(t, h, http.MethodGet, "/api/mailboxes/"+link.Token+"/messages?limit=20&details=1", nil, nil, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"text":"body"`) || fake.details.Load() != 20 {
		t.Fatalf("details compatibility failed: %d details=%d", w.Code, fake.details.Load())
	}
	cookie, csrf := loginFixture(t, a)
	w = request(t, h, http.MethodGet, "/api/admin/accounts/"+account.ID+"/messages?order=oldest", nil, cookie, csrf)
	var adminPage struct {
		Order string    `json:"order"`
		Items []Message `json:"items"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &adminPage); w.Code != 200 || e != nil || adminPage.Order != "oldest" || len(adminPage.Items) == 0 || adminPage.Items[0].Subject != "subject-44" {
		t.Fatalf("admin oldest order failed: %d %s (%v)", w.Code, w.Body.String(), e)
	}
}

func paginationEscape(s string) string {
	return strings.NewReplacer("+", "%2B", "/", "%2F", "=", "%3D").Replace(s)
}

package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestMailTXTExportsAreCompactAndMasterIsOptional(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	first := createInventoryMaster(t, a, cookie, csrf, "alpha@example.com ---- app password ---- client ---- ---- token ")
	second := createInventoryMaster(t, a, cookie, csrf, "beta@example.com----beta-secret")
	child := insertOutboundTestChild(t, a, first, "one@example.com", " https://receive.example/one ")
	insertOutboundTestChild(t, a, first, "two@example.com", "https://receive.example/two")
	insertOutboundTestChild(t, a, second, "three@example.com", "https://receive.example/three")
	firstRaw := "alpha@example.com----app password----client--------token"
	secondRaw := "beta@example.com----beta-secret"
	children := []string{"one@example.com----https://receive.example/one", "two@example.com----https://receive.example/two", "three@example.com----https://receive.example/three"}
	childrenOnly := strings.Join(children, "\n")
	decodeContent := func(status int, body []byte) string {
		t.Helper()
		var result struct{ Content string }
		if status != http.StatusOK || json.Unmarshal(body, &result) != nil {
			t.Fatalf("export failed: %d %s", status, body)
		}
		return result.Content
	}
	for _, tc := range []struct {
		name    string
		include any
		want    string
	}{
		{"default", nil, strings.Join(append([]string{firstRaw, secondRaw}, children...), "\n")},
		{"with master", true, strings.Join(append([]string{firstRaw, secondRaw}, children...), "\n")},
		{"children only", false, childrenOnly},
	} {
		t.Run("inventory/"+tc.name, func(t *testing.T) {
			body := map[string]any{"ids": []int64{first, second}, "kind": "master", "format": "txt"}
			if tc.include != nil {
				body["include_master"] = tc.include
			}
			w := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/export", body, cookie, csrf)
			if got := decodeContent(w.Code, w.Body.Bytes()); got != tc.want {
				t.Fatalf("export text: got %q, want %q", got, tc.want)
			}
		})
	}
	for _, format := range []string{"txt", "emails"} {
		w := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/export", map[string]any{"ids": []int64{child}, "kind": "child", "format": format, "include_master": true}, cookie, csrf)
		want := children[0]
		if format == "emails" {
			want = "one@example.com"
		}
		if got := decodeContent(w.Code, w.Body.Bytes()); got != want {
			t.Fatalf("child %s export: got %q, want %q", format, got, want)
		}
	}
	// Group order snapshots remain complete even when a download omits credentials.
	w := outboundRequest(t, a, http.MethodPost, "/api/admin/mail/outbounds", map[string]any{"master_ids": []int64{first, second}, "mode": "sell", "selection": "group", "request_key": "compact-multi-group-export"}, cookie, csrf)
	var order struct{ ID int64 }
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &order) != nil {
		t.Fatalf("group order: %d %s", w.Code, w.Body.String())
	}
	withMaster := strings.Join([]string{firstRaw, children[0], children[1], secondRaw, children[2]}, "\n")
	for _, tc := range []struct{ option, want string }{{"", withMaster}, {"?include_master=0", childrenOnly}, {"?include_master=false", childrenOnly}, {"?include_master=1", withMaster}, {"?include_master=true", withMaster}} {
		w = outboundRequest(t, a, http.MethodGet, fmt.Sprintf("/api/admin/mail/outbounds/%d/export%s", order.ID, tc.option), nil, cookie, csrf)
		if got := decodeContent(w.Code, w.Body.Bytes()); got != tc.want {
			t.Fatalf("order %s export: got %q, want %q", tc.option, got, tc.want)
		}
	}
	w = outboundRequest(t, a, http.MethodGet, fmt.Sprintf("/api/admin/mail/outbounds/%d/export?include_master=invalid", order.ID), nil, cookie, csrf)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid export option: %d %s", w.Code, w.Body.String())
	}
	var snapshotCount int
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mail_outbound_masters WHERE outbound_id=?", order.ID).Scan(&snapshotCount); err != nil || snapshotCount != 2 {
		t.Fatalf("export changed master snapshots: %d %v", snapshotCount, err)
	}
	// Returning an item must block downloads with either export choice.
	var itemID int64
	if err := a.store.db.QueryRow("SELECT id FROM mail_outbound_items WHERE outbound_id=? ORDER BY id LIMIT 1", order.ID).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	w = outboundRequest(t, a, http.MethodPost, fmt.Sprintf("/api/admin/mail/outbounds/%d/action", order.ID), map[string]any{"action": "return", "reason": "QA return", "item_ids": []int64{itemID}}, cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("return item: %d %s", w.Code, w.Body.String())
	}
	for _, option := range []string{"0", "1"} {
		w = outboundRequest(t, a, http.MethodGet, fmt.Sprintf("/api/admin/mail/outbounds/%d/export?include_master=%s", order.ID, option), nil, cookie, csrf)
		if w.Code != http.StatusConflict {
			t.Fatalf("disabled order exported with master=%s: %d", option, w.Code)
		}
	}
}

package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func insertOutboundTestChild(t *testing.T, a *App, parent int64, email, link string) int64 {
	t.Helper()
	result, err := a.store.db.Exec("INSERT INTO mailboxes(email,kind,parent_id,link,status,position,created_at,updated_at) VALUES(?,'child',?,?,?,0,?,?)", email, parent, link, "available", stampNow(), stampNow())
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func outboundRequest(t *testing.T, a *App, method, path string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	w := request(t, a.Handler(), method, path, body, cookie, csrf)
	return w
}

func TestMailOutboundReserveLifecycleAndIdempotency(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	master := createInventoryMaster(t, a, cookie, csrf, "sales@example.com----master-secret")
	child1 := insertOutboundTestChild(t, a, master, "sales-1@example.com", "https://receive.example/1")
	child2 := insertOutboundTestChild(t, a, master, "sales-2@example.com", "https://receive.example/2")
	body := map[string]any{
		"master_ids": []int64{master}, "quantity": 1, "mode": "reserve", "selection": "random",
		"include_master": true, "request_key": "reserve-lifecycle-1",
	}
	w := outboundRequest(t, a, "POST", "/api/admin/mail/outbounds", body, cookie, csrf)
	if w.Code != http.StatusCreated {
		t.Fatalf("create reserve: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID      int64  `json:"id"`
		OrderNo string `json:"order_no"`
		Items   int    `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil || created.ID < 1 || created.OrderNo == "" || created.Items != 1 {
		t.Fatalf("bad create response: %v %s", err, w.Body.String())
	}
	var reservedCount, availableCount int
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mailboxes WHERE id IN (?,?) AND status='reserved'", child1, child2).Scan(&reservedCount); err != nil || reservedCount != 1 {
		t.Fatalf("one child should be reserved: %d %v", reservedCount, err)
	}
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mailboxes WHERE id IN (?,?) AND status='available'", child1, child2).Scan(&availableCount); err != nil || availableCount != 1 {
		t.Fatalf("one child should remain available: %d %v", availableCount, err)
	}
	var status string

	w = outboundRequest(t, a, "POST", "/api/admin/mail/outbounds", body, cookie, csrf)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"reused":true`) {
		t.Fatalf("same request key was not idempotent: %d %s", w.Code, w.Body.String())
	}
	changed := map[string]any{"master_ids": []int64{master}, "quantity": 2, "mode": "reserve", "selection": "random", "request_key": "reserve-lifecycle-1"}
	if w = outboundRequest(t, a, "POST", "/api/admin/mail/outbounds", changed, cookie, csrf); w.Code != http.StatusConflict {
		t.Fatalf("changed idempotency payload should conflict: %d %s", w.Code, w.Body.String())
	}

	w = outboundRequest(t, a, "GET", fmt.Sprintf("/api/admin/mail/outbounds/%d", created.ID), nil, cookie, csrf)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"parent_email"`) || strings.Contains(w.Body.String(), `"parentEmail"`) {
		t.Fatalf("outbound detail DTO should be snake_case: %d %s", w.Code, w.Body.String())
	}
	var detail mailOutboundDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || len(detail.Items) != 1 {
		t.Fatalf("bad outbound detail: %v %s", err, w.Body.String())
	}
	itemID := detail.Items[0].ID
	w = outboundRequest(t, a, "POST", fmt.Sprintf("/api/admin/mail/outbounds/%d/action", created.ID), map[string]any{"action": "deliver"}, cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("deliver: %d %s", w.Code, w.Body.String())
	}
	if err := a.store.db.QueryRow("SELECT status FROM mail_outbounds WHERE id=?", created.ID).Scan(&status); err != nil || status != "sold" {
		t.Fatalf("delivered order status: %q %v", status, err)
	}
	w = outboundRequest(t, a, "POST", fmt.Sprintf("/api/admin/mail/outbounds/%d/action", created.ID), map[string]any{"action": "return", "reason": "customer return", "item_ids": []int64{itemID}}, cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("partial return: %d %s", w.Code, w.Body.String())
	}
	if err := a.store.db.QueryRow("SELECT status FROM mail_outbounds WHERE id=?", created.ID).Scan(&status); err != nil || status != "returned" {
		t.Fatalf("one-item order should be returned: %q %v", status, err)
	}
	if w = outboundRequest(t, a, "POST", fmt.Sprintf("/api/admin/mail/outbounds/%d/action", created.ID), map[string]any{"action": "return", "reason": "duplicate", "item_ids": []int64{itemID}}, cookie, csrf); w.Code != http.StatusConflict {
		t.Fatalf("duplicate return should conflict: %d %s", w.Code, w.Body.String())
	}
	if w = outboundRequest(t, a, "GET", fmt.Sprintf("/api/admin/mail/outbounds/%d/export", created.ID), nil, cookie, csrf); w.Code != http.StatusConflict {
		t.Fatalf("returned order should not export: %d %s", w.Code, w.Body.String())
	}
}

func TestMailOutboundCancelAndGroupExport(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	master := createInventoryMaster(t, a, cookie, csrf, "group@example.com----group-secret")
	child1 := insertOutboundTestChild(t, a, master, "group-1@example.com", "https://receive.example/g1")
	child2 := insertOutboundTestChild(t, a, master, "group-2@example.com", "https://receive.example/g2")
	body := map[string]any{"master_ids": []int64{master}, "mode": "sell", "selection": "group", "include_master": true, "request_key": "group-sell-request"}
	w := outboundRequest(t, a, "POST", "/api/admin/mail/outbounds", body, cookie, csrf)
	if w.Code != http.StatusCreated {
		t.Fatalf("group sale: %d %s", w.Code, w.Body.String())
	}
	var created struct{ ID int64 }
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := a.store.db.QueryRow("SELECT status FROM mailboxes WHERE id=?", master).Scan(&status); err != nil || status != "sold" {
		t.Fatalf("group master not sold: %q %v", status, err)
	}
	for _, id := range []int64{child1, child2} {
		if err := a.store.db.QueryRow("SELECT status FROM mailboxes WHERE id=?", id).Scan(&status); err != nil || status != "sold" {
			t.Fatalf("group child %d not sold: %q %v", id, status, err)
		}
	}
	w = outboundRequest(t, a, "GET", fmt.Sprintf("/api/admin/mail/outbounds/%d/export", created.ID), nil, cookie, csrf)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "group@example.com----group-secret") || !strings.Contains(w.Body.String(), "group-1@example.com----https://receive.example/g1") {
		t.Fatalf("group export did not decrypt master snapshot: %d %s", w.Code, w.Body.String())
	}

	cancelMaster := createInventoryMaster(t, a, cookie, csrf, "cancel@example.com----cancel-secret")
	cancelChild := insertOutboundTestChild(t, a, cancelMaster, "cancel-child@example.com", "https://receive.example/cancel")
	cancelBody := map[string]any{"ids": []int64{cancelChild}, "quantity": 1, "mode": "reserve", "selection": "random", "request_key": "cancel-reserve-request"}
	w = outboundRequest(t, a, "POST", "/api/admin/mail/outbounds", cancelBody, cookie, csrf)
	if w.Code != http.StatusCreated {
		t.Fatalf("reserve for cancel: %d %s", w.Code, w.Body.String())
	}
	var reserved struct{ ID int64 }
	if err := json.NewDecoder(w.Body).Decode(&reserved); err != nil {
		t.Fatal(err)
	}
	w = outboundRequest(t, a, "POST", fmt.Sprintf("/api/admin/mail/outbounds/%d/action", reserved.ID), map[string]any{"action": "cancel", "reason": "customer cancelled"}, cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel reserve: %d %s", w.Code, w.Body.String())
	}
	if err := a.store.db.QueryRow("SELECT status FROM mailboxes WHERE id=?", cancelChild).Scan(&status); err != nil || status != "available" {
		t.Fatalf("cancel did not release child: %q %v", status, err)
	}
}

func TestMailOutboundGroupDeliverRejectsNewChild(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	master := createInventoryMaster(t, a, cookie, csrf, "new-child@example.com----secret")
	child := insertOutboundTestChild(t, a, master, "new-child-1@example.com", "https://receive.example/n1")
	body := map[string]any{"master_ids": []int64{master}, "mode": "reserve", "selection": "group", "request_key": "group-reserve-new-child"}
	w := outboundRequest(t, a, "POST", "/api/admin/mail/outbounds", body, cookie, csrf)
	if w.Code != http.StatusCreated {
		t.Fatalf("group reserve: %d %s", w.Code, w.Body.String())
	}
	var created struct{ ID int64 }
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	insertOutboundTestChild(t, a, master, "new-child-2@example.com", "https://receive.example/n2")
	w = outboundRequest(t, a, "POST", fmt.Sprintf("/api/admin/mail/outbounds/%d/action", created.ID), map[string]any{"action": "deliver"}, cookie, csrf)
	if w.Code != http.StatusConflict {
		t.Fatalf("new child should reject group delivery: %d %s", w.Code, w.Body.String())
	}
	var orderStatus, childStatus string
	if err := a.store.db.QueryRow("SELECT status FROM mail_outbounds WHERE id=?", created.ID).Scan(&orderStatus); err != nil || orderStatus != "reserved" {
		t.Fatalf("failed group delivery changed order: %q %v", orderStatus, err)
	}
	if err := a.store.db.QueryRow("SELECT status FROM mailboxes WHERE id=?", child).Scan(&childStatus); err != nil || childStatus != "reserved" {
		t.Fatalf("failed group delivery changed child: %q %v", childStatus, err)
	}
}

func TestMailQuickOutboundUsesFolderScopeAndSoldMode(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	master := createInventoryMaster(t, a, cookie, csrf, "quick@example.com----quick-secret")
	child := insertOutboundTestChild(t, a, master, "quick-child@example.com", "https://receive.example/quick")
	w := outboundRequest(t, a, "POST", "/api/admin/mail/quick-outbound", map[string]any{
		"folder_id": 0, "quantity": 1, "request_key": "quick-outbound-1",
	}, cookie, csrf)
	if w.Code != http.StatusCreated {
		t.Fatalf("quick outbound: %d %s", w.Code, w.Body.String())
	}
	var created struct{ ID int64 }
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil || created.ID < 1 {
		t.Fatalf("bad quick response: %v %s", err, w.Body.String())
	}
	var mode, status string
	if err := a.store.db.QueryRow("SELECT mode,status FROM mail_outbounds WHERE id=?", created.ID).Scan(&mode, &status); err != nil || mode != "quick" || status != "sold" {
		t.Fatalf("quick mode/status mismatch: mode=%q status=%q err=%v", mode, status, err)
	}
	if err := a.store.db.QueryRow("SELECT status FROM mailboxes WHERE id=?", child).Scan(&status); err != nil || status != "sold" {
		t.Fatalf("quick child status: %q %v", status, err)
	}
}

func TestMailOutboundDetailHidesLinksAfterCancelOrReturn(t *testing.T) {
	for _, tc := range []struct {
		name, mode, action string
		returnCount        int
	}{
		{name: "cancelled", mode: "reserve", action: "cancel"},
		{name: "partial_return", mode: "sell", action: "return", returnCount: 1},
		{name: "returned", mode: "sell", action: "return", returnCount: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t, "primary")
			cookie, csrf := loginFixture(t, a)
			master := createInventoryMaster(t, a, cookie, csrf, "detail@example.com----secret")
			child1 := insertOutboundTestChild(t, a, master, "detail-1@example.com", "https://receive.example/private-1")
			child2 := insertOutboundTestChild(t, a, master, "detail-2@example.com", "https://receive.example/private-2")
			w := outboundRequest(t, a, "POST", "/api/admin/mail/outbounds", map[string]any{
				"ids": []int64{child1, child2}, "quantity": 2, "mode": tc.mode,
				"request_key": "hide-detail-" + tc.name,
			}, cookie, csrf)
			if w.Code != http.StatusCreated {
				t.Fatalf("create outbound: %d %s", w.Code, w.Body.String())
			}
			var created struct{ ID int64 }
			if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			path := fmt.Sprintf("/api/admin/mail/outbounds/%d", created.ID)
			w = outboundRequest(t, a, "GET", path, nil, cookie, csrf)
			var detail mailOutboundDetail
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &detail) != nil || !detail.Downloadable || len(detail.Items) != 2 {
				t.Fatalf("active outbound detail: %d %s", w.Code, w.Body.String())
			}
			for _, item := range detail.Items {
				if item.Link == "" {
					t.Fatal("active outbound lost its delivery link")
				}
			}
			body := map[string]any{"action": tc.action, "reason": "regression test"}
			if tc.returnCount > 0 {
				ids := make([]int64, tc.returnCount)
				for i := range ids {
					ids[i] = detail.Items[i].ID
				}
				body["item_ids"] = ids
			}
			w = outboundRequest(t, a, "POST", path+"/action", body, cookie, csrf)
			if w.Code != http.StatusOK {
				t.Fatalf("%s action: %d %s", tc.action, w.Code, w.Body.String())
			}
			w = outboundRequest(t, a, "GET", path, nil, cookie, csrf)
			detail = mailOutboundDetail{}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.Downloadable || detail.Status != tc.name {
				t.Fatalf("inactive outbound detail: %d %s", w.Code, w.Body.String())
			}
			for _, item := range detail.Items {
				if item.Link != "" {
					t.Fatalf("inactive outbound exposed a delivery link: %s", item.Link)
				}
			}
			if strings.Contains(w.Body.String(), "receive.example") {
				t.Fatal("inactive outbound response exposed a delivery link")
			}
		})
	}
}

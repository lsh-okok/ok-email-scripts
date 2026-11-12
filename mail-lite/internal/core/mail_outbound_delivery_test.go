package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func createDeliveryTestOrder(t *testing.T, a *App, cookie *http.Cookie, csrf, selection, mode string) (int64, int64, []int64) {
	t.Helper()
	master := createInventoryMaster(t, a, cookie, csrf, "delivery@example.com----secret")
	children := []int64{
		insertOutboundTestChild(t, a, master, "delivery-1@example.com", "https://receive.example/delivery-1"),
		insertOutboundTestChild(t, a, master, "delivery-2@example.com", "https://receive.example/delivery-2"),
	}
	body := map[string]any{"mode": mode, "selection": selection, "request_key": "delivery-eligibility-test"}
	if selection == "group" {
		body["master_ids"] = []int64{master}
	} else {
		body["ids"], body["quantity"] = children, len(children)
	}
	w := outboundRequest(t, a, "POST", "/api/admin/mail/outbounds", body, cookie, csrf)
	var created struct{ ID int64 }
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.ID == 0 {
		t.Fatalf("create delivery order: %d %s", w.Code, w.Body.String())
	}
	return created.ID, master, children
}

func assertDeliveryReserved(t *testing.T, a *App, order int64, children []int64) {
	t.Helper()
	var status string
	var delivered sql.NullString
	if err := a.store.db.QueryRow("SELECT status,delivered_at FROM mail_outbounds WHERE id=?", order).Scan(&status, &delivered); err != nil || status != "reserved" || delivered.Valid {
		t.Fatalf("failed delivery changed order: status=%q delivered=%v err=%v", status, delivered, err)
	}
	for _, child := range children {
		if err := a.store.db.QueryRow("SELECT status FROM mailboxes WHERE id=?", child).Scan(&status); err != nil || status != "reserved" {
			t.Fatalf("failed delivery changed child %d: status=%q err=%v", child, status, err)
		}
	}
	var histories int
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mail_outbound_history WHERE outbound_id=? AND action='deliver'", order).Scan(&histories); err != nil || histories != 0 {
		t.Fatalf("failed delivery recorded history: count=%d err=%v", histories, err)
	}
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mail_history WHERE action='mail.outbound.deliver'").Scan(&histories); err != nil || histories != 0 {
		t.Fatalf("failed delivery recorded mailbox history: count=%d err=%v", histories, err)
	}
	if err := a.store.db.QueryRow("SELECT COUNT(*) FROM mail_activities WHERE action='mail.outbound.deliver'").Scan(&histories); err != nil || histories != 0 {
		t.Fatalf("failed delivery recorded activity: count=%d err=%v", histories, err)
	}
}

func TestMailOutboundDeliveryRechecksReservedEligibility(t *testing.T) {
	for _, selection := range []string{"random", "group"} {
		for _, change := range []string{"disabled", "abnormal", "after_sale", "master_sold", "parent_changed", "orphan"} {
			t.Run(selection+"/"+change, func(t *testing.T) {
				a := testApp(t, "primary")
				cookie, csrf := loginFixture(t, a)
				order, master, children := createDeliveryTestOrder(t, a, cookie, csrf, selection, "reserve")
				child := children[1]
				var restore func()
				switch change {
				case "disabled", "abnormal", "after_sale":
					setFlag := func(value bool) {
						t.Helper()
						w := outboundRequest(t, a, "POST", "/api/admin/mail/children/flags", map[string]any{"ids": []int64{child}, "flag": change, "value": value, "reason": "delivery eligibility test"}, cookie, csrf)
						if w.Code != http.StatusOK {
							t.Fatalf("set %s=%v: %d %s", change, value, w.Code, w.Body.String())
						}
					}
					setFlag(true)
					restore = func() { setFlag(false) }
				case "master_sold":
					if _, err := a.store.db.Exec("UPDATE mailboxes SET status='sold' WHERE id=?", master); err != nil {
						t.Fatal(err)
					}
					restore = func() {
						if _, err := a.store.db.Exec("UPDATE mailboxes SET status='available' WHERE id=?", master); err != nil {
							t.Fatal(err)
						}
					}
				case "parent_changed", "orphan":
					var parent any
					if change == "parent_changed" {
						parent = createInventoryMaster(t, a, cookie, csrf, "other-delivery@example.com----secret")
					}
					if _, err := a.store.db.Exec("UPDATE mailboxes SET parent_id=? WHERE id=?", parent, child); err != nil {
						t.Fatal(err)
					}
					restore = func() {
						if _, err := a.store.db.Exec("UPDATE mailboxes SET parent_id=? WHERE id=?", master, child); err != nil {
							t.Fatal(err)
						}
					}
				}
				path := fmt.Sprintf("/api/admin/mail/outbounds/%d/action", order)
				w := outboundRequest(t, a, "POST", path, map[string]any{"action": "deliver"}, cookie, csrf)
				if w.Code != http.StatusConflict {
					t.Fatalf("changed inventory was delivered: %d %s", w.Code, w.Body.String())
				}
				assertDeliveryReserved(t, a, order, children)
				restore()
				w = outboundRequest(t, a, "POST", path, map[string]any{"action": "deliver"}, cookie, csrf)
				if w.Code != http.StatusOK {
					t.Fatalf("restored inventory could not deliver: %d %s", w.Code, w.Body.String())
				}
				var status string
				if err := a.store.db.QueryRow("SELECT status FROM mail_outbounds WHERE id=?", order).Scan(&status); err != nil || status != "sold" {
					t.Fatalf("restored order status=%q err=%v", status, err)
				}
				for _, id := range children {
					if err := a.store.db.QueryRow("SELECT status FROM mailboxes WHERE id=?", id).Scan(&status); err != nil || status != "sold" {
						t.Fatalf("restored child %d status=%q err=%v", id, status, err)
					}
				}
				wantMaster := "available"
				if selection == "group" {
					wantMaster = "sold"
				}
				if err := a.store.db.QueryRow("SELECT status FROM mailboxes WHERE id=?", master).Scan(&status); err != nil || status != wantMaster {
					t.Fatalf("restored master status=%q want=%q err=%v", status, wantMaster, err)
				}
			})
		}
	}
}

func TestMailOutboundDeliveryRollsBackAfterInventoryUpdates(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	order, master, children := createDeliveryTestOrder(t, a, cookie, csrf, "group", "reserve")
	// The history write follows the child, order and group-master updates.
	// Failing it exercises rollback of all those writes, not just preflight.
	if _, err := a.store.db.Exec(`CREATE TRIGGER reject_delivery_history BEFORE INSERT ON mail_outbound_history
	 WHEN NEW.action='deliver' BEGIN SELECT RAISE(ABORT,'forced delivery history failure'); END`); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/admin/mail/outbounds/%d/action", order)
	w := outboundRequest(t, a, "POST", path, map[string]any{"action": "deliver"}, cookie, csrf)
	if w.Code == http.StatusOK {
		t.Fatal("delivery succeeded despite the injected database failure")
	}
	assertDeliveryReserved(t, a, order, children)
	var status string
	if err := a.store.db.QueryRow("SELECT status FROM mailboxes WHERE id=?", master).Scan(&status); err != nil || status != "available" {
		t.Fatalf("failed delivery changed group master: status=%q err=%v", status, err)
	}
	if _, err := a.store.db.Exec("DROP TRIGGER reject_delivery_history"); err != nil {
		t.Fatal(err)
	}
	w = outboundRequest(t, a, "POST", path, map[string]any{"action": "deliver"}, cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("delivery after transient failure: %d %s", w.Code, w.Body.String())
	}
}

func TestMailOutboundFlaggedInventoryCanCancelAndReturn(t *testing.T) {
	for _, action := range []string{"cancel", "return"} {
		t.Run(action, func(t *testing.T) {
			a := testApp(t, "primary")
			cookie, csrf := loginFixture(t, a)
			mode := "reserve"
			if action == "return" {
				mode = "sell"
			}
			order, _, children := createDeliveryTestOrder(t, a, cookie, csrf, "random", mode)
			w := outboundRequest(t, a, "POST", "/api/admin/mail/children/flags", map[string]any{"ids": children, "flag": "disabled", "value": true, "reason": "do not sell"}, cookie, csrf)
			if w.Code != http.StatusOK {
				t.Fatalf("disable stock: %d %s", w.Code, w.Body.String())
			}
			body := map[string]any{"action": action, "reason": "release flagged inventory"}
			if action == "return" {
				rows, err := a.store.db.Query("SELECT id FROM mail_outbound_items WHERE outbound_id=?", order)
				if err != nil {
					t.Fatal(err)
				}
				var ids []int64
				for rows.Next() {
					var id int64
					if err := rows.Scan(&id); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					ids = append(ids, id)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					t.Fatal(err)
				}
				body["item_ids"] = ids
			}
			w = outboundRequest(t, a, "POST", fmt.Sprintf("/api/admin/mail/outbounds/%d/action", order), body, cookie, csrf)
			if w.Code != http.StatusOK {
				t.Fatalf("%s flagged stock: %d %s", action, w.Code, w.Body.String())
			}
			for _, child := range children {
				var status string
				var disabled bool
				if err := a.store.db.QueryRow("SELECT status,disabled FROM mailboxes WHERE id=?", child).Scan(&status, &disabled); err != nil || status != "available" || !disabled {
					t.Fatalf("released child %d: status=%q disabled=%v err=%v", child, status, disabled, err)
				}
			}
		})
	}
}

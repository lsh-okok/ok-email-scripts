package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNodeStatusAdminLifecycle(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	h := a.Handler()
	created := request(t, h, "POST", "/api/admin/nodes", map[string]string{"name": "reusable replica"}, cookie, csrf)
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	var original Node
	if err := json.Unmarshal(created.Body.Bytes(), &original); err != nil || original.Token == "" {
		t.Fatal("node creation did not provide credentials", err)
	}
	path := "/api/admin/nodes/" + original.ID + "/status"
	// Legacy node records have no disabled field; they must remain enabled.
	if err := a.store.AuxPut("node", original.ID, map[string]any{
		"id": original.ID, "name": original.Name, "token": original.Token,
		"cursor": int64(41), "last_seen": "2026-11-08T01:00:00Z", "error": "last sync error",
	}); err != nil {
		t.Fatal(err)
	}
	var legacy Node
	if err := a.store.AuxGet("node", original.ID, &legacy); err != nil || legacy.Disabled {
		t.Fatal("legacy node unexpectedly disabled", err)
	}

	lastSeen, wasDisabled := legacy.LastSeen, false
	for _, disabled := range []bool{true, true, false, false} {
		if wasDisabled && !disabled {
			lastSeen = "" // A fresh heartbeat must confirm that an enabled server is online.
		}
		w := request(t, h, "PUT", path, map[string]bool{"disabled": disabled}, cookie, csrf)
		if w.Code != 200 {
			t.Fatal(disabled, w.Code, w.Body.String())
		}
		var visible Node
		var raw map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &visible); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		if _, leaked := raw["token"]; leaked || visible.Token != "" || visible.ID != original.ID || visible.Disabled != disabled {
			t.Fatal("status response changed identity, exposed credentials or returned wrong status", w.Body.String())
		}
		var stored Node
		if err := a.store.AuxGet("node", original.ID, &stored); err != nil {
			t.Fatal(err)
		}
		if stored.Disabled != disabled || stored.Token != original.Token || stored.ID != legacy.ID || stored.Name != legacy.Name || stored.Cursor != legacy.Cursor || stored.LastSeen != lastSeen {
			t.Fatalf("status update destroyed reusable credentials or sync progress: %+v", stored)
		}
		wasDisabled = disabled
		listed := request(t, h, "GET", "/api/admin/nodes", nil, cookie, "")
		var list struct{ Items []Node }
		if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil || listed.Code != 200 || len(list.Items) != 1 || list.Items[0].Disabled != disabled || list.Items[0].Token != "" {
			t.Fatal("admin list did not expose status safely", listed.Code, listed.Body.String(), err)
		}
	}

	if w := request(t, h, "DELETE", "/api/admin/nodes/"+original.ID, nil, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, disabled := range []bool{false, true} {
		w := request(t, h, "PUT", path, map[string]bool{"disabled": disabled}, cookie, csrf)
		if w.Code != 409 {
			t.Fatal("revoked credential could be reused through status API", w.Code, w.Body.String())
		}
	}
	var revoked Node
	if err := a.store.AuxGet("node", original.ID, &revoked); err != nil || !revoked.Revoked || revoked.Token != "" {
		t.Fatal("permanent revocation was undone", err)
	}
}

func TestNodeStatusRequiresExplicitAuthorizedChange(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	node := Node{ID: "node-status-validation", Name: "validation", Token: randomToken()}
	if err := a.store.AuxPut("node", node.ID, node); err != nil {
		t.Fatal(err)
	}
	h := a.Handler()
	path := "/api/admin/nodes/" + node.ID + "/status"
	for _, tc := range []struct {
		name string
		body any
	}{
		{"missing disabled", map[string]any{}},
		{"null disabled", map[string]any{"disabled": nil}},
		{"string disabled", map[string]any{"disabled": "false"}},
		{"unknown field", map[string]any{"disabled": true, "token": "replacement"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(t, h, "PUT", path, tc.body, cookie, csrf)
			if w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	if w := request(t, h, "PUT", path, map[string]bool{"disabled": true}, nil, ""); w.Code != 401 {
		t.Fatal("anonymous mutation allowed", w.Code, w.Body.String())
	}
	if w := request(t, h, "PUT", path, map[string]bool{"disabled": true}, cookie, ""); w.Code != 403 {
		t.Fatal("mutation without CSRF allowed", w.Code, w.Body.String())
	}
	if w := request(t, h, "PUT", "/api/admin/nodes/not-a-node/status", map[string]bool{"disabled": true}, cookie, csrf); w.Code != 404 {
		t.Fatal("missing node did not return 404", w.Code, w.Body.String())
	}
	var unchanged Node
	if err := a.store.AuxGet("node", node.ID, &unchanged); err != nil || unchanged.Disabled || unchanged.Token != node.Token {
		t.Fatal("invalid request mutated node", err)
	}
}

func TestNodeDisabledBlocksClusterAndResumesWithSameCredentials(t *testing.T) {
	primary := testApp(t, "primary")
	account := mxFixture(t, primary, "resumable-node.test")
	link := linkFixture(t, primary, account.ID, "user@resumable-node.test")
	mxDeliver(t, primary, account, link.Recipient, "before disable", time.Now())
	node := Node{ID: "resumable-node", Token: randomToken(), Name: "resumable"}
	if err := primary.store.AuxPut("node", node.ID, node); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(primary.Handler())
	defer server.Close()
	replica := testApp(t, "replica")
	replica.cfg.MasterURL, replica.cfg.NodeID, replica.cfg.NodeToken = server.URL, node.ID, node.Token
	if err := replica.syncOnce(context.Background()); err != nil || !replica.isReady() {
		t.Fatal("initial replica sync failed", err)
	}
	publicPath := "/api/mailboxes/" + link.Token + "/messages?details=1"
	if got := mxAPI(t, replica, link.Token, "?details=1"); len(got.Items) != 1 || got.Items[0].Subject != "before disable" {
		t.Fatal("initial MX read failed", got)
	}
	cookie, csrf := loginFixture(t, primary)
	path := "/api/admin/nodes/" + node.ID + "/status"
	if w := request(t, primary.Handler(), "PUT", path, map[string]bool{"disabled": true}, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct{ method, path string }{
		{"POST", "/internal/snapshot"}, {"GET", "/internal/events?after=0"},
		{"POST", "/internal/heartbeat"}, {"POST", "/internal/tokens"},
		{"POST", "/internal/mx/read"},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		r.Header.Set("X-Node-ID", node.ID)
		r.Header.Set("Authorization", "Bearer "+node.Token)
		w := httptest.NewRecorder()
		primary.Handler().ServeHTTP(w, r)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "node_disabled") {
			t.Fatal("disabled node retained cluster access", tc.path, w.Code, w.Body.String())
		}
	}
	if w := request(t, replica.Handler(), "GET", publicPath, nil, nil, ""); w.Code != 503 {
		t.Fatal("disabled replica returned previously read MX mail", w.Code, w.Body.String())
	}
	if err := replica.syncOnce(context.Background()); err == nil || !strings.Contains(err.Error(), "停用") || replica.isReady() {
		t.Fatal("disabled replica stayed ready or reported revocation instead", err)
	}
	// Resume must catch up changes made during downtime, with the original on-disk configuration.
	account.Note = "changed while replica disabled"
	if _, err := primary.store.Save(context.Background(), account, nil); err != nil {
		t.Fatal(err)
	}
	mxDeliver(t, primary, account, link.Recipient, "during disable", time.Now())
	if w := request(t, primary.Handler(), "PUT", path, map[string]bool{"disabled": false}, cookie, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := replica.syncOnce(context.Background()); err != nil || !replica.isReady() {
		t.Fatal("same credential could not resume sync", err)
	}
	if replica.cfg.NodeID != node.ID || replica.cfg.NodeToken != node.Token || replica.store.Cursor() != primary.store.Cursor() {
		t.Fatal("resume required credential rotation or failed to catch up")
	}
	replicated, err := replica.store.Account(context.Background(), account.ID)
	if err != nil || replicated.Note != account.Note {
		t.Fatal("resume omitted updates made while disabled", replicated.Note, err)
	}
	if got := mxAPI(t, replica, link.Token, "?details=1"); len(got.Items) != 2 || got.Items[0].Subject != "during disable" {
		t.Fatal("same credential could not resume MX reads", got)
	}
}

func TestNodeHeartbeatCannotRestoreDisabledState(t *testing.T) {
	a := testApp(t, "primary")
	node := Node{ID: "disabled-heartbeat", Token: randomToken(), Disabled: true, Cursor: 17, LastSeen: "2026-11-08T01:00:00Z"}
	if err := a.store.AuxPut("node", node.ID, node); err != nil {
		t.Fatal(err)
	}
	// Simulate a heartbeat that passed middleware just before the admin disabled it.
	r := httptest.NewRequest(http.MethodPost, "/internal/heartbeat", strings.NewReader(`{"cursor":99,"error":""}`))
	r.Header.Set("X-Node-ID", node.ID)
	w := httptest.NewRecorder()
	a.heartbeat(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "node_disabled") {
		t.Fatal("in-flight heartbeat was accepted after disable", w.Code, w.Body.String())
	}
	var stored Node
	if err := a.store.AuxGet("node", node.ID, &stored); err != nil || !stored.Disabled || stored.Cursor != node.Cursor || stored.LastSeen != node.LastSeen || stored.Token != node.Token {
		t.Fatal("in-flight heartbeat changed disabled node", stored, err)
	}
}

func TestNodeRevokedRecordDeletionPreservesAccessBoundary(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	h := a.Handler()
	token := randomToken()
	nodes := []Node{
		{ID: "delete-active", Token: randomToken()},
		{ID: "delete-disabled", Token: randomToken(), Disabled: true},
		{ID: "delete-revoked", Token: token},
	}
	for _, node := range nodes {
		if err := a.store.AuxPut("node", node.ID, node); err != nil {
			t.Fatal(err)
		}
	}
	for _, node := range nodes[:2] {
		w := request(t, h, "DELETE", "/api/admin/nodes/"+node.ID+"/record", nil, cookie, csrf)
		if w.Code != 409 {
			t.Fatal("active or disabled node record was deleted", node.ID, w.Code, w.Body.String())
		}
		var stored Node
		if err := a.store.AuxGet("node", node.ID, &stored); err != nil || stored.Token != node.Token || stored.Disabled != node.Disabled {
			t.Fatal("rejected deletion mutated node", node.ID, err)
		}
	}
	if w := request(t, h, "DELETE", "/api/admin/nodes/delete-revoked", nil, cookie, csrf); w.Code != 200 {
		t.Fatal("revocation failed", w.Code, w.Body.String())
	}
	path := "/api/admin/nodes/delete-revoked/record"
	if w := request(t, h, "DELETE", path, nil, nil, ""); w.Code != 401 {
		t.Fatal("unauthenticated record deletion allowed", w.Code, w.Body.String())
	}
	if w := request(t, h, "DELETE", path, nil, cookie, ""); w.Code != 403 {
		t.Fatal("record deletion without CSRF allowed", w.Code, w.Body.String())
	}
	if w := request(t, h, "DELETE", path, nil, cookie, csrf); w.Code != 200 {
		t.Fatal("revoked record could not be deleted", w.Code, w.Body.String())
	}
	var deleted Node
	if err := a.store.AuxGet("node", nodes[2].ID, &deleted); err == nil {
		t.Fatal("deleted record still stored")
	}
	listed := request(t, h, "GET", "/api/admin/nodes", nil, cookie, "")
	var list struct{ Items []Node }
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil || listed.Code != 200 || len(list.Items) != 2 {
		t.Fatal("deleted record still listed or unrelated nodes removed", listed.Code, listed.Body.String(), err)
	}
	for _, node := range list.Items {
		if node.ID == nodes[2].ID || node.Token != "" {
			t.Fatal("deleted record or credential exposed", node.ID)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/internal/events?after=0", nil)
	r.Header.Set("X-Node-ID", nodes[2].ID)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("record deletion restored old credential access", w.Code, w.Body.String())
	}
	if w := request(t, h, "DELETE", path, nil, cookie, csrf); w.Code != 404 {
		t.Fatal("missing record did not return 404", w.Code, w.Body.String())
	}
}

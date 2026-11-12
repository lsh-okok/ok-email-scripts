package core

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNodeHeartbeatRecordsObservedIP(t *testing.T) {
	a := testApp(t, "primary")
	cookie, _ := loginFixture(t, a)
	node := Node{ID: "node-ip", Token: randomToken(), IP: "192.0.2.1"}
	for _, tc := range []struct{ name, remote, want string }{
		{"IPv4", "192.168.31.112:54321", "192.168.31.112"},
		{"IPv6", "[2001:0db8:0:0:0:0:0:112]:443", "2001:db8::112"},
		{"mapped IPv4", "[::ffff:192.168.31.113]:12345", "192.168.31.113"},
		{"invalid socket preserves previous IP", "not-a-socket-address", "192.0.2.1"},
		{"hostname preserves previous IP", "example.com:4444", "192.0.2.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := a.store.AuxPut("node", node.ID, node); err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", "/internal/heartbeat", strings.NewReader(`{"cursor":0,"error":""}`))
			r.RemoteAddr = tc.remote
			r.Header.Set("X-Node-ID", node.ID)
			r.Header.Set("Authorization", "Bearer "+node.Token)
			r.Header.Set("X-Forwarded-For", "203.0.113.99")
			r.Header.Set("Forwarded", "for=203.0.113.98")
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var stored Node
			if err := a.store.AuxGet("node", node.ID, &stored); err != nil || stored.IP != tc.want {
				t.Fatal("heartbeat did not retain its observed IP", stored.IP, err)
			}
			listed := request(t, a.Handler(), "GET", "/api/admin/nodes", nil, cookie, "")
			var result struct{ Items []Node }
			if err := json.Unmarshal(listed.Body.Bytes(), &result); err != nil || listed.Code != 200 || len(result.Items) != 1 || result.Items[0].IP != tc.want || result.Items[0].Token != "" {
				t.Fatal("admin list did not expose the IP without credentials", listed.Code, listed.Body.String(), err)
			}
		})
	}
}

func TestNodeRejectedHeartbeatPreservesIP(t *testing.T) {
	a := testApp(t, "primary")
	for _, tc := range []struct {
		name     string
		disabled bool
		revoked  bool
		badToken bool
	}{
		{name: "invalid credential", badToken: true},
		{name: "disabled node", disabled: true},
		{name: "revoked node", revoked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := Node{ID: "rejected-node-ip", Token: randomToken(), IP: "192.168.31.112", Disabled: tc.disabled, Revoked: tc.revoked}
			if err := a.store.AuxPut("node", node.ID, node); err != nil {
				t.Fatal(err)
			}
			token := node.Token
			if tc.badToken {
				token = "wrong-token"
			}
			r := httptest.NewRequest("POST", "/internal/heartbeat", strings.NewReader(`{"cursor":0,"error":""}`))
			r.RemoteAddr = "192.168.31.113:55555"
			r.Header.Set("X-Node-ID", node.ID)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatal("unauthorized heartbeat was accepted", w.Code, w.Body.String())
			}
			var stored Node
			if err := a.store.AuxGet("node", node.ID, &stored); err != nil || stored.IP != node.IP {
				t.Fatal("unauthorized heartbeat changed the last observed IP", stored.IP, err)
			}
		})
	}
}

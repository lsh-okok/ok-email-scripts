package core

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProxyInheritanceAndValidation(t *testing.T) {
	a := testApp(t, "primary")
	ctx := context.Background()
	for _, g := range []Group{{ID: "one", Proxies: []string{"http://parent.example:8080"}}, {ID: "two", ParentID: "one"}, {ID: "three", ParentID: "two", Proxies: []string{"socks5://child.example:1080"}}} {
		if _, e := a.store.Save(ctx, g, nil); e != nil {
			t.Fatal(e)
		}
	}
	for _, test := range []struct {
		account Account
		want    string
	}{{Account{GroupID: "two"}, "http://parent.example:8080"}, {Account{GroupID: "three"}, "socks5://child.example:1080"}, {Account{GroupID: "three", Proxies: []string{"https://override.example:8443"}}, "https://override.example:8443"}, {Account{}, ""}} {
		values, e := a.effectiveProxies(ctx, test.account)
		if e != nil || len(values) != 1 || values[0] != test.want {
			t.Fatal(values, e)
		}
	}
	for _, bad := range [][]string{{"ftp://bad.example:21"}, {"http://without-port.example"}, {"http://a:80", "http://b:80", "http://c:80", "http://d:80"}} {
		if validateProxies(bad) == nil {
			t.Fatal("accepted invalid proxy", bad)
		}
	}
}
func TestHTTPConnectProxyUsesPerConnectionAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "imap.example:993" {
			t.Error("wrong CONNECT", r.Method, r.Host)
		}
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:password"))
		if r.Header.Get("Proxy-Authorization") != want {
			t.Error("missing proxy auth")
		}
		conn, buffer, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		defer conn.Close()
		buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		buffer.Flush()
		io.Copy(conn, conn)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	proxyURL := "http://user:password@" + server.Listener.Addr().String()
	conn, e := proxyDial(ctx, "imap.example:993", proxyURL)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, e = conn.Write([]byte("test")); e != nil {
		t.Fatal(e)
	}
	b := make([]byte, 4)
	if _, e = io.ReadFull(conn, b); e != nil || string(b) != "test" {
		t.Fatal(string(b), e)
	}
}

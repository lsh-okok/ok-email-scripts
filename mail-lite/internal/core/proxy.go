package core

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

func validateProxies(values []string) error {
	if len(values) > 3 {
		return errors.New("at most three proxy addresses are allowed")
	}
	for _, s := range values {
		u, e := url.Parse(s)
		if e != nil || u.Hostname() == "" || u.Port() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("proxy must be a complete http://, https:// or socks5:// host:port address")
		}
		if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h" {
			return errors.New("unsupported proxy protocol")
		}
	}
	return nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
func proxyDial(ctx context.Context, address, proxyURL string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if proxyURL == "" {
		return d.DialContext(ctx, "tcp", address)
	}
	u, e := url.Parse(proxyURL)
	if e != nil {
		return nil, e
	}
	if u.Scheme == "socks5" || u.Scheme == "socks5h" {
		var auth *proxy.Auth
		if u.User != nil {
			p, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: p}
		}
		sd, e := proxy.SOCKS5("tcp", u.Host, auth, d)
		if e != nil {
			return nil, e
		}
		cd, ok := sd.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("proxy does not support cancellation")
		}
		return cd.DialContext(ctx, "tcp", address)
	}
	c, e := d.DialContext(ctx, "tcp", u.Host)
	if e != nil {
		return nil, e
	}
	ok := false
	defer func() {
		if !ok {
			c.Close()
		}
	}()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	if u.Scheme == "https" {
		tc := tls.Client(c, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
		if e = tc.HandshakeContext(ctx); e != nil {
			return nil, e
		}
		c = tc
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: address}, Host: address, Header: make(http.Header)}
	if u.User != nil {
		p, _ := u.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+p)))
	}
	if e = req.Write(c); e != nil {
		return nil, e
	}
	reader := bufio.NewReader(c)
	res, e := http.ReadResponse(reader, req)
	if e != nil {
		return nil, e
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("proxy CONNECT returned %d", res.StatusCode)
	}
	c.SetDeadline(time.Time{})
	ok = true
	return &bufferedConn{Conn: c, r: reader}, nil
}
func transport(proxyURL string) *http.Transport {
	t := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 25 * time.Second, MaxIdleConns: 16, MaxIdleConnsPerHost: 4}
	if proxyURL != "" {
		u, _ := url.Parse(proxyURL)
		if strings.HasPrefix(u.Scheme, "socks") {
			t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return proxyDial(ctx, address, proxyURL)
			}
		} else {
			t.Proxy = http.ProxyURL(u)
		}
	}
	return t
}
func (a *App) effectiveProxies(ctx context.Context, account Account) ([]string, error) {
	if len(account.Proxies) > 0 {
		return append([]string{}, account.Proxies...), nil
	}
	id := account.GroupID
	seen := map[string]bool{}
	for id != "" {
		if seen[id] || len(seen) >= 3 {
			return nil, errors.New("invalid group hierarchy")
		}
		seen[id] = true
		r, e := a.store.Get(ctx, "group", id)
		if e != nil {
			return nil, e
		}
		var g Group
		if e = decode(r.Data, &g); e != nil {
			return nil, e
		}
		if len(g.Proxies) > 0 {
			return append([]string{}, g.Proxies...), nil
		}
		id = g.ParentID
	}
	return []string{""}, nil
}

package core

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Role, Listen, DataDir, DataKey, AdminPassword     string
	MasterURL, NodeID, NodeToken, TLSCert, TLSKey     string
	ExternalOrigin                                    string
	AllowInsecure                                     bool
	Poll, MaxStale                                    time.Duration
	UpstreamLimit                                     int
	GraphURL, OAuthURL, IMAPOAuthURL                  string
	SMTPEnabled                                       bool
	SMTPListen, SMTPHostname, SMTPTLSCert, SMTPTLSKey string
}

func env(k, d string) string {
	if s := strings.TrimSpace(os.Getenv(k)); s != "" {
		return s
	}
	return d
}
func LoadConfig() (Config, error) {
	c := Config{ExternalOrigin: strings.TrimRight(env("APP_ORIGIN", ""), "/"), Role: env("NODE_ROLE", "primary"), Listen: env("LISTEN_ADDR", ":8080"), DataDir: env("DATA_DIR", "data"), DataKey: env("DATA_KEY", ""), AdminPassword: env("ADMIN_PASSWORD", ""), MasterURL: strings.TrimRight(env("MASTER_URL", ""), "/"), NodeID: env("NODE_ID", ""), NodeToken: env("NODE_TOKEN", ""), TLSCert: env("TLS_CERT", ""), TLSKey: env("TLS_KEY", ""), AllowInsecure: env("ALLOW_INSECURE_NODE_HTTP", "") == "true", Poll: 5 * time.Second, MaxStale: 120 * time.Second, UpstreamLimit: 16, GraphURL: "https://graph.microsoft.com/v1.0", OAuthURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token"}
	if c.ExternalOrigin != "" {
		u, e := url.Parse(c.ExternalOrigin)
		if e != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return c, errors.New("APP_ORIGIN must be an HTTP(S) origin")
		}
	}
	if c.Role != "primary" && c.Role != "replica" {
		return c, errors.New("NODE_ROLE must be primary or replica")
	}
	if c.Role == "replica" {
		if c.NodeID == "" || c.NodeToken == "" {
			return c, errors.New("replica requires NODE_ID and NODE_TOKEN")
		}
		u, e := url.Parse(c.MasterURL)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
			return c, errors.New("MASTER_URL must be an HTTP(S) origin")
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && (c.AllowInsecure || isLoopback(u.Hostname()))) {
			return c, errors.New("MASTER_URL requires HTTPS; private-network HTTP requires ALLOW_INSECURE_NODE_HTTP=true")
		}
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return c, errors.New("TLS_CERT and TLS_KEY must be set together")
	}
	if s := os.Getenv("UPSTREAM_CONCURRENCY"); s != "" {
		n, e := strconv.Atoi(s)
		if e != nil || n < 1 || n > 128 {
			return c, errors.New("UPSTREAM_CONCURRENCY must be 1..128")
		}
		c.UpstreamLimit = n
	}
	var e error
	c.SMTPEnabled = env("SMTP_ENABLED", "false") == "true"
	c.SMTPListen = env("SMTP_LISTEN_ADDR", ":2525")
	c.SMTPHostname = env("SMTP_HOSTNAME", "")
	c.SMTPTLSCert, c.SMTPTLSKey = env("SMTP_TLS_CERT", ""), env("SMTP_TLS_KEY", "")
	if (c.SMTPTLSCert == "") != (c.SMTPTLSKey == "") {
		return c, errors.New("SMTP_TLS_CERT and SMTP_TLS_KEY must be set together")
	}
	if c.SMTPEnabled && (c.Role != "primary" || c.SMTPHostname == "") {
		return c, errors.New("SMTP requires primary role and SMTP_HOSTNAME")
	}
	c.DataDir, e = filepath.Abs(c.DataDir)
	return c, e
}
func isLoopback(s string) bool {
	if s == "localhost" {
		return true
	}
	ip := net.ParseIP(s)
	return ip != nil && ip.IsLoopback()
}

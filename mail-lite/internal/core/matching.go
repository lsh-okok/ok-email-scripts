package core

import (
	"mime"
	"net/mail"
	"regexp"
	"strings"
)

func address(s string) string {
	a, e := mail.ParseAddress(strings.TrimSpace(s))
	if e != nil || a.Address != strings.TrimSpace(s) || !strings.Contains(a.Address, ".") {
		return ""
	}
	return strings.ToLower(a.Address)
}
func addressMatches(value, target string) bool {
	list, e := mail.ParseAddressList(value)
	if e == nil {
		for _, a := range list {
			if strings.EqualFold(a.Address, target) {
				return true
			}
		}
	}
	return strings.EqualFold(strings.TrimSpace(value), target)
}
func recipientMatches(m Message, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	if !addressMatches(m.To, target) {
		return false
	}
	parts := strings.Split(target, "@")
	if len(parts) != 2 || parts[1] != "icloud.com" {
		return true
	}
	encoded := parts[0] + "=" + parts[1]
	pattern := regexp.MustCompile(`(?i)(^|[^a-z0-9.%+_=-])` + regexp.QuoteMeta(encoded) + `($|[^a-z0-9.%+_=-])`)
	for _, name := range []string{"received-spf", "return-path", "authentication-results-original", "authentication-results"} {
		for _, v := range m.Headers[name] {
			if pattern.MatchString(v) {
				return true
			}
		}
	}
	if strings.Contains(parts[0], "+") {
		return false
	}
	for _, v := range m.Headers["x-icloud-hme"] {
		for _, part := range strings.Split(v, ";") {
			k, val, ok := strings.Cut(part, "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "p") && strings.EqualFold(strings.Trim(strings.TrimSpace(val), `"`), target) {
				return true
			}
		}
	}
	return false
}

func matchesAccountRecipient(account Account, m Message, target string) bool {
	if account.Provider == "mx" {
		for _, recipient := range m.EnvelopeRecipients {
			if strings.EqualFold(recipient, target) {
				return true
			}
		}
		return false
	}
	directICloud := account.Provider == "icloud" && strings.EqualFold(account.Email, target) && addressMatches(m.To, target)
	return directICloud || recipientMatches(m, target)
}
func platformMatches(m Message, p Platform) bool {
	from, e := mail.ParseAddress(m.From)
	if e != nil {
		return false
	}
	email := strings.ToLower(from.Address)
	sender := false
	for _, s := range p.Emails {
		if strings.EqualFold(email, s) {
			sender = true
		}
	}
	_, domain, _ := strings.Cut(email, "@")
	for _, s := range p.Domains {
		s = strings.ToLower(s)
		if domain == s || strings.HasSuffix(domain, "."+s) {
			sender = true
		}
	}
	if !sender {
		return false
	}
	if len(p.Keywords) == 0 {
		return true
	}
	subject := strings.ToLower(m.Subject)
	for _, s := range p.Keywords {
		if strings.Contains(subject, strings.ToLower(s)) {
			return true
		}
	}
	return false
}
func decodeHeader(s string) string {
	d := &mime.WordDecoder{}
	v, e := d.DecodeHeader(s)
	if e != nil {
		return s
	}
	return v
}

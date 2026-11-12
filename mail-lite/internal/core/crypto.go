package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func randomID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func randomToken() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func loadKey(dir, provided string) ([]byte, error) {
	if provided != "" {
		b, e := base64.RawURLEncoding.DecodeString(provided)
		if e != nil || len(b) != 32 {
			return nil, errors.New("DATA_KEY must be a base64url-encoded 32-byte key")
		}
		return b, nil
	}
	p := filepath.Join(dir, "data.key")
	b, e := os.ReadFile(p)
	if e == nil {
		if len(b) != 32 {
			return nil, errors.New("invalid data.key")
		}
		return b, nil
	}
	if !os.IsNotExist(e) {
		return nil, e
	}
	b = make([]byte, 32)
	if _, e = rand.Read(b); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(e) {
		return loadKey(dir, provided)
	}
	if e != nil {
		return nil, e
	}
	_, e = f.Write(b)
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return b, e
}
func seal(key, plain []byte) (string, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return "", e
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(g.Seal(nonce, nonce, plain, []byte("mail-lite/v1"))), nil
}
func unseal(key []byte, s string) ([]byte, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	v, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil || len(v) < g.NonceSize() {
		return nil, errors.New("invalid encrypted value")
	}
	return g.Open(nil, v[:g.NonceSize()], v[g.NonceSize():], []byte("mail-lite/v1"))
}
func signRef(key []byte, r MessageRef) string {
	b, _ := json.Marshal(r)
	s := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return s + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func parseRef(key []byte, s, account string) (MessageRef, error) {
	var r MessageRef
	p := strings.Split(s, ".")
	if len(p) != 2 {
		return r, errors.New("invalid message reference")
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(p[0]))
	sig, e := base64.RawURLEncoding.DecodeString(p[1])
	if e != nil || !hmac.Equal(sig, m.Sum(nil)) {
		return r, errors.New("invalid message reference")
	}
	b, e := base64.RawURLEncoding.DecodeString(p[0])
	if e != nil {
		return r, e
	}
	e = json.Unmarshal(b, &r)
	if e != nil || r.AccountID != account || (r.Folder != "inbox" && r.Folder != "junkemail") {
		return r, errors.New("message reference does not belong to this mailbox")
	}
	if r.Protocol == "imap" && (r.UID == 0 || r.UIDValidity == 0 || r.Mailbox == "") {
		return r, errors.New("invalid IMAP reference")
	}
	if r.Protocol == "graph" && (r.ID == "" || len(r.ID) > 2048) {
		return r, errors.New("invalid Graph reference")
	}
	if r.Protocol == "mx" {
		if _, err := mxSourceID(r); err != nil {
			return r, err
		}
	}
	if r.Protocol != "imap" && r.Protocol != "graph" && r.Protocol != "mx" {
		return r, errors.New("unknown message protocol")
	}
	return r, nil
}

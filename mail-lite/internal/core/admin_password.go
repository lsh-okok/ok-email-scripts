package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

func validAdminPassword(password string) bool {
	return utf8.RuneCountInString(password) >= 12 && len(password) <= 72
}

func (a *App) initAdminPassword() error {
	var hash string
	err := a.store.AuxGet("auth", "password", &hash)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	configured := a.cfg.AdminPassword
	if configured == "" {
		if err != nil || hash == "" {
			return errors.New("set ADMIN_PASSWORD (at least 12 characters, at most 72 bytes) for the first startup")
		}
		a.password = []byte(hash)
		return nil
	}
	if !validAdminPassword(configured) {
		return errors.New("ADMIN_PASSWORD must contain at least 12 characters and at most 72 bytes")
	}
	// Track the last configured password separately from the active one. An
	// unchanged environment must not undo a password changed in the admin UI.
	var environmentHash string
	err = a.store.AuxGet("auth", "environment_password", &environmentHash)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if hash != "" && bcrypt.CompareHashAndPassword([]byte(environmentHash), []byte(configured)) == nil {
		a.password = []byte(hash)
		return nil
	}
	changed := bcrypt.CompareHashAndPassword([]byte(hash), []byte(configured)) != nil
	if changed {
		generated, err := bcrypt.GenerateFromPassword([]byte(configured), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		hash = string(generated)
	}
	if err := a.persistAdminPassword(hash, hash, changed); err != nil {
		return err
	}
	a.password = []byte(hash)
	return nil
}

// The password and session revocation commit together. Auth data stays local
// to the primary and never enters the public-record replication stream.
func (a *App) persistAdminPassword(hash, environmentHash string, revokeSessions bool) error {
	a.store.write.Lock()
	defer a.store.write.Unlock()
	tx, err := a.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	values := map[string]string{"password": hash}
	if environmentHash != "" {
		values["environment_password"] = environmentHash
	}
	for key, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		encrypted, err := seal(a.key, data)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO aux(bucket,key,data) VALUES('auth',?,?) ON CONFLICT(bucket,key) DO UPDATE SET data=excluded.data", key, encrypted); err != nil {
			return err
		}
	}
	if revokeSessions {
		if _, err = tx.Exec("DELETE FROM aux WHERE bucket='session'"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *App) changeAdminPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
		Confirm string `json:"confirm_password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !validAdminPassword(body.New) {
		fail(w, errors.New("新密码至少 12 个字符，且不能超过 72 字节"))
		return
	}
	if body.New != body.Confirm {
		fail(w, errors.New("两次输入的新密码不一致"))
		return
	}
	if body.Current == body.New {
		fail(w, errors.New("新密码不能与当前密码相同"))
		return
	}
	if !a.allowPasswordAttempt(w, r) {
		return
	}
	a.passwordMu.Lock()
	defer a.passwordMu.Unlock()
	// A concurrent password change may have invalidated this request's session
	// while it was waiting for the lock.
	if _, err := a.loadSession(r); err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "请重新登录"})
		return
	}
	if bcrypt.CompareHashAndPassword(a.password, []byte(body.Current)) != nil {
		fail(w, errors.New("当前密码不正确"))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.New), bcrypt.DefaultCost)
	if err != nil {
		fail(w, err)
		return
	}
	if err := a.persistAdminPassword(string(hash), "", true); err != nil {
		fail(w, err)
		return
	}
	a.password = hash
	a.adminPages.clear()
	http.SetCookie(w, &http.Cookie{Name: "ml_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil || strings.HasPrefix(a.cfg.ExternalOrigin, "https://"), SameSite: http.SameSiteStrictMode})
	respond(w, http.StatusOK, map[string]bool{"ok": true})
}

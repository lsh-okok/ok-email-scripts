package core

import (
	"html/template"
	"net/http"
)

func (a *App) redemptionPage(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Role != "primary" {
		respond(w, http.StatusForbidden, map[string]string{"error": "请在主节点兑换卡密"})
		return
	}
	t, err := template.ParseFS(a.assets, "templates/redeem.html")
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "兑换页面暂时不可用"})
		return
	}
	nonce := randomToken()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	if err := t.Execute(w, struct{ Nonce string }{nonce}); err != nil {
		return
	}
}

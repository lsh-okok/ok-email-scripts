package core

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

type tokenResponse struct {
	Token       string `json:"access_token"`
	Refresh     string `json:"refresh_token"`
	Expires     int    `json:"expires_in"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	ErrorCodes  []int  `json:"error_codes"`
	Description string `json:"error_description"`
}

const (
	graphOAuthURL        = "https://login.microsoftonline.com/common/oauth2/v2.0/token"
	imapOAuthURL         = "https://login.microsoftonline.com/consumers/oauth2/v2.0/token"
	graphConfiguredScope = "https://graph.microsoft.com/Mail.Read https://graph.microsoft.com/Mail.ReadWrite https://graph.microsoft.com/User.Read offline_access"
	graphReadScope       = "https://graph.microsoft.com/Mail.Read https://graph.microsoft.com/User.Read offline_access"
	graphDefaultScope    = "https://graph.microsoft.com/.default"
	imapTokenScope       = "https://outlook.office.com/IMAP.AccessAsUser.All offline_access"
)

var aadstsPattern = regexp.MustCompile(`(?i)aadsts([0-9]{4,8})`)

func tokenErrorCode(payload tokenResponse) int {
	if len(payload.ErrorCodes) > 0 {
		return payload.ErrorCodes[0]
	}
	if match := aadstsPattern.FindStringSubmatch(payload.Description); len(match) == 2 {
		code, _ := strconv.Atoi(match[1])
		return code
	}
	return 0
}

func tokenScopeRetryable(status int, payload tokenResponse) bool {
	if status != 400 && status != 401 && status != 403 {
		return false
	}
	switch payload.Error {
	case "invalid_scope", "consent_required", "interaction_required":
		return true
	}
	switch tokenErrorCode(payload) {
	case 90023, 70000, 70011:
		return true
	}
	description := strings.ToLower(payload.Description)
	for _, marker := range []string{"no applicable permissions", "requested are unauthorized or expired", "consent", "invalid scope"} {
		if strings.Contains(description, marker) {
			return true
		}
	}
	return false
}

func tokenScopes(kind string, originalScope bool) []string {
	if kind == "imap" {
		return []string{imapTokenScope}
	}
	scopes := []string{graphConfiguredScope}
	if kind != "write" {
		scopes = append(scopes, graphReadScope)
	}
	scopes = append(scopes, graphDefaultScope)
	if originalScope && kind != "write" {
		scopes = append(scopes, "")
	}
	return scopes
}

func (c Config) tokenEndpoint(kind string) string {
	if kind == "imap" {
		if c.IMAPOAuthURL != "" {
			return c.IMAPOAuthURL
		}
		if c.OAuthURL == "" || c.OAuthURL == graphOAuthURL {
			return imapOAuthURL
		}
	}
	if c.OAuthURL != "" {
		return c.OAuthURL
	}
	return graphOAuthURL
}

// Never include the raw response or transport error: either can contain
// credentials, proxy passwords or request URLs.
func tokenResponseError(status int, payload tokenResponse, kind string) error {
	reason := "Microsoft 授权请求失败"
	switch payload.Error {
	case "invalid_client":
		reason = "Client ID 无效或应用配置不符合要求"
	case "invalid_grant":
		reason = "Refresh Token 无法用于当前请求，请核对 Client ID 和授权状态"
	case "invalid_scope":
		reason = "请求的权限范围不适用于该应用或未获授权，请核对原授权权限"
	case "interaction_required", "consent_required":
		reason = "需要重新登录 Microsoft 并同意所需权限"
	case "unauthorized_client", "access_denied":
		reason = "应用无权访问该邮箱，请检查应用配置和用户授权"
	case "temporarily_unavailable", "server_error":
		reason = "Microsoft 授权服务暂时不可用，请稍后重试"
	}
	if tokenErrorCode(payload) == 70000 || tokenErrorCode(payload) == 90023 {
		reason = "请求的权限范围未获授权，请核对原授权权限"
	}
	switch tokenErrorCode(payload) {
	case 700082, 700084, 70043, 70008, 50173:
		reason = "Refresh Token 已失效或被撤销，请重新授权邮箱"
	}
	if status == 429 {
		reason = "Microsoft 授权服务正在限流，请稍后重试"
	} else if status >= 500 {
		reason = "Microsoft 授权服务暂时不可用，请稍后重试"
	}
	code := fmt.Sprintf("HTTP %d", status)
	if number := tokenErrorCode(payload); number != 0 {
		code += fmt.Sprintf(" / AADSTS%d", number)
	}
	if kind == "write" {
		reason += "；删除邮件需要 Mail.ReadWrite 权限"
	}
	return fmt.Errorf("%s（%s）", reason, code)
}

func tokenConnectionError(err error, proxyURL string) error {
	route := "直连"
	if proxyURL != "" {
		route = "邮箱代理"
	}
	reason := "网络连接失败，请检查网络或代理"
	var dns *net.DNSError
	var timeout net.Error
	var cert *tls.CertificateVerificationError
	switch {
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM), errors.Is(err, syscall.Errno(10013)):
		reason = "网络连接被系统拒绝，请检查防火墙或进程网络限制"
	case errors.As(err, &dns):
		reason = "域名解析失败，请检查 DNS 和代理"
	case errors.As(err, &cert):
		reason = "TLS 证书校验失败，请检查系统证书、时间或代理"
	case errors.As(err, &timeout) && timeout.Timeout():
		reason = "连接超时，请检查网络或代理"
	case errors.Is(err, syscall.ECONNREFUSED):
		reason = "连接被拒绝，请检查网络或代理是否启动"
	}
	return fmt.Errorf("无法连接 Microsoft 授权服务（%s）：%s", route, reason)
}

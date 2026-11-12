package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type readableLinkView struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	JSONURL     string `json:"json_url"`
	AccountCode string `json:"account_code"`
}

func readReadableLink(t *testing.T, a *App, cookie *http.Cookie, link Link) readableLinkView {
	t.Helper()
	w := request(t, a.Handler(), "GET", "/api/admin/links?q="+url.QueryEscape(link.Token), nil, cookie, "")
	var out struct{ Items []readableLinkView }
	if e := json.Unmarshal(w.Body.Bytes(), &out); w.Code != 200 || e != nil || len(out.Items) != 1 {
		t.Fatalf("read admin link: %d %s (%v)", w.Code, w.Body.String(), e)
	}
	return out.Items[0]
}

func readablePath(t *testing.T, raw string) string {
	t.Helper()
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" {
		t.Fatalf("invalid generated public URL %q: %v", raw, e)
	}
	return u.EscapedPath()
}

type readableMail struct {
	*fakeMail
	recipient string
}

func (f *readableMail) List(_ context.Context, account Account, _ []string, _ int) ([]Message, error) {
	return []Message{
		{Subject: "selected", From: "verify@example.net", To: f.recipient, ReceivedAt: time.Now(), Folder: "inbox", Source: MessageRef{AccountID: account.ID, Protocol: "graph", ID: "selected", Folder: "inbox"}},
		{Subject: "other recipient", From: "verify@example.net", To: "other@example.net", ReceivedAt: time.Now().Add(-time.Minute), Folder: "inbox", Source: MessageRef{AccountID: account.ID, Protocol: "graph", ID: "other", Folder: "inbox"}},
		{Subject: "other platform", From: "other@example.net", To: f.recipient, ReceivedAt: time.Now().Add(-2 * time.Minute), Folder: "inbox", Source: MessageRef{AccountID: account.ID, Protocol: "graph", ID: "platform", Folder: "inbox"}},
	}, nil
}

func TestReadableLinksBindMasterRecipientAndPlatform(t *testing.T) {
	a := testApp(t, "primary")
	mainID, otherID := randomID(), randomID()
	main := accountFixture(t, a, mainID)
	other := accountFixture(t, a, otherID)
	const recipient = "child+shopping@example.net"
	a.mail.backend = &readableMail{fakeMail: &fakeMail{key: a.key}, recipient: recipient}
	platform := Platform{ID: randomID(), Name: "购物 验证", Emails: []string{"verify@example.net"}}
	if _, e := a.store.Save(context.Background(), platform, nil); e != nil {
		t.Fatal(e)
	}
	link := linkFixture(t, a, mainID, recipient)
	link.PlatformID = platform.ID
	if _, e := a.store.Save(context.Background(), link, nil); e != nil {
		t.Fatal(e)
	}
	cookie, _ := loginFixture(t, a)
	view := readReadableLink(t, a, cookie, link)
	u, _ := url.Parse(view.URL)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "show" || parts[2] != main.PublicCode || len(parts[2]) != 10 || parts[3] != "child+shopping" || !strings.HasSuffix(parts[1], "~"+platform.ID) || view.AccountCode != main.PublicCode {
		t.Fatalf("URL does not identify the selected master and child: %+v", view)
	}
	jsonPath := readablePath(t, view.JSONURL)
	w := request(t, a.Handler(), "GET", jsonPath+"?limit=20", nil, nil, "")
	var mail struct {
		Recipient string    `json:"recipient"`
		Items     []Message `json:"items"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &mail); w.Code != 200 || e != nil || mail.Recipient != recipient || len(mail.Items) != 1 || mail.Items[0].Subject != "selected" {
		t.Fatalf("new route lost exact recipient/platform filtering: %d %s (%v)", w.Code, w.Body.String(), e)
	}
	// '+' in an address is a literal path character, including when percent-encoded.
	if w = request(t, a.Handler(), "GET", strings.ReplaceAll(jsonPath, "+", "%2B"), nil, nil, ""); w.Code != 200 {
		t.Fatalf("percent-encoded child address failed: %d %s", w.Code, w.Body.String())
	}
	pagePath := readablePath(t, view.URL)
	for _, path := range []string{
		strings.Replace(pagePath, "/"+main.PublicCode+"/", "/"+other.PublicCode+"/", 1),
		"/m/" + unrestrictedPlatformSegment + "/" + mainID + "/" + url.PathEscape(recipient),
		"/m/other~" + randomID() + "/" + mainID + "/" + url.PathEscape(recipient),
		"/m/" + url.PathEscape(parts[1]) + "/" + mainID + "/unregistered@example.net",
	} {
		if w = request(t, a.Handler(), "GET", path, nil, nil, ""); w.Code != 404 {
			t.Fatalf("unbound path returned %d instead of 404: %s", w.Code, path)
		}
	}
	if w = request(t, a.Handler(), "GET", pagePath, nil, nil, ""); w.Code != 200 {
		t.Fatalf("readable HTML page failed: %d %s", w.Code, w.Body.String())
	}
	if w = request(t, a.Handler(), "GET", "/api/mailboxes/"+link.Token+"/messages", nil, nil, ""); w.Code != 200 {
		t.Fatalf("existing token URL stopped working: %d %s", w.Code, w.Body.String())
	}
	stats := readAccessStats(t, a, cookie, "q="+url.QueryEscape(view.URL))
	if stats.ItemCount != 1 || len(stats.Items) != 1 || stats.Items[0].LinkID != link.ID || stats.Items[0].PageCount != 1 || stats.Items[0].JSONCount != 3 || stats.Items[0].Count != 4 {
		t.Fatalf("old and readable URLs did not share one access counter: %+v", stats)
	}
	for _, raw := range []string{view.URL, view.JSONURL} {
		w = request(t, a.Handler(), "GET", "/api/admin/links?q="+url.QueryEscape(raw), nil, cookie, "")
		var found struct{ Items []readableLinkView }
		if e := json.Unmarshal(w.Body.Bytes(), &found); w.Code != 200 || e != nil || len(found.Items) != 1 || found.Items[0].ID != link.ID {
			t.Fatalf("pasted concrete URL search failed: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestPublicSingleMessageLookupByMicrosoftIDAndRef(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, randomID())
	installFake(a)
	link := linkFixture(t, a, account.ID, "recipient000@example.net")
	cookie, _ := loginFixture(t, a)
	view := readReadableLink(t, a, cookie, link)
	path := readablePath(t, view.JSONURL)

	var payload struct {
		Items []Message `json:"items"`
	}
	w := request(t, a.Handler(), "GET", path+"?id=0", nil, nil, "")
	if e := json.Unmarshal(w.Body.Bytes(), &payload); w.Code != 200 || e != nil || len(payload.Items) != 1 || payload.Items[0].Subject != "验证邮件 0" {
		t.Fatalf("single Graph ID lookup failed: %d %s (%v)", w.Code, w.Body.String(), e)
	}

	list, e := a.mail.List(context.Background(), account)
	if e != nil || len(list) == 0 {
		t.Fatalf("list messages: %v", e)
	}
	payload = struct {
		Items []Message `json:"items"`
	}{}
	w = request(t, a.Handler(), "GET", path+"?ref="+url.QueryEscape(list[0].Ref), nil, nil, "")
	if e := json.Unmarshal(w.Body.Bytes(), &payload); w.Code != 200 || e != nil || len(payload.Items) != 1 {
		t.Fatalf("single signed reference lookup failed: %d %s (%v)", w.Code, w.Body.String(), e)
	}
	if w = request(t, a.Handler(), "GET", path+"?id=0", nil, nil, ""); !strings.Contains(w.Body.String(), `"message_id":"0"`) {
		t.Fatalf("message ID was not returned: %s", w.Body.String())
	}
}

func TestGraphRecipientQueryUsesBothPublicFolders(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, randomID())
	const recipient = "child@example.net"
	folders := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			respond(w, 200, map[string]any{"access_token": "access", "expires_in": 3600})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/messages") {
			folder := "inbox"
			if strings.Contains(r.URL.Path, "/junkemail/") {
				folder = "junkemail"
			}
			folders[folder] = true
			if !strings.Contains(r.URL.Query().Get("$filter"), "toRecipients/any") || !strings.Contains(r.URL.Query().Get("$filter"), recipient) {
				t.Fatalf("missing recipient filter: %s", r.URL.RawQuery)
			}
			respond(w, 200, map[string]any{"value": []map[string]any{{
				"id": "id-" + folder, "subject": folder, "receivedDateTime": "2026-10-24T00:00:00Z",
				"from":         map[string]any{"emailAddress": map[string]string{"address": "sender@example.net"}},
				"toRecipients": []map[string]any{{"emailAddress": map[string]string{"address": recipient}}},
				"bodyPreview":  "preview", "internetMessageHeaders": []map[string]string{},
			}}})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	a.cfg.GraphURL = server.URL + "/v1.0"
	a.cfg.OAuthURL = server.URL + "/token"
	values, e := (&liveBackend{app: a}).graphListRecipient(context.Background(), account, []string{""}, 10, recipient)
	if e != nil || len(values) != 2 || !folders["inbox"] || !folders["junkemail"] {
		t.Fatalf("Graph recipient query did not cover both folders: %d %v %+v", len(values), e, folders)
	}
}

func TestLinksAllowOnePolicyPerPlatformForSameRecipient(t *testing.T) {
	a := testApp(t, "primary")
	account := randomID()
	accountFixture(t, a, account)
	const recipient = "child@example.net"
	linkFixture(t, a, account, recipient)
	firstPlatform := Platform{ID: randomID(), Name: "平台一"}
	secondPlatform := Platform{ID: randomID(), Name: "平台二"}
	for _, platform := range []Platform{firstPlatform, secondPlatform} {
		if _, e := a.store.Save(context.Background(), platform, nil); e != nil {
			t.Fatal(e)
		}
		v := Link{ID: randomID(), AccountID: account, Recipient: recipient, Token: randomToken(), PlatformID: platform.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
		v.TokenHash = digest(v.Token)
		if _, e := a.store.Save(context.Background(), v, nil); e != nil {
			t.Fatalf("same recipient should allow platform %s: %v", platform.ID, e)
		}
	}
	duplicate := Link{ID: randomID(), AccountID: account, Recipient: recipient, Token: randomToken(), PlatformID: firstPlatform.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	duplicate.TokenHash = digest(duplicate.Token)
	if _, e := a.store.Save(context.Background(), duplicate, nil); e == nil {
		t.Fatal("same recipient and platform policy should remain unique")
	}
	if _, e := a.store.Find(context.Background(), "kind='link' AND account_id=? AND recipient=?", account, recipient); e != nil {
		t.Fatalf("base link disappeared: %v", e)
	}
}

func TestUnrestrictedLinkCanSelectPlatformInPath(t *testing.T) {
	a := testApp(t, "primary")
	accountID := randomID()
	account := accountFixture(t, a, accountID)
	const recipient = "child@example.net"
	a.mail.backend = &readableMail{fakeMail: &fakeMail{key: a.key}, recipient: recipient}
	platform := Platform{ID: randomID(), Name: "GitHub", Emails: []string{"verify@example.net"}}
	if _, e := a.store.Save(context.Background(), platform, nil); e != nil {
		t.Fatal(e)
	}
	link := linkFixture(t, a, accountID, recipient)
	cookie, _ := loginFixture(t, a)
	view := readReadableLink(t, a, cookie, link)
	if !strings.Contains(view.URL, "/"+unrestrictedPlatformSegment+"/") {
		t.Fatalf("unrestricted link did not use opaque platform segment: %s", view.URL)
	}
	platformPath := "/show/" + url.PathEscape(platform.Name+"~"+platform.ID) + "/" + account.PublicCode + "/child"
	jsonPath := "/api/mailboxes/" + url.PathEscape(platform.Name+"~"+platform.ID) + "/" + account.PublicCode + "/child/messages"
	var payload struct {
		Items []Message `json:"items"`
	}
	w := request(t, a.Handler(), "GET", jsonPath, nil, nil, "")
	if e := json.Unmarshal(w.Body.Bytes(), &payload); w.Code != 200 || e != nil || len(payload.Items) != 1 || payload.Items[0].Subject != "selected" {
		t.Fatalf("platform segment did not filter the shared link: %d %s (%v)", w.Code, w.Body.String(), e)
	}
	if w = request(t, a.Handler(), "GET", platformPath, nil, nil, ""); w.Code != 200 {
		t.Fatalf("platform page variant failed: %d %s", w.Code, w.Body.String())
	}
	var found struct {
		Items []readableLinkView `json:"items"`
	}
	w = request(t, a.Handler(), "GET", "/api/admin/links?q="+url.QueryEscape("http://example.test"+platformPath), nil, cookie, "")
	if e := json.Unmarshal(w.Body.Bytes(), &found); w.Code != 200 || e != nil || len(found.Items) != 1 || found.Items[0].ID != link.ID {
		t.Fatalf("platform variant search did not find shared link: %d %s (%v)", w.Code, w.Body.String(), e)
	}
	unknown := "/m/unknown~" + randomID() + "/" + accountID + "/" + url.PathEscape(recipient)
	if w = request(t, a.Handler(), "GET", unknown, nil, nil, ""); w.Code != 404 {
		t.Fatalf("unknown platform variant returned %d", w.Code)
	}
	stats := readAccessStats(t, a, cookie, "q="+url.QueryEscape(view.URL))
	if stats.ItemCount != 1 || len(stats.Items) != 1 || stats.Items[0].LinkID != link.ID || stats.Items[0].Count != 2 {
		t.Fatalf("platform variants did not share one access counter: %+v", stats)
	}
}

func TestLinkExpiryEditingAuthorizationAndAtomicity(t *testing.T) {
	a := testApp(t, "primary")
	mainID := randomID()
	accountFixture(t, a, mainID)
	installFake(a)
	first := linkFixture(t, a, mainID, "recipient000@example.net")
	second := linkFixture(t, a, mainID, "recipient001@example.net")
	cookie, csrf := loginFixture(t, a)
	view := readReadableLink(t, a, cookie, first)
	path := readablePath(t, view.JSONURL)
	expired := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	body := map[string]any{"ids": []string{first.ID}, "action": "expiry", "value": expired}
	for _, test := range []struct {
		cookie *http.Cookie
		csrf   string
		status int
	}{{nil, "", 401}, {cookie, "", 403}} {
		w := request(t, a.Handler(), "POST", "/api/admin/links/batch", body, test.cookie, test.csrf)
		if w.Code != test.status {
			t.Fatalf("unauthorized expiry mutation returned %d, want %d", w.Code, test.status)
		}
	}
	if w := request(t, a.Handler(), "GET", path, nil, nil, ""); w.Code != 200 {
		t.Fatalf("rejected expiry update changed public access: %d", w.Code)
	}
	if w := request(t, a.Handler(), "POST", "/api/admin/links/batch", body, cookie, csrf); w.Code != 200 {
		t.Fatalf("expiry edit failed: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, a.Handler(), "GET", path, nil, nil, ""); w.Code != 410 {
		t.Fatalf("updated expiry not enforced immediately: %d", w.Code)
	}
	body["value"] = "not-a-date"
	if w := request(t, a.Handler(), "POST", "/api/admin/links/batch", body, cookie, csrf); w.Code != 400 {
		t.Fatalf("invalid date accepted: %d %s", w.Code, w.Body.String())
	}
	body["value"] = ""
	body["ids"] = []string{first.ID, "missing-link"}
	if w := request(t, a.Handler(), "POST", "/api/admin/links/batch", body, cookie, csrf); w.Code != 404 {
		t.Fatalf("missing batch member returned %d: %s", w.Code, w.Body.String())
	}
	if w := request(t, a.Handler(), "GET", path, nil, nil, ""); w.Code != 410 {
		t.Fatalf("failed batch partially cleared expiry: %d", w.Code)
	}
	body["ids"] = []string{first.ID}
	if w := request(t, a.Handler(), "POST", "/api/admin/links/batch", body, cookie, csrf); w.Code != 200 {
		t.Fatalf("permanent expiry edit failed: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, a.Handler(), "GET", path, nil, nil, ""); w.Code != 200 {
		t.Fatalf("cleared expiry did not restore public access: %d", w.Code)
	}
	secondView := readReadableLink(t, a, cookie, second)
	if w := request(t, a.Handler(), "GET", readablePath(t, secondView.JSONURL), nil, nil, ""); w.Code != 200 {
		t.Fatalf("unselected link changed: %d", w.Code)
	}
}

func TestReadableLinkExpirySyncsToReplica(t *testing.T) {
	a := testApp(t, "primary")
	mainID := randomID()
	accountFixture(t, a, mainID)
	link := linkFixture(t, a, mainID, "recipient000@example.net")
	shared := installFake(a)
	node := Node{ID: "readable-link-node", Token: randomToken()}
	if e := a.store.AuxPut("node", node.ID, node); e != nil {
		t.Fatal(e)
	}
	primary := httptest.NewServer(a.Handler())
	defer primary.Close()
	replica := testApp(t, "replica")
	replica.cfg.MasterURL, replica.cfg.NodeID, replica.cfg.NodeToken = primary.URL, node.ID, node.Token
	replica.mail.backend = shared
	if e := replica.syncOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	cookie, csrf := loginFixture(t, a)
	view := readReadableLink(t, a, cookie, link)
	path := readablePath(t, view.JSONURL)
	if w := request(t, replica.Handler(), "GET", path, nil, nil, ""); w.Code != 200 {
		t.Fatalf("readable link missing after initial snapshot: %d %s", w.Code, w.Body.String())
	}
	// Accounts created after the snapshot must publish the same random code via
	// incremental replication, rather than generating another code on the replica.
	nextAccount := accountFixture(t, a, randomID())
	nextLink := linkFixture(t, a, nextAccount.ID, "recipient001@example.net")
	nextLink.AccountCode = nextAccount.PublicCode
	if e := replica.syncOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	nextReplicaAccount, e := replica.store.Account(context.Background(), nextAccount.ID)
	if e != nil || nextReplicaAccount.PublicCode != nextAccount.PublicCode {
		t.Fatalf("incremental replication changed public code: %v", e)
	}
	_, nextJSONURL := publicLinkURLs("http://example.test", nextLink, "")
	if w := request(t, replica.Handler(), "GET", readablePath(t, nextJSONURL), nil, nil, ""); w.Code != 200 {
		t.Fatalf("new short link missing after incremental sync: %d %s", w.Code, w.Body.String())
	}
	body := map[string]any{"ids": []string{link.ID}, "action": "expiry", "value": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}
	if w := request(t, a.Handler(), "POST", "/api/admin/links/batch", body, cookie, csrf); w.Code != 200 {
		t.Fatalf("expiry edit failed: %d %s", w.Code, w.Body.String())
	}
	if e := replica.syncOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	if w := request(t, replica.Handler(), "GET", path, nil, nil, ""); w.Code != 410 {
		t.Fatalf("replica served expired link after incremental sync: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, replica.Handler(), "POST", "/api/admin/links/batch", body, cookie, csrf); w.Code != 403 {
		t.Fatalf("replica accepted admin expiry mutation: %d", w.Code)
	}
	body["value"] = ""
	if w := request(t, a.Handler(), "POST", "/api/admin/links/batch", body, cookie, csrf); w.Code != 200 {
		t.Fatalf("expiry reset failed: %d %s", w.Code, w.Body.String())
	}
	if e := replica.syncOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	if w := request(t, replica.Handler(), "GET", path, nil, nil, ""); w.Code != 200 {
		t.Fatalf("replica did not apply permanent expiry: %d %s", w.Code, w.Body.String())
	}
}

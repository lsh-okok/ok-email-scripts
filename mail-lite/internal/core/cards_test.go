package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func createCardFixture(t *testing.T, a *App, accountIDs ...string) []cardRow {
	t.Helper()
	cookie, csrf := loginFixture(t, a)
	w := request(t, a.Handler(), "POST", "/api/admin/cards", map[string]any{"account_ids": accountIDs}, cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("create cards: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Items []cardRow `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Items
}

func redeemCardsFixture(t *testing.T, a *App, codes ...string) (*httptest.ResponseRecorder, []redemptionResult) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"codes": codes})
	r := httptest.NewRequest(http.MethodPost, "/api/redeem", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		return w, nil
	}
	var result struct {
		Items []redemptionResult `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return w, result.Items
}

func TestCardsGenerationListingAndEncryption(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "one")
	accountFixture(t, a, "two")
	linkFixture(t, a, "one", "child@example.net")
	generated := createCardFixture(t, a, "one", "two", "one")
	if len(generated) != 2 || generated[0].AccountID != "one" || generated[0].Code == generated[1].Code || len(generated[0].Code) < 40 {
		t.Fatalf("unexpected cards: %+v", generated)
	}
	repeated := createCardFixture(t, a, "one")
	if repeated[0].Code != generated[0].Code || repeated[0].CreatedAt != generated[0].CreatedAt {
		t.Fatal("generating again changed a mailbox's card")
	}
	var encrypted, hash string
	if err := a.store.db.QueryRow("SELECT code_encrypted,code_hash FROM redemption_cards WHERE account_id='one'").Scan(&encrypted, &hash); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, generated[0].Code) || hash != digest(generated[0].Code) {
		t.Fatal("card was not encrypted and indexed by its hash")
	}
	accountFixture(t, a, "three")
	cookie, _ := loginFixture(t, a)
	w := request(t, a.Handler(), "GET", "/api/admin/cards?status=unissued&page_size=1", nil, cookie, "")
	var page struct {
		Items   []cardRow      `json:"items"`
		Total   int            `json:"total"`
		Summary map[string]int `json:"summary"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || page.Total != 1 || len(page.Items) != 1 || page.Items[0].AccountID != "three" || page.Summary["unused"] != 2 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret-refresh") || strings.Contains(w.Body.String(), "client_id") {
		t.Fatal("card list leaked credentials")
	}
	w = request(t, a.Handler(), "GET", "/api/admin/cards?q="+generated[0].Code, nil, cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":1`) {
		t.Fatalf("search exact code: %s", w.Body.String())
	}
}

func TestCardsBatchRedemptionIsolationAndRecovery(t *testing.T) {
	a := testApp(t, "primary")
	one := accountFixture(t, a, "one")
	accountFixture(t, a, "two")
	accountFixture(t, a, "empty")
	linkFixture(t, a, "one", "first@example.net")
	linkFixture(t, a, "two", "second@example.net")
	settings := DefaultSettings()
	settings.PublicBaseURL = "https://public.example.test/mail"
	if _, err := a.store.Save(context.Background(), settings, nil); err != nil {
		t.Fatal(err)
	}
	cards := createCardFixture(t, a, "one", "two", "empty")
	w, results := redeemCardsFixture(t, a, cards[0].Code, "unknown", cards[1].Code, cards[0].Code, cards[2].Code)
	if w.Code != 200 || len(results) != 4 || !results[0].Success || results[1].Success || !results[2].Success || results[3].Success {
		t.Fatalf("redemption results: %d %s", w.Code, w.Body.String())
	}
	if results[0].AccountEmail != one.Email || len(results[0].Links) != 1 || results[0].Links[0].Recipient != "first@example.net" || !strings.HasPrefix(results[0].Links[0].URL, settings.PublicBaseURL+"/show/p0/") {
		t.Fatalf("wrong mailbox delivery: %+v", results[0])
	}
	if strings.Contains(w.Body.String(), "secret-refresh") || strings.Contains(w.Body.String(), `"client_id"`) || strings.Contains(w.Body.String(), `"password"`) {
		t.Fatal("redemption leaked credentials")
	}
	var firstRedeemed, emptyRedeemed string
	a.store.db.QueryRow("SELECT redeemed_at FROM redemption_cards WHERE account_id='one'").Scan(&firstRedeemed)
	a.store.db.QueryRow("SELECT redeemed_at FROM redemption_cards WHERE account_id='empty'").Scan(&emptyRedeemed)
	if firstRedeemed == "" || emptyRedeemed != "" {
		t.Fatal("successful and failed cards were not consumed correctly")
	}
	_, repeated := redeemCardsFixture(t, a, cards[0].Code)
	var after string
	a.store.db.QueryRow("SELECT redeemed_at FROM redemption_cards WHERE account_id='one'").Scan(&after)
	if !repeated[0].Success || !repeated[0].Reused || after != firstRedeemed || repeated[0].AccountEmail != one.Email {
		t.Fatal("repeated redemption did not recover the original mailbox")
	}
	linkFixture(t, a, "empty", "later@example.net")
	_, later := redeemCardsFixture(t, a, cards[2].Code)
	if !later[0].Success || later[0].Reused {
		t.Fatal("failed card could not be redeemed after links were added")
	}
}

func TestCardsRedemptionURLsBehindHTTPSProxy(t *testing.T) {
	a := testApp(t, "primary")
	a.cfg.ExternalOrigin = "https://mail.example"
	accountFixture(t, a, "one")
	linkFixture(t, a, "one", "child@example.net")
	card := createCardFixture(t, a, "one")[0]
	for _, test := range []struct {
		name, publicBase, wantBase string
	}{
		{"configured HTTPS origin", "", "https://mail.example"},
		{"explicit public URL takes precedence", "https://other.example/prefix", "https://other.example/prefix"},
	} {
		t.Run(test.name, func(t *testing.T) {
			settings := DefaultSettings()
			settings.PublicBaseURL = test.publicBase
			if _, err := a.store.Save(context.Background(), settings, nil); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]any{"codes": []string{card.Code}})
			r := httptest.NewRequest("POST", "http://internal-backend:8080/api/redeem", bytes.NewReader(body))
			if r.TLS != nil {
				t.Fatal("proxy fixture must reach the application over plain HTTP")
			}
			r.Header.Set("Origin", a.cfg.ExternalOrigin)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			var result struct {
				Items []redemptionResult `json:"items"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != 1 || !result.Items[0].Success || len(result.Items[0].Links) != 1 {
				t.Fatalf("proxy redemption: %d %s", w.Code, w.Body.String())
			}
			if got := result.Items[0].Links[0].URL; !strings.HasPrefix(got, test.wantBase+"/show/p0/") {
				t.Fatalf("expected public HTTPS base %q, got %q", test.wantBase, got)
			}
		})
	}
}

func TestCardsDisableExpiredAndDeletedSources(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, "one")
	link := linkFixture(t, a, account.ID, "child@example.net")
	card := createCardFixture(t, a, account.ID)[0]
	cookie, csrf := loginFixture(t, a)
	for _, enabled := range []bool{false, true} {
		w := request(t, a.Handler(), "PUT", "/api/admin/cards/one", map[string]any{"enabled": enabled}, cookie, csrf)
		if w.Code != 200 {
			t.Fatalf("set enabled: %s", w.Body.String())
		}
		_, results := redeemCardsFixture(t, a, card.Code)
		if results[0].Success != enabled {
			t.Fatalf("enabled=%v: %+v", enabled, results[0])
		}
	}
	w := request(t, a.Handler(), "PUT", "/api/admin/cards/one", map[string]any{"enabled": false}, cookie, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"disabled"`) {
		t.Fatal(w.Body.String())
	}
	w = request(t, a.Handler(), "PUT", "/api/admin/cards/one", map[string]any{"enabled": true}, cookie, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"redeemed"`) {
		t.Fatal("reenabling lost redemption history", w.Body.String())
	}
	link.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if _, err := a.store.Save(context.Background(), link, nil); err != nil {
		t.Fatal(err)
	}
	_, results := redeemCardsFixture(t, a, card.Code)
	if results[0].Success || len(results[0].Links) != 0 {
		t.Fatal("expired link was delivered")
	}
	link.ExpiresAt = ""
	a.store.Save(context.Background(), link, nil)
	account.Status = "disabled"
	a.store.Save(context.Background(), account, nil)
	_, results = redeemCardsFixture(t, a, card.Code)
	if results[0].Success {
		t.Fatal("disabled account delivered links")
	}
	if err := a.store.Delete(context.Background(), "account", []string{account.ID}); err != nil {
		t.Fatal(err)
	}
	_, results = redeemCardsFixture(t, a, card.Code)
	if results[0].Success || results[0].AccountEmail != "" {
		t.Fatal("deleted account delivered links")
	}
}

func TestCardsAtomicGenerationAndConcurrency(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "one")
	linkFixture(t, a, "one", "child@example.net")
	cookie, csrf := loginFixture(t, a)
	w := request(t, a.Handler(), "POST", "/api/admin/cards", map[string]any{"account_ids": []string{"one", "missing"}}, cookie, csrf)
	var count int
	a.store.db.QueryRow("SELECT count(*) FROM redemption_cards").Scan(&count)
	if w.Code == 200 || count != 0 {
		t.Fatal("invalid batch partially generated cards")
	}
	var wg sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses <- request(t, a.Handler(), "POST", "/api/admin/cards", map[string]any{"account_ids": []string{"one"}}, cookie, csrf)
		}()
	}
	wg.Wait()
	close(responses)
	code := ""
	for response := range responses {
		var result struct{ Items []cardRow }
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Items) != 1 {
			t.Fatalf("concurrent generation: %d %s", response.Code, response.Body.String())
		}
		if code == "" {
			code = result.Items[0].Code
		} else if result.Items[0].Code != code {
			t.Fatal("concurrent generation created multiple codes for a mailbox")
		}
	}
	a.store.db.QueryRow("SELECT count(*) FROM redemption_cards").Scan(&count)
	if count != 1 {
		t.Fatal("multiple cards persisted")
	}
	results := make(chan redemptionResult, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, items := redeemCardsFixture(t, a, code)
			if len(items) == 1 {
				results <- items[0]
			}
		}()
	}
	wg.Wait()
	close(results)
	fresh, total := 0, 0
	for item := range results {
		total++
		if !item.Success || item.AccountEmail != "one@outlook.com" {
			t.Fatalf("concurrent redemption: %+v", item)
		}
		if !item.Reused {
			fresh++
		}
	}
	if total != 8 || fresh != 1 {
		t.Fatalf("expected exactly one first redemption, got %d/%d", fresh, total)
	}
}

func TestCardsPublicInputOriginAuthAndRateLimit(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "one")
	card := createCardFixture(t, a, "one")[0]
	h := a.Handler()
	for _, test := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/admin/cards", nil},
		{"POST", "/api/admin/cards", map[string]any{"account_ids": []string{"one"}}},
		{"PUT", "/api/admin/cards/one", map[string]any{"enabled": false}},
	} {
		w := request(t, h, test.method, test.path, test.body, nil, "")
		if w.Code != 401 || strings.Contains(w.Body.String(), card.Code) {
			t.Fatalf("unauthenticated management: %d %s", w.Code, w.Body.String())
		}
	}
	cookie, _ := loginFixture(t, a)
	w := request(t, h, "POST", "/api/admin/cards", map[string]any{"account_ids": []string{"one"}}, cookie, "bad-csrf")
	if w.Code != 403 {
		t.Fatal("management accepted missing CSRF")
	}
	for _, test := range []struct {
		body, origin, content string
		status                int
	}{
		{`{"codes":["x"]}`, "https://attacker.example", "application/json", 403},
		{`{"codes":["x"]}`, "", "text/plain", 415},
		{`{"codes":[]}`, "", "application/json", 400},
		{`{"codes":[""]}`, "", "application/json", 400},
		{`{"codes":["x"],"account_id":"one"}`, "", "application/json", 400},
		{`{"codes":["x"]} {}`, "", "application/json", 400},
	} {
		r := httptest.NewRequest("POST", "/api/redeem", strings.NewReader(test.body))
		r.Header.Set("Origin", test.origin)
		r.Header.Set("Content-Type", test.content)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("input %+v: %d %s", test, w.Code, w.Body.String())
		}
	}
	for i := 0; i < 30; i++ {
		redeemCardsFixture(t, a, "unknown")
	}
	w, _ = redeemCardsFixture(t, a, "unknown")
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("redemption was not rate limited")
	}
	replica := testApp(t, "replica")
	w, _ = redeemCardsFixture(t, replica, card.Code)
	if w.Code != 403 {
		t.Fatal("replica accepted a redemption")
	}
}

func TestCardsAmbiguousRecipientUsesExactToken(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "one")
	one := linkFixture(t, a, "one", "same@first.example")
	two := linkFixture(t, a, "one", "same@second.example")
	card := createCardFixture(t, a, "one")[0]
	_, results := redeemCardsFixture(t, a, card.Code)
	if len(results) != 1 || !results[0].Success || len(results[0].Links) != 2 {
		t.Fatalf("ambiguous recipient links: %+v", results)
	}
	want := map[string]string{one.Recipient: one.Token, two.Recipient: two.Token}
	for _, item := range results[0].Links {
		if !strings.HasSuffix(item.URL, "/m/"+want[item.Recipient]) {
			t.Fatalf("ambiguous readable URL escaped exact recipient isolation: %s", item.URL)
		}
	}
}

func TestCardsRedemptionPagePrimaryAndReplica(t *testing.T) {
	a := testApp(t, "primary")
	w := request(t, a.Handler(), "GET", "/redeem", nil, nil, "")
	policy := w.Header().Get("Content-Security-Policy")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("redemption page headers: %d %+v", w.Code, w.Header())
	}
	_, noncePart, found := strings.Cut(policy, "script-src 'nonce-")
	nonce, _, complete := strings.Cut(noncePart, "'")
	if !found || !complete || nonce == "" || !strings.Contains(w.Body.String(), `nonce="`+nonce+`"`) || !strings.Contains(policy, "connect-src 'self'") {
		t.Fatal("redemption page is missing a matching script nonce and origin policy")
	}
	other := request(t, a.Handler(), "GET", "/redeem", nil, nil, "")
	if other.Header().Get("Content-Security-Policy") == policy {
		t.Fatal("redemption page reused its script nonce")
	}
	replica := testApp(t, "replica")
	w = request(t, replica.Handler(), "GET", "/redeem", nil, nil, "")
	if w.Code != 403 || strings.Contains(w.Body.String(), "<script") {
		t.Fatal("replica served a redemption page")
	}
}

package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mappedSourceFixture(t *testing.T, a *App, cookie *http.Cookie, csrf string) (Account, Link, int64) {
	t.Helper()
	account := accountFixture(t, a, "source-links")
	link := linkFixture(t, a, account.ID, "recipient000@example.net")
	inspectionDecode[mailMappingCommitResponse](t, request(t, a.Handler(), "POST", "/api/admin/mail/mapping", map[string]any{"include_links": true}, cookie, csrf), 200)
	var id int64
	if err := a.store.db.QueryRow("SELECT mailbox_id FROM mail_source_mappings WHERE source_kind='link' AND source_id=?", link.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return account, link, id
}

func sourceChildForTest(t *testing.T, a *App, cookie *http.Cookie, csrf string, id int64) MailChild {
	t.Helper()
	page := inspectionDecode[struct{ Items []MailChild }](t, request(t, a.Handler(), "GET", "/api/admin/mail/children?limit=100", nil, cookie, csrf), 200)
	for _, v := range page.Items {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("child %d absent", id)
	return MailChild{}
}

func TestMailSourceLinksAutomaticMappingDynamicURLAndSnapshots(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	_, _, id := mappedSourceFixture(t, a, cookie, csrf)
	c := sourceChildForTest(t, a, cookie, csrf, id)
	if c.LinkMode != "source" || !c.LinkAvailable || !c.Sellable || c.Link == "" || c.LinkPlatform != "全部平台" {
		t.Fatalf("source link not ready: %+v", c)
	}
	settings := a.store.Settings(context.Background())
	settings.PublicBaseURL = "https://first.example.test"
	if _, err := a.store.Save(context.Background(), settings, nil); err != nil {
		t.Fatal(err)
	}
	c = sourceChildForTest(t, a, cookie, csrf, id)
	if !strings.HasPrefix(c.Link, settings.PublicBaseURL+"/show/p0/") {
		t.Fatalf("public URL not generated: %+v", c)
	}
	export := inspectionDecode[struct{ Content string }](t, request(t, a.Handler(), "POST", "/api/admin/mail/export", map[string]any{"kind": "child", "ids": []int64{id}, "format": "txt"}, cookie, csrf), 200)
	if !strings.Contains(export.Content, c.Link) {
		t.Fatalf("inventory export missed generated URL: %s", export.Content)
	}
	body := map[string]any{"ids": []int64{id}, "quantity": 1, "mode": "sell", "request_key": "source-snapshot"}
	order := inspectionDecode[struct{ ID int64 }](t, request(t, a.Handler(), "POST", "/api/admin/mail/outbounds", body, cookie, csrf), 201)
	settings.PublicBaseURL = "https://second.example.test"
	if _, err := a.store.Save(context.Background(), settings, nil); err != nil {
		t.Fatal(err)
	}
	c = sourceChildForTest(t, a, cookie, csrf, id)
	if !strings.HasPrefix(c.Link, settings.PublicBaseURL) {
		t.Fatal("source URL did not follow public base")
	}
	detail := inspectionDecode[mailOutboundDetail](t, request(t, a.Handler(), "GET", fmt.Sprintf("/api/admin/mail/outbounds/%d", order.ID), nil, cookie, csrf), 200)
	if len(detail.Items) != 1 || !strings.HasPrefix(detail.Items[0].Link, "https://first.example.test/") {
		t.Fatalf("historical URL mutated: %+v", detail.Items)
	}
	if w := request(t, a.Handler(), "PUT", fmt.Sprintf("/api/admin/mail/children/%d", id), map[string]string{"link": "https://custom.example.test"}, cookie, csrf); w.Code == 200 {
		t.Fatal("sold link edited")
	}
}

func TestMailSourceLinkBackfillPreviewAtomicAndNonDestructive(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	_, link, id := mappedSourceFixture(t, a, cookie, csrf)
	// Simulate inventory created by the previous release. Notes are deliberately unrelated.
	if _, err := a.store.db.Exec("UPDATE mailboxes SET link_mode='custom',link='',note='user note' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/mail/children/link-sync"
	p := inspectionDecode[mailLinkSyncPreview](t, request(t, a.Handler(), "POST", path+"/preview", map[string]any{}, cookie, csrf), 200)
	if p.Ready != 1 || p.Failed != 0 || !strings.Contains(p.Items[0].Link, "/show/") {
		t.Fatalf("bad preview: %+v", p)
	}
	// A policy edit between preview and commit must be detected.
	link.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if _, err := a.store.Save(context.Background(), link, nil); err != nil {
		t.Fatal(err)
	}
	if w := request(t, a.Handler(), "POST", path, map[string]string{"version": p.Version}, cookie, csrf); w.Code != 409 {
		t.Fatalf("stale preview: %d %s", w.Code, w.Body.String())
	}
	p = inspectionDecode[mailLinkSyncPreview](t, request(t, a.Handler(), "POST", path+"/preview", map[string]any{}, cookie, csrf), 200)
	rejectMailActivityForTest(t, a, "mail.links.associated")
	if w := request(t, a.Handler(), "POST", path, map[string]string{"version": p.Version}, cookie, csrf); w.Code == 200 {
		t.Fatal("association succeeded without audit")
	}
	if c := sourceChildForTest(t, a, cookie, csrf, id); c.LinkMode != "custom" || c.Link != "" {
		t.Fatalf("failed association changed inventory: %+v", c)
	}
	allowMailActivityForTest(t, a)
	inspectionDecode[map[string]any](t, request(t, a.Handler(), "POST", path, map[string]string{"version": p.Version}, cookie, csrf), 200)
	c := sourceChildForTest(t, a, cookie, csrf, id)
	if c.LinkMode != "source" || !c.LinkAvailable || c.Note != "user note" {
		t.Fatalf("backfill failed: %+v", c)
	}
	p = inspectionDecode[mailLinkSyncPreview](t, request(t, a.Handler(), "POST", path+"/preview", map[string]any{}, cookie, csrf), 200)
	if p.Ready != 0 || p.Skipped != 1 {
		t.Fatalf("backfill not idempotent: %+v", p)
	}
	inspectionDecode[map[string]any](t, request(t, a.Handler(), "POST", path, map[string]string{"version": p.Version}, cookie, csrf), 200)
	if n := mailActivityCountForTest(t, a, "mail.links.associated"); n != 2 {
		t.Fatalf("unexpected audit count %d", n)
	}
	// A manual URL always remains custom and is excluded from repair.
	inspectionDecode[map[string]any](t, request(t, a.Handler(), "PUT", fmt.Sprintf("/api/admin/mail/children/%d", id), map[string]string{"link": "https://custom.example.test/inbox"}, cookie, csrf), 200)
	c = sourceChildForTest(t, a, cookie, csrf, id)
	if c.LinkMode != "custom" || c.Link != "https://custom.example.test/inbox" {
		t.Fatalf("manual override did not detach: %+v", c)
	}
	p = inspectionDecode[mailLinkSyncPreview](t, request(t, a.Handler(), "POST", path+"/preview", map[string]any{}, cookie, csrf), 200)
	if p.Ready != 0 || p.Skipped != 1 {
		t.Fatal("manual URL would be overwritten")
	}
	if _, err := a.store.db.Exec("UPDATE mailboxes SET status='reserved',link='' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	p = inspectionDecode[mailLinkSyncPreview](t, request(t, a.Handler(), "POST", path+"/preview", map[string]any{}, cookie, csrf), 200)
	if p.Ready != 0 || p.Skipped != 1 {
		t.Fatal("reserved inventory would be changed")
	}
}

func TestMailSourceLinkInvalidationAndReservedDelivery(t *testing.T) {
	for _, cause := range []string{"expired", "deleted", "disabled", "recipient", "platform"} {
		t.Run(cause, func(t *testing.T) {
			a := testApp(t, "primary")
			cookie, csrf := loginFixture(t, a)
			account, link, id := mappedSourceFixture(t, a, cookie, csrf)
			if cause == "platform" {
				p := Platform{ID: "platform", Name: "Service", Domains: []string{"example.net"}}
				if _, err := a.store.Save(context.Background(), p, nil); err != nil {
					t.Fatal(err)
				}
				link.PlatformID = p.ID
				if _, err := a.store.Save(context.Background(), link, nil); err != nil {
					t.Fatal(err)
				}
			}
			order := inspectionDecode[struct{ ID int64 }](t, request(t, a.Handler(), "POST", "/api/admin/mail/outbounds", map[string]any{"ids": []int64{id}, "quantity": 1, "mode": "reserve", "request_key": "source-reserve-" + cause}, cookie, csrf), 201)
			switch cause {
			case "expired":
				link.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
				if _, err := a.store.Save(context.Background(), link, nil); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				if err := a.store.Delete(context.Background(), "link", []string{link.ID}); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				account.Status = "disabled"
				if _, err := a.store.Save(context.Background(), account, nil); err != nil {
					t.Fatal(err)
				}
			case "recipient":
				link.Recipient = "somebody-else@example.net"
				if _, err := a.store.Save(context.Background(), link, nil); err != nil {
					t.Fatal(err)
				}
			case "platform":
				if _, err := a.store.db.Exec("DELETE FROM records WHERE kind='platform' AND id='platform'"); err != nil {
					t.Fatal(err)
				}
			}
			c := sourceChildForTest(t, a, cookie, csrf, id)
			if c.LinkAvailable || c.LinkReason == "" {
				t.Fatalf("invalid source not reported: %+v", c)
			}
			w := request(t, a.Handler(), "POST", fmt.Sprintf("/api/admin/mail/outbounds/%d/action", order.ID), map[string]string{"action": "deliver"}, cookie, csrf)
			if w.Code == 200 {
				t.Fatalf("delivered invalid source: %s", w.Body.String())
			}
			inspectionDecode[map[string]any](t, request(t, a.Handler(), "POST", fmt.Sprintf("/api/admin/mail/outbounds/%d/action", order.ID), map[string]string{"action": "cancel", "reason": "invalid source"}, cookie, csrf), 200)
			stats, err := readMailStats(a.store.db)
			if err != nil || stats.Sellable != 0 {
				t.Fatalf("invalid source counted as sellable: %+v %v", stats, err)
			}
			w = request(t, a.Handler(), "POST", "/api/admin/mail/outbounds", map[string]any{"ids": []int64{id}, "quantity": 1, "mode": "sell", "request_key": "invalid-" + cause}, cookie, csrf)
			if w.Code == 201 {
				t.Fatal("invalid source sold")
			}
		})
	}
}

func TestMailSourceLinksMatchCanonicalPolicyAndFullRecipientFallback(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	account, source, id := mappedSourceFixture(t, a, cookie, csrf)
	p := Platform{ID: "service", Name: "Service", Domains: []string{"example.net"}}
	if _, err := a.store.Save(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
	// Bind the mapped source to a platform and add an unrestricted alternative.
	source.PlatformID = p.ID
	source.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if _, err := a.store.Save(context.Background(), source, nil); err != nil {
		t.Fatal(err)
	}
	unrestricted := linkFixture(t, a, account.ID, source.Recipient)
	s, err := a.sourceLink(context.Background(), a.store.db, id, "http://127.0.0.1:8081", true)
	if err != nil || !s.LinkAvailable || s.Effective.ID != unrestricted.ID || s.Effective.PlatformID != p.ID || s.ExpiresAt != "" || !s.LocalOnly {
		t.Fatalf("canonical policy differs: %+v %v", s, err)
	}
	linkFixture(t, a, account.ID, "recipient000@other.net")
	s, err = a.sourceLink(context.Background(), a.store.db, id, "https://mail.example.test", true)
	if err != nil || !s.LinkAvailable || !strings.HasSuffix(s.Link, "/recipient000@example.net") {
		t.Fatalf("ambiguous localpart not repaired: %+v %v", s, err)
	}
	unrestricted.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if _, err := a.store.Save(context.Background(), unrestricted, nil); err != nil {
		t.Fatal(err)
	}
	s, err = a.sourceLink(context.Background(), a.store.db, id, "https://mail.example.test", true)
	if err != nil || s.LinkAvailable || s.LinkReason != "取信链接已过期" {
		t.Fatalf("effective expiration ignored: %+v %v", s, err)
	}
}

func TestMailSourceReservedSnapshotAndInspectionRevalidation(t *testing.T) {
	for _, change := range []string{"ambiguous", "inspection", "domain"} {
		t.Run(change, func(t *testing.T) {
			a := testApp(t, "primary")
			cookie, csrf := loginFixture(t, a)
			account, source, id := mappedSourceFixture(t, a, cookie, csrf)
			a.mail.backend = &fakeMail{}
			inspect := func(includeReserved bool) {
				created := inspectionDecode[struct{ ID int64 }](t, request(t, a.Handler(), "POST", "/api/admin/mail/inspections", inspectionInput{Mode: "link", Scope: "selected", IDs: []int64{id}, IncludeSold: includeReserved, Options: inspectionTestOptions()}, cookie, csrf), 201)
				if task := waitInspectionTask(t, a, created.ID); task.Passed != 1 {
					t.Fatalf("source inspection failed: %+v", task)
				}
			}
			body := map[string]any{"ids": []int64{id}, "quantity": 1, "mode": "reserve", "request_key": "reserve-snapshot-" + change}
			if change == "inspection" {
				inspect(false)
				body["inspection"], body["valid_hours"] = "link", 24
			}
			order := inspectionDecode[struct{ ID int64 }](t, request(t, a.Handler(), "POST", "/api/admin/mail/outbounds", body, cookie, csrf), 201)
			detailPath := fmt.Sprintf("/api/admin/mail/outbounds/%d", order.ID)
			before := inspectionDecode[mailOutboundDetail](t, request(t, a.Handler(), "GET", detailPath, nil, cookie, csrf), 200)
			switch change {
			case "ambiguous":
				linkFixture(t, a, account.ID, "recipient000@other.net")
			case "inspection":
				source.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
				if _, err := a.store.Save(context.Background(), source, nil); err != nil {
					t.Fatal(err)
				}
			case "domain":
				settings := a.store.Settings(context.Background())
				settings.PublicBaseURL = "https://new.example.test"
				if _, err := a.store.Save(context.Background(), settings, nil); err != nil {
					t.Fatal(err)
				}
			}
			w := request(t, a.Handler(), "POST", detailPath+"/action", map[string]string{"action": "deliver"}, cookie, csrf)
			want := 409
			if change == "domain" {
				want = 200
			}
			if w.Code != want {
				t.Fatalf("delivery after %s: HTTP %d %s", change, w.Code, w.Body.String())
			}
			after := inspectionDecode[mailOutboundDetail](t, request(t, a.Handler(), "GET", detailPath, nil, cookie, csrf), 200)
			if before.Items[0].Link != after.Items[0].Link {
				t.Fatal("delivery revalidation mutated the URL snapshot")
			}
			if change == "inspection" {
				inspect(true)
				inspectionDecode[map[string]any](t, request(t, a.Handler(), "POST", detailPath+"/action", map[string]string{"action": "deliver"}, cookie, csrf), 200)
			}
			if change == "ambiguous" {
				inspectionDecode[map[string]any](t, request(t, a.Handler(), "POST", detailPath+"/action", map[string]string{"action": "cancel", "reason": "重新生成完整邮箱地址"}, cookie, csrf), 200)
			}
		})
	}
}

type sourceTestTransport struct{ calls atomic.Int32 }

func (s *sourceTestTransport) RoundTrip(*http.Request) (*http.Response, error) {
	s.calls.Add(1)
	return nil, fmt.Errorf("unexpected HTTP")
}

func TestMailSourceInspectionInternalAndPolicyInvalidation(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	_, link, id := mappedSourceFixture(t, a, cookie, csrf)
	a.mail.backend = &fakeMail{}
	transport := &sourceTestTransport{}
	a.inspectionHTTP = &http.Client{Transport: transport}
	s, err := a.sourceLink(context.Background(), a.store.db, id, "http://localhost:8081", true)
	if err != nil {
		t.Fatal(err)
	}
	item := inspectionResult{MailboxID: &id, Link: s.Link, LinkMode: "source", PolicyKey: s.PolicyKey}
	result, _, message := a.checkMailInspectionLink(context.Background(), a.inspectionHTTP, item, 2, "123456")
	if result != "passed" || transport.calls.Load() != 0 {
		t.Fatalf("internal lookup failed: %s %s HTTP=%d", result, message, transport.calls.Load())
	}
	created := inspectionDecode[struct{ ID int64 }](t, request(t, a.Handler(), "POST", "/api/admin/mail/inspections", inspectionInput{Mode: "link", Scope: "selected", IDs: []int64{id}, Options: inspectionTestOptions()}, cookie, csrf), 201)
	if task := waitInspectionTask(t, a, created.ID); task.Passed != 1 {
		t.Fatalf("internal worker failed: %+v", task)
	}
	c := sourceChildForTest(t, a, cookie, csrf, id)
	if c.LinkCheck != "passed" {
		t.Fatal("fresh result not visible")
	}
	for _, mode := range []string{"custom", "source"} {
		payload := map[string]string{"link_mode": mode}
		if mode == "custom" {
			payload["link"] = c.Link
		}
		inspectionDecode[map[string]any](t, request(t, a.Handler(), "PUT", fmt.Sprintf("/api/admin/mail/children/%d", id), payload, cookie, csrf), 200)
	}
	if c = sourceChildForTest(t, a, cookie, csrf, id); c.LinkCheck != "unmeasured" {
		t.Fatal("switching back to source resurrected an old inspection")
	}
	created = inspectionDecode[struct{ ID int64 }](t, request(t, a.Handler(), "POST", "/api/admin/mail/inspections", inspectionInput{Mode: "link", Scope: "selected", IDs: []int64{id}, Options: inspectionTestOptions()}, cookie, csrf), 201)
	if task := waitInspectionTask(t, a, created.ID); task.Passed != 1 {
		t.Fatalf("new source policy could not be inspected: %+v", task)
	}
	link.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if _, err := a.store.Save(context.Background(), link, nil); err != nil {
		t.Fatal(err)
	}
	c = sourceChildForTest(t, a, cookie, csrf, id)
	if c.LinkCheck != "unmeasured" {
		t.Fatal("old result survived source edit")
	}
	w := request(t, a.Handler(), "POST", "/api/admin/mail/outbounds", map[string]any{"ids": []int64{id}, "quantity": 1, "mode": "sell", "request_key": "old-policy", "inspection": "link", "valid_hours": 24}, cookie, csrf)
	if w.Code == 201 {
		t.Fatal("old inspection authorized changed source")
	}
	// Source mode is persisted, rather than inferred from a local-looking URL.
	item.LinkMode = "custom"
	result, _, _ = a.checkMailInspectionLink(context.Background(), newInspectionHTTPClient(1), item, 1, "")
	if result == "passed" {
		t.Fatal("custom localhost URL bypassed SSRF protection")
	}
}

func TestMailSourceInspectionSupportsPublicBasePathWithoutCrossSourceAccess(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	_, link, id := mappedSourceFixture(t, a, cookie, csrf)
	a.mail.backend = &fakeMail{}
	settings := a.store.Settings(context.Background())
	settings.PublicBaseURL = "https://mail.example.test/mail"
	if _, err := a.store.Save(context.Background(), settings, nil); err != nil {
		t.Fatal(err)
	}
	source, err := a.sourceLink(context.Background(), a.store.db, id, settings.PublicBaseURL, true)
	if err != nil || !source.LinkAvailable || source.Effective.ID != link.ID || !strings.Contains(source.Link, "/mail/show/") {
		t.Fatalf("prefixed source was not resolved: %+v err=%v", source, err)
	}
	if _, err := a.verifyMailSourceURL(context.Background(), a.store.db, source, strings.Replace(source.Link, "/mail/show/", "/mail-other/show/", 1)); err == nil {
		t.Fatal("an unrelated path prefix was accepted")
	}
	other := accountFixture(t, a, "prefix-other")
	otherLink := linkFixture(t, a, other.ID, "different@example.net")
	otherLink.AccountCode = other.PublicCode
	otherURL, _ := publicLinkURLs(settings.PublicBaseURL, otherLink, "全部平台")
	if _, err := a.verifyMailSourceURL(context.Background(), a.store.db, source, otherURL); err == nil {
		t.Fatal("the path prefix allowed another account or recipient")
	}
	created := inspectionDecode[struct{ ID int64 }](t, request(t, a.Handler(), "POST", "/api/admin/mail/inspections", inspectionInput{
		Mode: "link", Scope: "selected", IDs: []int64{id}, Options: inspectionTestOptions(),
	}, cookie, csrf), 201)
	if task := waitInspectionTask(t, a, created.ID); task.Passed != 1 || task.Total != 1 {
		t.Fatalf("inspection failed with public base path: %+v", task)
	}
}

func TestMailSourceInspectionRespectsPlatformAndConcurrentStock(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)
	_, link, id := mappedSourceFixture(t, a, cookie, csrf)
	p := Platform{ID: "filter", Name: "Filter", Domains: []string{"unrelated.test"}}
	if _, err := a.store.Save(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
	link.PlatformID = p.ID
	if _, err := a.store.Save(context.Background(), link, nil); err != nil {
		t.Fatal(err)
	}
	a.mail.backend = &fakeMail{}
	s, err := a.sourceLink(context.Background(), a.store.db, id, "http://localhost:8081", true)
	if err != nil {
		t.Fatal(err)
	}
	result, _, message := a.checkMailInspectionLink(context.Background(), nil, inspectionResult{MailboxID: &id, LinkMode: "source", PolicyKey: s.PolicyKey}, 2, "123456")
	if result != "pending" || !strings.Contains(message, "平台") {
		t.Fatalf("platform was bypassed: %s %s", result, message)
	}
	var wg sync.WaitGroup
	var success atomic.Int32
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := request(t, a.Handler(), "POST", "/api/admin/mail/outbounds", map[string]any{"ids": []int64{id}, "quantity": 1, "mode": "reserve", "request_key": fmt.Sprint("source-concurrent-", i)}, cookie, csrf)
			if w.Code == 201 {
				success.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatalf("concurrent source stock sold %d times", success.Load())
	}
}

func TestMailSourceSyncAuthenticationAndCSRF(t *testing.T) {
	a := testApp(t, "primary")
	cookie, _ := loginFixture(t, a)
	for _, path := range []string{"/api/admin/mail/children/link-sync/preview", "/api/admin/mail/children/link-sync"} {
		if w := request(t, a.Handler(), "POST", path, map[string]any{}, nil, ""); w.Code != 401 {
			t.Fatalf("anonymous HTTP %d", w.Code)
		}
		if w := request(t, a.Handler(), "POST", path, map[string]any{}, cookie, ""); w.Code != 403 {
			t.Fatalf("missing CSRF HTTP %d", w.Code)
		}
	}
}

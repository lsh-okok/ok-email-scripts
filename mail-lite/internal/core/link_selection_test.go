package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

type linkSelectionResponse struct {
	IDs   []string `json:"ids"`
	Total int      `json:"total"`
}

func seedSelectionLinks(t *testing.T, a *App, accountID, platformID, prefix string, count int) []Link {
	t.Helper()
	links := make([]Link, 0, count)
	records := make([]Record, 0, count)
	for i := 0; i < count; i++ {
		token := randomToken()
		link := Link{ID: randomID(), AccountID: accountID, PlatformID: platformID, Recipient: fmt.Sprintf("%s%04d@example.net", prefix, i), Token: token, TokenHash: digest(token)}
		record, err := recordOf(link)
		if err != nil {
			t.Fatal(err)
		}
		links, records = append(links, link), append(records, record)
	}
	if err := a.store.Put(context.Background(), records, nil); err != nil {
		t.Fatal(err)
	}
	return links
}

func readLinkSelection(t *testing.T, a *App, cookie *http.Cookie, query url.Values) linkSelectionResponse {
	t.Helper()
	w := request(t, a.Handler(), "GET", "/api/admin/links/selection?"+query.Encode(), nil, cookie, "")
	var out linkSelectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); w.Code != http.StatusOK || err != nil {
		t.Fatalf("read selected link IDs: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	if out.Total != len(out.IDs) {
		t.Fatalf("selection total %d differs from ID snapshot length %d", out.Total, len(out.IDs))
	}
	seen := make(map[string]bool, len(out.IDs))
	for _, id := range out.IDs {
		if seen[id] {
			t.Fatalf("selection contains duplicate ID %q", id)
		}
		seen[id] = true
	}
	return out
}

func requireSelectionIDs(t *testing.T, got linkSelectionResponse, want []Link) {
	t.Helper()
	wantIDs := make(map[string]bool, len(want))
	for _, link := range want {
		wantIDs[link.ID] = true
	}
	if got.Total != len(wantIDs) {
		t.Fatalf("selection has %d IDs, want %d", got.Total, len(wantIDs))
	}
	for _, id := range got.IDs {
		if !wantIDs[id] {
			t.Fatalf("selection included unexpected link %q", id)
		}
	}
}

func storedSelectionLink(t *testing.T, a *App, id string) Link {
	t.Helper()
	record, err := a.store.Get(context.Background(), "link", id)
	if err != nil {
		t.Fatal(err)
	}
	var link Link
	if err = json.Unmarshal(record.Data, &link); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestLinkPageSizesUpTo750RemainScopedToLinks(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, randomID())
	seedSelectionLinks(t, a, account.ID, "", "page-", 751)
	cookie, _ := loginFixture(t, a)
	for _, size := range []int{20, 100, 250, 500, 750, 751} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			path := fmt.Sprintf("/api/admin/links?account_id=%s&page_size=%d", account.ID, size)
			w := request(t, a.Handler(), "GET", path, nil, cookie, "")
			var out struct {
				Items    []Link `json:"items"`
				Total    int    `json:"total"`
				PageSize int    `json:"page_size"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); w.Code != 200 || err != nil {
				t.Fatalf("list links: %d %s (%v)", w.Code, w.Body.String(), err)
			}
			want := min(size, 750)
			if out.PageSize != want || len(out.Items) != want || out.Total != 751 {
				t.Fatalf("page_size=%d returned size=%d items=%d total=%d", size, out.PageSize, len(out.Items), out.Total)
			}
		})
	}
	w := request(t, a.Handler(), "GET", "/api/admin/links?account_id="+account.ID+"&page_size=750&page=2", nil, cookie, "")
	var second struct{ Items []Link }
	if err := json.Unmarshal(w.Body.Bytes(), &second); w.Code != 200 || err != nil || len(second.Items) != 1 {
		t.Fatalf("750-link second page: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	w = request(t, a.Handler(), "GET", "/api/admin/accounts?page_size=750", nil, cookie, "")
	var accounts Page
	if err := json.Unmarshal(w.Body.Bytes(), &accounts); w.Code != 200 || err != nil || accounts.PageSize != 100 {
		t.Fatalf("link page-size change expanded account limit: %d %s (%v)", w.Code, w.Body.String(), err)
	}
}

func TestLinkSelectionRequiresExplicitAccountAndKeepsFilters(t *testing.T) {
	a := testApp(t, "primary")
	main := accountFixture(t, a, randomID())
	other := accountFixture(t, a, randomID())
	platform := Platform{ID: randomID(), Name: "Selected platform"}
	if _, err := a.store.Save(context.Background(), platform, nil); err != nil {
		t.Fatal(err)
	}
	matching := seedSelectionLinks(t, a, main.ID, platform.ID, "matching-", 2)
	unrestricted := seedSelectionLinks(t, a, main.ID, "", "matching-other-", 1)
	nonmatching := seedSelectionLinks(t, a, main.ID, platform.ID, "unrelated-", 1)
	seedSelectionLinks(t, a, other.ID, platform.ID, "matching-", 1)
	cookie, _ := loginFixture(t, a)

	query := url.Values{"account_id": {main.ID}}
	allMain := append(append(append([]Link{}, matching...), unrestricted...), nonmatching...)
	requireSelectionIDs(t, readLinkSelection(t, a, cookie, query), allMain)
	query.Set("q", "matching-")
	requireSelectionIDs(t, readLinkSelection(t, a, cookie, query), append(append([]Link{}, matching...), unrestricted...))
	query.Set("platform_id", platform.ID)
	requireSelectionIDs(t, readLinkSelection(t, a, cookie, query), matching)
	query.Set("q", "no-such-recipient")
	empty := readLinkSelection(t, a, cookie, query)
	if empty.IDs == nil || empty.Total != 0 {
		t.Fatalf("empty selection should serialize as an empty ID array: %+v", empty)
	}

	for _, test := range []struct {
		name, account string
		status        int
	}{
		{"missing", "", 400},
		{"blank", "   ", 400},
		{"unknown", randomID(), 404},
		{"other record kind", platform.ID, 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := url.Values{"account_id": {test.account}}
			w := request(t, a.Handler(), "GET", "/api/admin/links/selection?"+query.Encode(), nil, cookie, "")
			if w.Code != test.status {
				t.Fatalf("invalid account returned %d, want %d: %s", w.Code, test.status, w.Body.String())
			}
		})
	}
	path := "/api/admin/links/selection?account_id=" + main.ID
	if w := request(t, a.Handler(), "GET", path, nil, nil, ""); w.Code != 401 {
		t.Fatalf("anonymous selection returned %d: %s", w.Code, w.Body.String())
	}
	replica := testApp(t, "replica")
	if w := request(t, replica.Handler(), "GET", path, nil, cookie, ""); w.Code != 403 {
		t.Fatalf("replica exposed admin selection: %d %s", w.Code, w.Body.String())
	}
}

func Test750LinkSelectionBatchUsesCapturedIDs(t *testing.T) {
	a := testApp(t, "primary")
	main := accountFixture(t, a, randomID())
	other := accountFixture(t, a, randomID())
	original := Platform{ID: randomID(), Name: "Original platform"}
	target := Platform{ID: randomID(), Name: "Target platform"}
	for _, platform := range []Platform{original, target} {
		if _, err := a.store.Save(context.Background(), platform, nil); err != nil {
			t.Fatal(err)
		}
	}
	selected := seedSelectionLinks(t, a, main.ID, original.ID, "selected-", 750)
	unselected := seedSelectionLinks(t, a, other.ID, original.ID, "selected-", 1)
	unselected = append(unselected, seedSelectionLinks(t, a, main.ID, original.ID, "unrelated-", 1)...)
	unselected = append(unselected, seedSelectionLinks(t, a, main.ID, "", "selected-unrestricted-", 1)...)
	cookie, csrf := loginFixture(t, a)
	query := url.Values{"account_id": {main.ID}, "q": {"selected-"}, "platform_id": {original.ID}}
	snapshot := readLinkSelection(t, a, cookie, query)
	requireSelectionIDs(t, snapshot, selected)
	// A later import matches the same filters, but must not join an open batch.
	unselected = append(unselected, seedSelectionLinks(t, a, main.ID, original.ID, "selected-later-", 1)...)
	ids := append(append([]string{}, snapshot.IDs...), snapshot.IDs[0])
	expiry := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)
	batch := func(action, value string) {
		t.Helper()
		body := map[string]any{"ids": ids, "action": action, "value": value}
		if w := request(t, a.Handler(), "POST", "/api/admin/links/batch", body, cookie, csrf); w.Code != 200 {
			t.Fatalf("750-link %s batch failed: %d %s", action, w.Code, w.Body.String())
		}
	}
	checkUnselected := func() {
		t.Helper()
		for _, original := range unselected {
			got := storedSelectionLink(t, a, original.ID)
			if got.ExpiresAt != original.ExpiresAt || got.PlatformID != original.PlatformID {
				t.Fatalf("batch changed link outside captured selection: %+v", got)
			}
		}
	}
	batch("expiry", expiry)
	for _, link := range selected {
		if got := storedSelectionLink(t, a, link.ID); got.ExpiresAt != expiry {
			t.Fatalf("selected link did not receive expiry: %+v", got)
		}
	}
	checkUnselected()
	batch("platform", target.ID)
	for _, link := range selected {
		if got := storedSelectionLink(t, a, link.ID); got.PlatformID != target.ID || got.ExpiresAt != expiry {
			t.Fatalf("selected link did not retain both batch changes: %+v", got)
		}
	}
	checkUnselected()
	batch("delete", "")
	for _, link := range selected {
		if _, err := a.store.Get(context.Background(), "link", link.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("selected link survived deletion: %s (%v)", link.ID, err)
		}
	}
	checkUnselected()
	_, total, err := a.store.List(context.Background(), "kind='link'", nil, 1, 0)
	if err != nil || total != len(unselected) {
		t.Fatalf("snapshot deletion left %d links, want %d: %v", total, len(unselected), err)
	}
}

func TestLinkSelectionBatchFailuresAreAtomic(t *testing.T) {
	a := testApp(t, "primary")
	account := accountFixture(t, a, randomID())
	links := seedSelectionLinks(t, a, account.ID, "", "atomic-", 2)
	platform := Platform{ID: randomID(), Name: "Batch platform"}
	if _, err := a.store.Save(context.Background(), platform, nil); err != nil {
		t.Fatal(err)
	}
	cookie, csrf := loginFixture(t, a)
	for _, test := range []struct {
		name, action, value string
		ids                 []string
		status              int
	}{
		{"expiry missing member", "expiry", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), []string{links[0].ID, "missing-link", links[1].ID}, 404},
		{"platform missing member", "platform", platform.ID, []string{links[0].ID, "missing-link", links[1].ID}, 404},
		{"invalid expiry", "expiry", "not-a-date", []string{links[0].ID, links[1].ID}, 400},
		{"missing platform", "platform", "missing-platform", []string{links[0].ID, links[1].ID}, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := make(map[string]Record, len(links))
			for _, link := range links {
				record, err := a.store.Get(context.Background(), "link", link.ID)
				if err != nil {
					t.Fatal(err)
				}
				before[link.ID] = record
			}
			body := map[string]any{"ids": test.ids, "action": test.action, "value": test.value}
			w := request(t, a.Handler(), "POST", "/api/admin/links/batch", body, cookie, csrf)
			if w.Code != test.status {
				t.Fatalf("invalid batch returned %d, want %d: %s", w.Code, test.status, w.Body.String())
			}
			for _, link := range links {
				after, err := a.store.Get(context.Background(), "link", link.ID)
				if err != nil || after.Version != before[link.ID].Version || string(after.Data) != string(before[link.ID].Data) {
					t.Fatalf("failed batch partially changed link %s: %v", link.ID, err)
				}
			}
		})
	}
}

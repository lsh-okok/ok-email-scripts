package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestMailFolderCountsFollowInventoryGrouping(t *testing.T) {
	a := testApp(t, "primary")
	cookie, csrf := loginFixture(t, a)

	type folderSummary struct {
		Items                []MailFolder `json:"items"`
		Unclassified         int          `json:"unclassified"`
		UnclassifiedChildren int          `json:"unclassified_children"`
		TotalMasters         int          `json:"total_masters"`
		TotalChildren        int          `json:"total_children"`
	}
	readSummary := func() folderSummary {
		t.Helper()
		w := request(t, a.Handler(), http.MethodGet, "/api/admin/mail/folders", nil, cookie, csrf)
		var result folderSummary
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("folder summary: %d %s", w.Code, w.Body.String())
		}
		return result
	}
	if empty := readSummary(); empty.Items == nil || len(empty.Items) != 0 || empty.TotalMasters != 0 || empty.TotalChildren != 0 || empty.Unclassified != 0 || empty.UnclassifiedChildren != 0 {
		t.Fatalf("empty inventory summary: %+v", empty)
	}
	createFolder := func(name string) int64 {
		t.Helper()
		w := request(t, a.Handler(), http.MethodPost, "/api/admin/mail/folders", map[string]string{"name": name}, cookie, csrf)
		var item MailFolder
		if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &item) != nil {
			t.Fatalf("create folder: %d %s", w.Code, w.Body.String())
		}
		return item.ID
	}
	alpha, beta, empty := createFolder("Alpha"), createFolder("Beta"), createFolder("Empty")
	move := func(id, folder int64) {
		t.Helper()
		w := mailJSONRequest(t, a, http.MethodPost, "/api/admin/mail/masters/batch", map[string]any{"ids": []int64{id}, "action": "move", "folder_id": folder}, cookie, csrf)
		if w.Code != http.StatusOK {
			t.Fatalf("move master: %d %s", w.Code, w.Body.String())
		}
	}
	alphaMaster := createInventoryMaster(t, a, cookie, csrf, "alpha@example.com")
	alphaEmptyMaster := createInventoryMaster(t, a, cookie, csrf, "alpha-empty@example.com")
	betaMaster := createInventoryMaster(t, a, cookie, csrf, "beta@example.com")
	unclassifiedMaster := createInventoryMaster(t, a, cookie, csrf, "unclassified@example.com")
	move(alphaMaster, alpha)
	move(alphaEmptyMaster, alpha)
	move(betaMaster, beta)
	createChild := func(email string, parent int64, status string) int64 {
		t.Helper()
		result, err := a.store.db.Exec("INSERT INTO mailboxes(email,kind,parent_id,status,created_at,updated_at) VALUES(?,'child',?,?,?,?)", email, nullableID(parent), status, stampNow(), stampNow())
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	alphaChildren := []int64{
		createChild("alpha-one@example.com", alphaMaster, "available"),
		createChild("alpha-two@example.com", alphaMaster, "reserved"),
		createChild("alpha-three@example.com", alphaMaster, "sold"),
	}
	betaChild := createChild("beta-child@example.com", betaMaster, "available")
	unclassifiedChild := createChild("unclassified-child@example.com", unclassifiedMaster, "available")
	orphan := createChild("orphan@example.com", 0, "available")

	assertSummary := func(wantFolders map[int64][2]int, wantUnclassified, wantUnclassifiedChildren int) {
		t.Helper()
		got := readSummary()
		if got.TotalMasters != 4 || got.TotalChildren != 6 || got.Unclassified != wantUnclassified || got.UnclassifiedChildren != wantUnclassifiedChildren || len(got.Items) != len(wantFolders) {
			t.Fatalf("inventory group totals: %+v", got)
		}
		for _, item := range got.Items {
			want, exists := wantFolders[item.ID]
			if !exists || item.Masters != want[0] || item.Children != want[1] {
				t.Fatalf("group %q counts: got %d/%d, want %v (exists=%v)", item.Name, item.Masters, item.Children, want, exists)
			}
		}
	}
	assertInventory := func(folder string, wantMasters, wantChildren []int64) {
		t.Helper()
		for kind, expected := range map[string][]int64{"masters": wantMasters, "children": wantChildren} {
			w := request(t, a.Handler(), http.MethodGet, "/api/admin/mail/"+kind+"?folder="+folder, nil, cookie, csrf)
			var page struct {
				Items []struct{ ID int64 } `json:"items"`
				Total int                  `json:"total"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != len(expected) || len(page.Items) != len(expected) {
				t.Fatalf("%s folder %q: %d %s", kind, folder, w.Code, w.Body.String())
			}
			remaining := map[int64]bool{}
			for _, id := range expected {
				remaining[id] = true
			}
			for _, item := range page.Items {
				if !remaining[item.ID] {
					t.Fatalf("%s folder %q contained unexpected mailbox %d", kind, folder, item.ID)
				}
				delete(remaining, item.ID)
			}
		}
	}

	assertSummary(map[int64][2]int{alpha: {2, 3}, beta: {1, 1}, empty: {0, 0}}, 1, 2)
	assertInventory(fmt.Sprint(alpha), []int64{alphaMaster, alphaEmptyMaster}, alphaChildren)
	assertInventory(fmt.Sprint(beta), []int64{betaMaster}, []int64{betaChild})
	assertInventory(fmt.Sprint(empty), nil, nil)
	assertInventory("0", []int64{unclassifiedMaster}, []int64{unclassifiedChild, orphan})
	allChildren := append(append([]int64{}, alphaChildren...), betaChild, unclassifiedChild, orphan)
	assertInventory("", []int64{alphaMaster, alphaEmptyMaster, betaMaster, unclassifiedMaster}, allChildren)

	// Moving a master carries all its children across group counts and filters,
	// including reserved/sold children and children without a working link.
	move(alphaMaster, beta)
	assertSummary(map[int64][2]int{alpha: {1, 0}, beta: {2, 4}, empty: {0, 0}}, 1, 2)
	assertInventory(fmt.Sprint(alpha), []int64{alphaEmptyMaster}, nil)
	assertInventory(fmt.Sprint(beta), []int64{alphaMaster, betaMaster}, append(append([]int64{}, alphaChildren...), betaChild))

	deleted := request(t, a.Handler(), http.MethodDelete, fmt.Sprintf("/api/admin/mail/folders/%d", beta), nil, cookie, csrf)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete folder: %d %s", deleted.Code, deleted.Body.String())
	}
	assertSummary(map[int64][2]int{alpha: {1, 0}, empty: {0, 0}}, 3, 6)
	assertInventory("0", []int64{alphaMaster, betaMaster, unclassifiedMaster}, allChildren)
}

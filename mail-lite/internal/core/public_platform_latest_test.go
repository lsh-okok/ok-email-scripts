package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

type scopedLatestMail struct {
	items     []Message
	detailed  []string
	limitSeen int
	lists     int
}

func (f *scopedLatestMail) List(_ context.Context, _ Account, _ []string, limit int) ([]Message, error) {
	f.limitSeen = limit
	f.lists++
	return append([]Message{}, f.items[:min(limit, len(f.items))]...), nil
}

func (f *scopedLatestMail) Detail(_ context.Context, _ Account, _ []string, ref MessageRef) (Message, error) {
	f.detailed = append(f.detailed, ref.ID)
	for _, item := range f.items {
		if item.Source.ID == ref.ID {
			item.Text = "body-for-" + ref.ID
			return item, nil
		}
	}
	return Message{}, ErrNotFound
}

func (*scopedLatestMail) Delete(context.Context, Account, []string, []MessageRef) ([]DeleteResult, error) {
	return nil, nil
}

func TestPublicPlatformLatestFiltersBeforeLimit(t *testing.T) {
	for _, targetIndex := range []int{1, 25} {
		t.Run(fmt.Sprint(targetIndex), func(t *testing.T) {
			app := testApp(t, "primary")
			account := accountFixture(t, app, "scoped-latest")
			platform := Platform{ID: "scoped-platform", Name: "Target", Domains: []string{"example.net"}, Keywords: []string{"verification"}}
			if _, err := app.store.Save(context.Background(), platform, nil); err != nil {
				t.Fatal(err)
			}
			link := linkFixture(t, app, account.ID, "child@example.net")
			link.PlatformID = platform.ID
			if _, err := app.store.Save(context.Background(), link, nil); err != nil {
				t.Fatal(err)
			}
			backend := &scopedLatestMail{}
			for i := range targetIndex + 2 {
				from, subject := "noise@other.invalid", "other platform"
				if i == targetIndex || i == targetIndex+1 {
					from, subject = "verify@example.net", "verification code"
				}
				backend.items = append(backend.items, Message{Subject: subject, From: from, To: link.Recipient, ReceivedAt: time.Unix(1_800_000_000-int64(i), 0).UTC(), Source: MessageRef{AccountID: account.ID, Protocol: "graph", Folder: "inbox", ID: fmt.Sprint(i)}})
			}
			app.mail.backend = backend
			w := request(t, app.Handler(), http.MethodGet, "/m/"+link.Token, nil, nil, "")
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "body-for-"+fmt.Sprint(targetIndex)) {
				t.Fatalf("default page missed newest matching platform message: status=%d", w.Code)
			}
			if len(backend.detailed) != 1 || backend.detailed[0] != fmt.Sprint(targetIndex) {
				t.Fatalf("scoped latest read unnecessary/wrong bodies: %v", backend.detailed)
			}
			w = request(t, app.Handler(), http.MethodGet, "/m/"+link.Token, nil, nil, "")
			if w.Code != http.StatusOK || backend.lists != 1 || len(backend.detailed) != 1 {
				t.Fatal("repeated scoped reads did not reuse the normal short-lived cache")
			}
			platform.Domains = []string{"blocked.invalid"}
			if _, err := app.store.Save(context.Background(), platform, nil); err != nil {
				t.Fatal(err)
			}
			w = request(t, app.Handler(), http.MethodGet, "/m/"+link.Token, nil, nil, "")
			if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "body-for-") || backend.lists != 2 {
				t.Fatal("platform edit reused cached summaries from the old policy")
			}
		})
	}
}

type editDuringPublicList struct {
	paginationMail
	edit func()
}

func (f *editDuringPublicList) List(ctx context.Context, account Account, proxies []string, limit int) ([]Message, error) {
	items, err := f.paginationMail.List(ctx, account, proxies, limit)
	f.edit()
	return items, err
}

func TestPublicPaginationRechecksPolicyAfterUpstreamRead(t *testing.T) {
	for _, change := range []string{"disabled", "generation", "platform"} {
		t.Run(change, func(t *testing.T) {
			app := testApp(t, "primary")
			account := accountFixture(t, app, "changed-during-read")
			platform := Platform{ID: "changed-platform", Name: "Allowed", Domains: []string{"example.net"}}
			if _, err := app.store.Save(context.Background(), platform, nil); err != nil {
				t.Fatal(err)
			}
			link := linkFixture(t, app, account.ID, "child@example.net")
			link.PlatformID = platform.ID
			if _, err := app.store.Save(context.Background(), link, nil); err != nil {
				t.Fatal(err)
			}
			app.mail.backend = &editDuringPublicList{edit: func() {
				var value any
				switch change {
				case "disabled":
					account.Status = "disabled"
					value = account
				case "generation":
					account.Generation++
					value = account
				case "platform":
					platform.Domains = []string{"blocked.invalid"}
					value = platform
				}
				if _, err := app.store.Save(context.Background(), value, nil); err != nil {
					t.Fatal(err)
				}
			}}
			w := request(t, app.Handler(), http.MethodGet, "/api/mailboxes/"+link.Token+"/messages?details=1", nil, nil, "")
			if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "subject-00") || len(app.publicPages.sessions) != 0 {
				t.Fatalf("changed policy published an old snapshot: status=%d", w.Code)
			}
		})
	}
}

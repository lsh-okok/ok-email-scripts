package core

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestIMAPReadHostPreferenceLearnsFallbackAndRecoversPrimary(t *testing.T) {
	account := Account{ID: "host-preference", Provider: "outlook", Generation: 1}
	backend := &liveBackend{}
	primary, fallback := outlookIMAPHosts[0], outlookIMAPHosts[1]
	var tried []string
	value, err := readIMAPHosts(context.Background(), backend, account, func(host string) (string, error) {
		tried = append(tried, host)
		if host == primary {
			return "", errors.New("primary temporarily unavailable")
		}
		return "complete mailbox", nil
	})
	if err != nil || value != "complete mailbox" || !reflect.DeepEqual(tried, []string{primary, fallback}) {
		t.Fatalf("initial read lost canonical fallback: value=%q err=%v tried=%v", value, err, tried)
	}
	tried = nil
	_, err = readIMAPHosts(context.Background(), backend, account, func(host string) (string, error) {
		tried = append(tried, host)
		return "complete mailbox", nil
	})
	if err != nil || !reflect.DeepEqual(tried, []string{fallback}) {
		t.Fatalf("next read retried the failed primary: err=%v tried=%v", err, tried)
	}
	tried = nil
	_, err = readIMAPHosts(context.Background(), backend, account, func(host string) (string, error) {
		tried = append(tried, host)
		if host == fallback {
			return "", errors.New("preferred host now unavailable")
		}
		return "recovered mailbox", nil
	})
	if err != nil || !reflect.DeepEqual(tried, []string{fallback, primary}) {
		t.Fatalf("preferred failure did not recover on primary: err=%v tried=%v", err, tried)
	}
	tried = nil
	_, err = readIMAPHosts(context.Background(), backend, account, func(host string) (string, error) {
		tried = append(tried, host)
		return "complete mailbox", nil
	})
	if err != nil || !reflect.DeepEqual(tried, []string{primary}) {
		t.Fatalf("recovered primary was not retained: err=%v tried=%v", err, tried)
	}
}

func TestIMAPReadHostPreferenceExpiryAndIsolation(t *testing.T) {
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	account := Account{ID: "first", Provider: "outlook", Generation: 3}
	other := account
	other.ID = "second"
	updated := account
	updated.Generation++
	primary, fallback := outlookIMAPHosts[0], outlookIMAPHosts[1]
	canonical, preferred := []string{primary, fallback}, []string{fallback, primary}
	var preferences imapReadHostPreferences
	preferences.remember(account, fallback, base)
	if got := preferences.hosts(account, base.Add(imapReadHostTTL-time.Nanosecond)); !reflect.DeepEqual(got, preferred) {
		t.Fatalf("unexpired preferred host lost: %v", got)
	}
	for _, isolated := range []Account{other, updated} {
		if got := preferences.hosts(isolated, base); !reflect.DeepEqual(got, canonical) {
			t.Fatalf("account or generation inherited a preference: account=%+v hosts=%v", isolated, got)
		}
	}
	preferences.remember(updated, primary, base)
	if got := preferences.hosts(account, base); !reflect.DeepEqual(got, preferred) {
		t.Fatalf("another generation replaced the preference: %v", got)
	}
	preferences.forget(account, primary)
	if got := preferences.hosts(account, base); !reflect.DeepEqual(got, preferred) {
		t.Fatalf("failure of another host cleared a healthy preference: %v", got)
	}
	preferences.forget(account, fallback)
	if got := preferences.hosts(account, base); !reflect.DeepEqual(got, canonical) {
		t.Fatalf("failed preferred host was retained: %v", got)
	}
	preferences.remember(account, fallback, base)
	if got := preferences.hosts(account, base.Add(imapReadHostTTL)); !reflect.DeepEqual(got, canonical) {
		t.Fatalf("expired preference was used: %v", got)
	}
	key := imapReadHostKey{accountID: account.ID, generation: account.Generation}
	if _, retained := preferences.entries[key]; retained {
		t.Fatal("expired preference was not removed")
	}
}

func TestIMAPReadHostPreferenceAllowlistAndBoundedMemory(t *testing.T) {
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	var preferences imapReadHostPreferences
	account := Account{ID: "allowed", Provider: "outlook", Generation: 1}
	preferences.remember(account, "untrusted.example.net", base)
	preferences.remember(Account{Provider: "outlook"}, outlookIMAPHosts[1], base)
	preferences.remember(Account{ID: "icloud", Provider: "icloud"}, "imap.mail.me.com", base)
	if len(preferences.entries) != 0 {
		t.Fatalf("untrusted host, blank ID or single-host provider was cached: %v", preferences.entries)
	}
	if got := preferences.hosts(Account{ID: "icloud", Provider: "icloud"}, base); !reflect.DeepEqual(got, []string{"imap.mail.me.com"}) {
		t.Fatalf("single-host provider changed routing: %v", got)
	}
	for i := 0; i < imapReadHostMaxEntries+100; i++ {
		account.ID = fmt.Sprintf("bounded-%d", i)
		preferences.remember(account, outlookIMAPHosts[1], base)
		if len(preferences.entries) > imapReadHostMaxEntries {
			t.Fatalf("host preferences exceeded the bound: %d", len(preferences.entries))
		}
	}
	account.ID = "after-expiry"
	preferences.remember(account, outlookIMAPHosts[1], base.Add(imapReadHostTTL+time.Second))
	if len(preferences.entries) != 1 {
		t.Fatalf("expired preferences were not pruned on new success: %d", len(preferences.entries))
	}
	returned := preferences.hosts(account, base.Add(imapReadHostTTL+time.Second))
	returned[0] = "untrusted.example.net"
	if got := preferences.hosts(account, base.Add(imapReadHostTTL+time.Second)); got[0] != outlookIMAPHosts[1] {
		t.Fatalf("caller modified stored or canonical host order: %v", got)
	}
}

func TestIMAPReadHostPreferenceCancellationDoesNotPoisonHost(t *testing.T) {
	account := Account{ID: "canceled-reader", Provider: "outlook", Generation: 1}
	backend := &liveBackend{}
	preferred := outlookIMAPHosts[1]
	backend.imapHostPreferences.remember(account, preferred, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var tried []string
	_, err := readIMAPHosts(ctx, backend, account, func(host string) (string, error) {
		tried = append(tried, host)
		cancel()
		return "", ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(tried, []string{preferred}) {
		t.Fatalf("cancellation did not stop the read: tried=%v err=%v", tried, err)
	}
	if got := backend.imapHostPreferences.hosts(account, time.Now()); got[0] != preferred {
		t.Fatalf("visitor cancellation discarded a working host: %v", got)
	}
}

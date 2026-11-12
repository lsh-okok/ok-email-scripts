package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func seedScale(t *testing.T, a *App) ([]Link, time.Duration) {
	t.Helper()
	start := time.Now()
	ctx := context.Background()
	records := []Record{}
	for i := 0; i < 1000; i++ {
		v := Account{ID: fmt.Sprintf("a%04d", i), Email: fmt.Sprintf("a%04d@outlook.com", i), Provider: "outlook", Status: "active", ClientID: "fake", RefreshToken: "fake-refresh"}
		r, _ := recordOf(v)
		records = append(records, r)
	}
	if e := a.store.Put(ctx, records, nil); e != nil {
		t.Fatal(e)
	}
	links := []Link{}
	for i := 0; i < 1000; i++ {
		text := strings.Builder{}
		for j := 0; j < 100; j++ {
			fmt.Fprintf(&text, "recipient%03d@example.net\n", j)
		}
		job := Job{ID: "scale", Status: "running"}
		a.runLinksImport(ctx, &job, ImportRequest{AccountID: fmt.Sprintf("a%04d", i), Files: []ImportFile{{Name: "scale", Text: text.String()}}})
		if job.Failed > 0 {
			t.Fatal(job.Errors)
		}
		r, e := a.store.Find(ctx, "kind='link' AND account_id=? AND recipient='recipient000@example.net'", fmt.Sprintf("a%04d", i))
		if e != nil {
			t.Fatal(e)
		}
		var l Link
		decode(r.Data, &l)
		links = append(links, l)
	}
	return links, time.Since(start)
}

func TestScaleSyncAndLoad(t *testing.T) {
	if os.Getenv("MAIL_LITE_ACCEPTANCE") != "1" {
		t.Skip("set MAIL_LITE_ACCEPTANCE=1 for 100k/four-node/10-minute acceptance")
	}
	a := testApp(t, "primary")
	installFake(a)
	links, importTime := seedScale(t, a)
	t.Logf("import 1000 accounts / 100000 links: %s", importTime)
	qStart := time.Now()
	rs, total, e := a.store.List(context.Background(), "kind='link' AND recipient LIKE ?", []any{"%recipient000%"}, 100, 0)
	if e != nil || total != 1000 || len(rs) != 100 {
		t.Fatal(total, e)
	}
	t.Logf("server search/pagination: %s", time.Since(qStart))
	cookie, csrf := loginFixture(t, a)
	exportStart := time.Now()
	export := request(t, a.Handler(), "GET", "/api/admin/links/export", nil, cookie, csrf)
	if export.Code != 200 || strings.Count(export.Body.String(), "\n") != 100000 {
		t.Fatal("incomplete export", export.Code)
	}
	t.Logf("export 100000 links: %s / %d bytes", time.Since(exportStart), export.Body.Len())
	export = nil
	primary := httptest.NewServer(a.Handler())
	defer primary.Close()
	replicas := []*App{}
	servers := []*httptest.Server{}
	for i := 0; i < 4; i++ {
		n := Node{ID: fmt.Sprint(i), Token: randomToken(), Name: fmt.Sprint(i)}
		a.store.AuxPut("node", n.ID, n)
		r := testApp(t, "replica")
		r.cfg.MasterURL = primary.URL
		r.cfg.NodeID = n.ID
		r.cfg.NodeToken = n.Token
		installFake(r)
		replicas = append(replicas, r)
	}
	syncStart := time.Now()
	var wg sync.WaitGroup
	for _, r := range replicas {
		wg.Add(1)
		go func(r *App) {
			defer wg.Done()
			if e := r.syncOnce(context.Background()); e != nil {
				t.Error(e)
			}
		}(r)
	}
	wg.Wait()
	if t.Failed() {
		return
	}
	t.Logf("four replicas full sync: %s", time.Since(syncStart))
	for _, r := range replicas {
		_, count, e := r.store.List(context.Background(), "kind='link'", nil, 1, 0)
		if count != 100000 || e != nil {
			t.Fatal(count, e)
		}
		s := httptest.NewServer(r.Handler())
		servers = append(servers, s)
		defer s.Close()
		r.Start(context.Background())
	}
	// Warm a realistic shared mailbox working set, then retain normal 8s TTL.
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{MaxIdleConns: 400, MaxIdleConnsPerHost: 100}}
	defer client.CloseIdleConnections()
	for _, s := range servers {
		for i := 0; i < 32; i++ {
			res, e := client.Get(s.URL + "/api/mailboxes/" + links[i].Token + "/messages")
			if e != nil {
				t.Fatal(e)
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatal(res.StatusCode)
			}
		}
	}
	duration := 10 * time.Minute
	if v := os.Getenv("MAIL_LITE_LOAD_DURATION"); v != "" {
		d, e := time.ParseDuration(v)
		if e != nil {
			t.Fatal(e)
		}
		duration = d
	}
	var count, failed atomic.Int64
	var histogram [60001]atomic.Int64
	start := time.Now()
	deadline := start.Add(duration)
	for worker := 0; worker < 200; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; time.Now().Before(deadline); iteration++ {
				target := servers[worker%4].URL + "/api/mailboxes/" + links[(worker+iteration)%32].Token + "/messages"
				begin := time.Now()
				res, e := client.Get(target)
				bad := e != nil
				if e == nil {
					var result struct{ Items []Message }
					e = json.NewDecoder(res.Body).Decode(&result)
					res.Body.Close()
					bad = res.StatusCode != 200 || e != nil || len(result.Items) != 1
				}
				if bad {
					failed.Add(1)
				}
				ms := min(time.Since(begin).Milliseconds(), 60000)
				histogram[ms].Add(1)
				count.Add(1)
			}
		}(worker)
	}
	wg.Wait()
	elapsed := time.Since(start)
	n := count.Load()
	threshold := int64(float64(n) * .95)
	var sum, p95 int64
	for i := range histogram {
		sum += histogram[i].Load()
		if sum >= threshold {
			p95 = int64(i)
			break
		}
	}
	errorRate := float64(failed.Load()) / float64(n)
	t.Logf("LOAD duration=%s concurrent=200 replicas=4 requests=%d failures=%d error_rate=%.5f p95=%dms rps=%.1f", elapsed, n, failed.Load(), errorRate, p95, float64(n)/elapsed.Seconds())
	if p95 >= 1000 || errorRate >= .01 {
		t.Errorf("acceptance threshold missed")
	}
}

func TestSyncResumeRevocationAndCacheInvalidation(t *testing.T) {
	a := testApp(t, "primary")
	accountFixture(t, a, "a")
	l := linkFixture(t, a, "a", "recipient000@example.net")
	shared := installFake(a)
	n := Node{ID: "node", Token: randomToken()}
	a.store.AuxPut("node", n.ID, n)
	primary := httptest.NewServer(a.Handler())
	defer primary.Close()
	r := testApp(t, "replica")
	r.cfg.MasterURL = primary.URL
	r.cfg.NodeID = n.ID
	r.cfg.NodeToken = n.Token
	r.mail.backend = shared
	if r.isReady() {
		t.Fatal("fresh replica ready")
	}
	if e := r.syncOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !r.isReady() {
		t.Fatal("replica not ready")
	}
	w := request(t, r.Handler(), "GET", "/api/mailboxes/"+l.Token+"/messages", nil, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	v, _ := a.store.Account(context.Background(), "a")
	listed, e := a.mail.List(context.Background(), v)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.mail.Delete(context.Background(), v, []string{listed[0].Ref}); e != nil {
		t.Fatal(e)
	}
	if e := r.syncOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	r.cache.mu.Lock()
	cacheLen := len(r.cache.entries)
	r.cache.mu.Unlock()
	if cacheLen != 0 {
		t.Fatal("replica stale cache")
	}
	w = request(t, r.Handler(), "GET", "/api/mailboxes/"+l.Token+"/messages", nil, nil, "")
	var after struct{ Items []Message }
	json.Unmarshal(w.Body.Bytes(), &after)
	if w.Code != 200 || len(after.Items) != 0 {
		t.Fatal("deleted message served across nodes", w.Code, w.Body.String())
	}
	a.store.Delete(context.Background(), "link", []string{l.ID})
	if e := r.syncOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	w = request(t, r.Handler(), "GET", "/api/mailboxes/"+l.Token+"/messages", nil, nil, "")
	if w.Code != 404 {
		t.Fatal("deleted link served", w.Code)
	}
	r.syncMu.Lock()
	r.lastSync = time.Now().Add(-121 * time.Second)
	r.syncMu.Unlock()
	if r.isReady() {
		t.Fatal("stale replica served")
	}
	n.Revoked = true
	a.store.AuxPut("node", n.ID, n)
	if r.syncOnce(context.Background()) == nil || r.isReady() {
		t.Fatal("revoked replica ready")
	}
}

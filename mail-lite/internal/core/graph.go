package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type graphMessage struct {
	ID       string    `json:"id"`
	Subject  string    `json:"subject"`
	Received time.Time `json:"receivedDateTime"`
	Preview  string    `json:"bodyPreview"`
	From     struct {
		Address struct{ Name, Address string } `json:"emailAddress"`
	} `json:"from"`
	To []struct {
		Address struct{ Name, Address string } `json:"emailAddress"`
	} `json:"toRecipients"`
	Body struct {
		Type    string `json:"contentType"`
		Content string `json:"content"`
	} `json:"body"`
	Headers []struct{ Name, Value string } `json:"internetMessageHeaders"`
}

func (g graphMessage) message(account, folder string) Message {
	to := []string{}
	for _, a := range g.To {
		to = append(to, a.Address.Address)
	}
	h := map[string][]string{}
	for _, v := range g.Headers {
		k := strings.ToLower(v.Name)
		h[k] = append(h[k], v.Value)
	}
	m := Message{Subject: g.Subject, From: g.From.Address.Address, To: strings.Join(to, ", "), ReceivedAt: g.Received, Folder: folder, Preview: g.Preview, Headers: h, Source: MessageRef{AccountID: account, Protocol: "graph", ID: g.ID, Folder: folder}}
	if strings.EqualFold(g.Body.Type, "html") {
		m.HTML = g.Body.Content
	} else {
		m.Text = g.Body.Content
	}
	return m
}
func (b *liveBackend) graphRequest(ctx context.Context, p []string, token, method, target string, body []byte, out any) (int, error) {
	base, e := url.Parse(b.app.cfg.GraphURL)
	if e != nil {
		return 0, e
	}
	u, e := url.Parse(target)
	if e != nil || u.Scheme != base.Scheme || u.Host != base.Host || !strings.HasPrefix(u.Path, base.Path+"/") {
		return 0, errors.New("invalid Graph URL")
	}
	for _, proxyURL := range p {
		tr := transport(proxyURL)
		c := &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: noRedirect}
		req, _ := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, e := c.Do(req)
		if e != nil {
			tr.CloseIdleConnections()
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			continue
		}
		data, e := io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
		res.Body.Close()
		tr.CloseIdleConnections()
		if e != nil || len(data) > 16<<20 {
			return res.StatusCode, errors.New("Graph response exceeded limits or was interrupted")
		}
		if res.StatusCode == 429 {
			return 429, errors.New("Microsoft 正在限流，请稍后重试")
		}
		if res.StatusCode == 401 || res.StatusCode == 403 {
			return res.StatusCode, errors.New("Microsoft 授权失效或权限不足")
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return res.StatusCode, fmt.Errorf("Microsoft 请求失败（%d）", res.StatusCode)
		}
		if out != nil && len(data) > 0 {
			if e = json.Unmarshal(data, out); e != nil {
				return res.StatusCode, errors.New("invalid Graph response")
			}
		}
		return res.StatusCode, nil
	}
	return 0, errors.New("无法连接 Microsoft，请检查网络及代理")
}
func (b *liveBackend) graphList(ctx context.Context, a Account, p []string, n int) ([]Message, error) {
	return b.graphListOrder(ctx, a, p, n, "latest")
}
func (b *liveBackend) graphListOrder(ctx context.Context, a Account, p []string, n int, order string) ([]Message, error) {
	token, e := b.app.tokens.Get(ctx, a.ID, "graph", false)
	if e != nil {
		return nil, e
	}
	out := []Message{}
	direction := "desc"
	if order == "oldest" {
		direction = "asc"
	}
	for _, folder := range []string{"inbox", "junkemail"} {
		params := url.Values{"$top": {strconv.Itoa(min(50, n))}, "$orderby": {"receivedDateTime " + direction}, "$select": {"id,subject,from,toRecipients,receivedDateTime,bodyPreview,internetMessageHeaders"}}
		target := b.app.cfg.GraphURL + "/me/mailFolders/" + folder + "/messages?" + params.Encode()
		count := 0
		visited := map[string]bool{}
		for target != "" && count < n {
			if visited[target] {
				return nil, errors.New("Graph pagination loop")
			}
			visited[target] = true
			var page struct {
				Value []graphMessage `json:"value"`
				Next  string         `json:"@odata.nextLink"`
			}
			if _, e = b.graphRequest(ctx, p, token.Token, "GET", target, nil, &page); e != nil {
				return nil, e
			}
			for _, v := range page.Value {
				if count >= n {
					break
				}
				out = append(out, v.message(a.ID, folder))
				count++
			}
			if len(page.Value) == 0 {
				break
			}
			target = page.Next
		}
	}
	return out, nil
}
func (b *liveBackend) graphListRecipient(ctx context.Context, a Account, p []string, n int, recipient string) ([]Message, error) {
	return b.graphListRecipientOrder(ctx, a, p, n, recipient, "latest")
}
func (b *liveBackend) graphListRecipientOrder(ctx context.Context, a Account, p []string, n int, recipient, order string) ([]Message, error) {
	t, e := b.app.tokens.Get(ctx, a.ID, "graph", false)
	if e != nil {
		return nil, e
	}
	targetRecipient := strings.TrimSpace(recipient)
	if targetRecipient == "" || len(targetRecipient) > 320 {
		return nil, errors.New("收件人地址无效")
	}
	// Graph uses a single quote as the OData string delimiter.
	recipient = strings.ReplaceAll(targetRecipient, "'", "''")
	out := []Message{}
	direction := "desc"
	if order == "oldest" {
		direction = "asc"
	}
	for _, folder := range []string{"inbox", "junkemail"} {
		params := url.Values{
			"$top":     {strconv.Itoa(min(50, n))},
			"$orderby": {"receivedDateTime " + direction},
			"$select":  {"id,subject,from,toRecipients,receivedDateTime,bodyPreview,internetMessageHeaders"},
			"$filter":  {"toRecipients/any(r:r/emailAddress/address eq '" + recipient + "')"},
		}
		target := b.app.cfg.GraphURL + "/me/mailFolders/" + folder + "/messages?" + params.Encode()
		count := 0
		visited := map[string]bool{}
		for target != "" && count < n {
			if visited[target] {
				return nil, errors.New("Graph pagination loop")
			}
			visited[target] = true
			var page struct {
				Value []graphMessage `json:"value"`
				Next  string         `json:"@odata.nextLink"`
			}
			if _, e = b.graphRequest(ctx, p, t.Token, "GET", target, nil, &page); e != nil {
				return nil, e
			}
			for _, v := range page.Value {
				if count >= n {
					break
				}
				m := v.message(a.ID, folder)
				if matchesAccountRecipient(a, m, targetRecipient) {
					out = append(out, m)
					count++
				}
			}
			if len(page.Value) == 0 {
				break
			}
			target = page.Next
		}
	}
	return out, nil
}
func (b *liveBackend) graphDetail(ctx context.Context, a Account, p []string, r MessageRef) (Message, error) {
	t, e := b.app.tokens.Get(ctx, a.ID, "graph", false)
	if e != nil {
		return Message{}, e
	}
	var v graphMessage
	target := b.app.cfg.GraphURL + "/me/mailFolders/" + r.Folder + "/messages/" + url.PathEscape(r.ID) + "?$select=id,subject,from,toRecipients,receivedDateTime,bodyPreview,body,internetMessageHeaders"
	_, e = b.graphRequest(ctx, p, t.Token, "GET", target, nil, &v)
	m := v.message(a.ID, r.Folder)
	if len(m.HTML)+len(m.Text) > 2<<20 {
		return m, errors.New("邮件正文超过 2 MiB 限制")
	}
	return m, e
}
func (b *liveBackend) graphDelete(ctx context.Context, a Account, p []string, refs []MessageRef) ([]DeleteResult, error) {
	t, e := b.app.tokens.Get(ctx, a.ID, "write", false)
	if e != nil {
		return nil, e
	}
	out := []DeleteResult{}
	for start := 0; start < len(refs); start += 20 {
		batch := refs[start:min(start+20, len(refs))]
		requests := []map[string]any{}
		for i, r := range batch {
			requests = append(requests, map[string]any{"id": strconv.Itoa(i), "method": "DELETE", "url": "/me/mailFolders/" + r.Folder + "/messages/" + url.PathEscape(r.ID)})
		}
		body, _ := json.Marshal(map[string]any{"requests": requests})
		var response struct {
			Responses []struct {
				ID     string `json:"id"`
				Status int    `json:"status"`
			} `json:"responses"`
		}
		_, err := b.graphRequest(ctx, p, t.Token, "POST", b.app.cfg.GraphURL+"/$batch", body, &response)
		statuses := map[int]int{}
		for _, r := range response.Responses {
			i, e := strconv.Atoi(r.ID)
			if e == nil && i >= 0 && i < len(batch) {
				statuses[i] = r.Status
			}
		}
		for i, r := range batch {
			v := DeleteResult{Ref: signRef(b.app.key, r)}
			s := statuses[i]
			switch {
			case err != nil:
				v.Error = err.Error()
			case s == 200 || s == 204 || s == 404:
				v.Success = true
			case s == 403:
				v.Error = "删除需要 Mail.ReadWrite 授权，请重新授权邮箱"
			case s == 429:
				v.Error = "Microsoft 正在限流，请稍后重试"
			default:
				v.Error = fmt.Sprintf("删除失败（%d）", s)
			}
			out = append(out, v)
		}
	}
	return out, nil
}

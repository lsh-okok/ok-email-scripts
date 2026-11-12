package core

import (
	"context"
	"errors"
	"net/url"
	"time"
)

type adminGraphFolder struct {
	folder  string
	nextURL string
	buffer  []Message
	visited map[string]bool
}

type adminGraphSource struct {
	backend  *liveBackend
	account  Account
	proxies  []string
	order    string
	snapshot time.Time
	folders  []adminGraphFolder
}

func newAdminGraphSource(b *liveBackend, account Account, proxies []string, order, folder string, snapshot time.Time) *adminGraphSource {
	s := &adminGraphSource{backend: b, account: account, proxies: proxies, order: order, snapshot: snapshot}
	direction := "desc"
	if order == "oldest" {
		direction = "asc"
	}
	for _, name := range adminFolders(folder) {
		params := url.Values{
			"$top":     {"20"},
			"$orderby": {"receivedDateTime " + direction},
			"$select":  {"id,subject,from,toRecipients,receivedDateTime,bodyPreview"},
			// A new arrival must not shift a later @odata.nextLink page.
			"$filter": {"receivedDateTime le " + snapshot.Format(time.RFC3339Nano)},
		}
		s.folders = append(s.folders, adminGraphFolder{folder: name, nextURL: b.app.cfg.GraphURL + "/me/mailFolders/" + name + "/messages?" + params.Encode(), visited: make(map[string]bool)})
	}
	return s
}

func (s *adminGraphSource) clone() adminPageSource {
	out := *s
	out.folders = make([]adminGraphFolder, len(s.folders))
	for i, folder := range s.folders {
		out.folders[i] = folder
		out.folders[i].buffer = append([]Message(nil), folder.buffer...)
		out.folders[i].visited = make(map[string]bool, len(folder.visited))
		for target := range folder.visited {
			out.folders[i].visited[target] = true
		}
	}
	return &out
}

func (s *adminGraphSource) prime(ctx context.Context) error {
	for i := range s.folders {
		if err := s.fillFolder(ctx, &s.folders[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *adminGraphSource) fillFolder(ctx context.Context, folder *adminGraphFolder) error {
	for len(folder.buffer) == 0 && folder.nextURL != "" {
		if folder.visited[folder.nextURL] {
			return errors.New("Graph pagination loop")
		}
		token, err := s.backend.app.tokens.Get(ctx, s.account.ID, "graph", false)
		if err != nil {
			return err
		}
		var page struct {
			Value []graphMessage `json:"value"`
			Next  string         `json:"@odata.nextLink"`
		}
		if _, err := s.backend.graphRequest(ctx, s.proxies, token.Token, "GET", folder.nextURL, nil, &page); err != nil {
			return err
		}
		folder.visited[folder.nextURL] = true
		folder.nextURL = page.Next
		for _, message := range page.Value {
			if message.ID == "" || message.Received.IsZero() {
				return errors.New("Graph 邮件摘要缺少 ID 或接收时间")
			}
			if !message.Received.After(s.snapshot) {
				folder.buffer = append(folder.buffer, message.message(s.account.ID, folder.folder))
			}
		}
	}
	return nil
}

func (s *adminGraphSource) next(ctx context.Context, count int) ([]Message, bool, error) {
	items := make([]Message, 0, count)
	for len(items) < count {
		if err := s.prime(ctx); err != nil {
			return nil, false, err
		}
		selected := -1
		for i := range s.folders {
			if len(s.folders[i].buffer) == 0 {
				continue
			}
			if selected == -1 || s.order == "oldest" && messageBefore(s.folders[i].buffer[0], s.folders[selected].buffer[0]) || s.order != "oldest" && messageAfter(s.folders[i].buffer[0], s.folders[selected].buffer[0]) {
				selected = i
			}
		}
		if selected == -1 {
			break
		}
		folder := &s.folders[selected]
		items = append(items, folder.buffer[0])
		folder.buffer = folder.buffer[1:]
	}
	more := false
	for _, folder := range s.folders {
		more = more || len(folder.buffer) > 0 || folder.nextURL != ""
	}
	return items, more, nil
}

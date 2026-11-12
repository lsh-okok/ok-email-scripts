package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

const adminIMAPMaxMessages = 100000

type adminIMAPEntry struct {
	source   MessageRef
	received time.Time
}

type adminIMAPSource struct {
	backend *liveBackend
	account Account
	proxies []string
	entries []adminIMAPEntry
	offset  int
}

func (s *adminIMAPSource) clone() adminPageSource {
	out := *s
	// The UID/date snapshot is immutable and may be shared by retries.
	return &out
}

func adminReadIMAP[T any](ctx context.Context, b *liveBackend, account Account, proxies []string, read func(*client.Client) (T, error)) (T, error) {
	var zero T
	if account.Provider != "outlook" || b.imapDial != nil {
		connection, close, err := b.imapConnect(ctx, account, proxies)
		if err != nil {
			return zero, err
		}
		defer close()
		return read(connection)
	}
	var failures []string
	for _, host := range outlookIMAPHosts {
		connection, close, err := b.imapConnectForHost(ctx, account, proxies, host)
		if err == nil {
			var value T
			value, err = read(connection)
			close()
			if err == nil {
				return value, nil
			}
		}
		if errors.Is(err, errAdminCursor) || ctx.Err() != nil {
			return zero, err
		}
		failures = append(failures, host+"："+err.Error())
	}
	return zero, errors.New(strings.Join(failures, "；"))
}

func (b *liveBackend) startAdminIMAPPage(ctx context.Context, account Account, proxies []string, order, folder string) (adminPageSource, error) {
	entries, err := adminReadIMAP(ctx, b, account, proxies, func(connection *client.Client) ([]adminIMAPEntry, error) {
		boxes, err := listMailboxes(connection)
		if err != nil {
			return nil, err
		}
		var out []adminIMAPEntry
		for _, name := range adminFolders(folder) {
			mailbox := mailboxFor(boxes, name)
			if mailbox == "" {
				continue
			}
			status, err := connection.Select(mailbox, true)
			if err != nil {
				return nil, fmt.Errorf("IMAP 文件夹 %s 不可访问：%w", name, err)
			}
			if status.UidValidity == 0 {
				return nil, errors.New("IMAP server did not provide UIDVALIDITY")
			}
			uids, err := connection.UidSearch(imap.NewSearchCriteria())
			if err != nil {
				return nil, fmt.Errorf("IMAP 文件夹 %s UID 查询失败：%w", name, err)
			}
			if len(uids) > adminIMAPMaxMessages-len(out) {
				return nil, errors.New("邮箱邮件数量过多，请选择单个文件夹后重试")
			}
			// INTERNALDATE is the receiving date. UID order alone cannot correctly
			// sort imported mail, so snapshot these small records before paging
			// headers. Neither message bodies nor all headers are downloaded.
			for start := 0; start < len(uids); start += 500 {
				set := new(imap.SeqSet)
				wanted := make(map[uint32]bool)
				for _, uid := range uids[start:min(start+500, len(uids))] {
					set.AddNum(uid)
					wanted[uid] = true
				}
				messages, err := fetchIMAP(connection, set, []imap.FetchItem{imap.FetchUid, imap.FetchInternalDate}, true)
				if err != nil {
					return nil, fmt.Errorf("IMAP 接收时间读取失败：%w", err)
				}
				for _, message := range messages {
					if message.Uid == 0 || !wanted[message.Uid] || message.InternalDate.IsZero() {
						return nil, errors.New("IMAP response omitted a message UID or receiving date")
					}
					delete(wanted, message.Uid)
					out = append(out, adminIMAPEntry{source: MessageRef{AccountID: account.ID, Protocol: "imap", Folder: name, Mailbox: mailbox, UID: message.Uid, UIDValidity: status.UidValidity}, received: message.InternalDate})
				}
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			left, right := Message{ReceivedAt: out[i].received, Source: out[i].source}, Message{ReceivedAt: out[j].received, Source: out[j].source}
			if order == "oldest" {
				return messageBefore(left, right)
			}
			return messageAfter(left, right)
		})
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return &adminIMAPSource{backend: b, account: account, proxies: proxies, entries: entries}, nil
}

func (s *adminIMAPSource) next(ctx context.Context, count int) ([]Message, bool, error) {
	if s.offset >= len(s.entries) {
		return []Message{}, false, nil
	}
	type result struct {
		items  []Message
		offset int
	}
	page, err := adminReadIMAP(ctx, s.backend, s.account, s.proxies, func(connection *client.Client) (result, error) {
		out := result{offset: s.offset}
		for len(out.items) < count && out.offset < len(s.entries) {
			entries := s.entries[out.offset:min(out.offset+count-len(out.items), len(s.entries))]
			groups := make(map[string][]adminIMAPEntry)
			for _, entry := range entries {
				groups[entry.source.Mailbox] = append(groups[entry.source.Mailbox], entry)
			}
			fetched := make(map[string]Message)
			for mailbox, group := range groups {
				status, err := connection.Select(mailbox, true)
				if err != nil {
					return result{}, fmt.Errorf("IMAP 文件夹不可访问：%w", err)
				}
				if status.UidValidity != group[0].source.UIDValidity {
					return result{}, errAdminCursor
				}
				set := new(imap.SeqSet)
				wanted := make(map[uint32]adminIMAPEntry)
				for _, entry := range group {
					set.AddNum(entry.source.UID)
					wanted[entry.source.UID] = entry
				}
				section := &imap.BodySectionName{BodyPartName: imap.BodyPartName{Specifier: imap.HeaderSpecifier}, Peek: true}
				messages, err := fetchIMAP(connection, set, []imap.FetchItem{imap.FetchUid, imap.FetchInternalDate, section.FetchItem()}, true)
				if err != nil {
					return result{}, fmt.Errorf("IMAP 邮件摘要读取失败：%w", err)
				}
				for _, message := range messages {
					entry, ok := wanted[message.Uid]
					if !ok || message.Uid == 0 || message.GetBody(section) == nil {
						return result{}, errors.New("IMAP response omitted a message UID or header")
					}
					item, err := headerMessage(message.GetBody(section), s.account.ID, entry.source.Folder, mailbox, message.Uid, status.UidValidity, entry.received)
					if err != nil {
						return result{}, errors.New("IMAP 邮件头解析失败")
					}
					fetched[messageIdentity(item)] = item
				}
			}
			// Preserve the snapshot's order, independent of FETCH response order.
			// A message deleted externally is simply absent; UIDs never shift.
			for _, entry := range entries {
				if item, ok := fetched[messageIdentity(Message{Source: entry.source})]; ok {
					out.items = append(out.items, item)
				}
			}
			out.offset += len(entries)
		}
		return out, nil
	})
	if err != nil {
		return nil, false, err
	}
	s.offset = page.offset
	return page.items, s.offset < len(s.entries), nil
}

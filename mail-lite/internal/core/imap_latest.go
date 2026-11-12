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

const (
	latestIMAPHeaderBatch = 20
	latestIMAPMaxMessages = 100000
)

type latestIMAPEntry struct {
	source   MessageRef
	received time.Time
	header   *Message
}

// imapLatestRecipient keeps the receiving-date scan and selected body read on one
// authenticated connection. Header downloads stop as soon as an exact matching
// message is found; UID order alone is insufficient for imported messages.
func (b *liveBackend) imapLatestRecipient(ctx context.Context, account Account, proxies []string, recipient string) ([]Message, error) {
	recipient = strings.TrimSpace(recipient)
	if recipient == "" || len(recipient) > 320 {
		return nil, errors.New("收件人地址无效")
	}
	return readIMAPHosts(ctx, b, account, func(host string) ([]Message, error) {
		connection, close, err := b.imapConnectForHost(ctx, account, proxies, host)
		if err != nil {
			return nil, err
		}
		defer close()
		entries, err := latestIMAPSnapshot(connection, account, recipient)
		if err != nil {
			return nil, err
		}
		for offset := 0; offset < len(entries); offset += latestIMAPHeaderBatch {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			batch := entries[offset:min(offset+latestIMAPHeaderBatch, len(entries))]
			matched, err := latestIMAPHeaders(connection, account, recipient, batch)
			if err != nil {
				return nil, err
			}
			// FETCH responses may arrive out of order. Retain the complete
			// two-folder snapshot order when selecting the final message.
			for _, entry := range batch {
				header, exists := matched[messageIdentity(Message{Source: entry.source})]
				if !exists {
					continue
				}
				message, err := imapDetailWithClient(connection, account, entry.source)
				if errors.Is(err, ErrNotFound) {
					// An externally deleted message must not shift another UID
					// into its place or prevent trying an older candidate.
					continue
				}
				if err != nil {
					return nil, err
				}
				if matchesAccountRecipient(account, message, recipient) {
					// Retain the decoded list metadata, matching the established
					// public path; only the selected body's content is merged.
					header.Text, header.HTML = message.Text, message.HTML
					return []Message{header}, nil
				}
			}
		}
		return []Message{}, nil
	})
}

func latestIMAPSnapshot(connection *client.Client, account Account, recipient string) ([]latestIMAPEntry, error) {
	boxes, err := listMailboxes(connection)
	if err != nil {
		return nil, err
	}
	entries := []latestIMAPEntry{}
	for _, folder := range []string{"inbox", "junkemail"} {
		mailbox := mailboxFor(boxes, folder)
		if mailbox == "" {
			continue
		}
		status, err := connection.Select(mailbox, true)
		if err != nil {
			return nil, fmt.Errorf("IMAP 文件夹 %s 不可访问：%w", folder, err)
		}
		if status.UidValidity == 0 {
			return nil, errors.New("IMAP server did not provide UIDVALIDITY")
		}
		criteria := imap.NewSearchCriteria()
		criteria.Header.Add("To", recipient)
		uids, err := connection.UidSearch(criteria)
		if err != nil {
			return nil, fmt.Errorf("IMAP 收件人查询失败：%w", err)
		}
		// Only UID/date metadata is read for the full matching set. Keep
		// memory and each command bounded even for a heavily used alias.
		if len(uids) > latestIMAPMaxMessages-len(entries) {
			return nil, errors.New("收件人邮件数量过多，请使用邮件列表查询")
		}
		// Small aliases need no separate metadata and header round trips.
		// Keep large result sets lightweight until their newest candidates
		// are known, but validate a small folder's headers in this same FETCH.
		withHeaders := len(uids) <= latestIMAPHeaderBatch
		section := &imap.BodySectionName{BodyPartName: imap.BodyPartName{Specifier: imap.HeaderSpecifier}, Peek: true}
		items := []imap.FetchItem{imap.FetchUid, imap.FetchInternalDate}
		if withHeaders {
			items = append(items, section.FetchItem())
		}
		for start := 0; start < len(uids); start += 1000 {
			set := new(imap.SeqSet)
			wanted := map[uint32]bool{}
			for _, uid := range uids[start:min(start+1000, len(uids))] {
				if uid == 0 {
					return nil, errors.New("IMAP response omitted a message UID")
				}
				set.AddNum(uid)
				wanted[uid] = true
			}
			messages, err := fetchIMAP(connection, set, items, true)
			if err != nil {
				return nil, fmt.Errorf("IMAP 接收时间读取失败：%w", err)
			}
			for _, message := range messages {
				if !wanted[message.Uid] || message.InternalDate.IsZero() {
					return nil, errors.New("IMAP response omitted a message UID or receiving date")
				}
				delete(wanted, message.Uid)
				entry := latestIMAPEntry{
					source:   MessageRef{AccountID: account.ID, Protocol: "imap", Folder: folder, Mailbox: mailbox, UID: message.Uid, UIDValidity: status.UidValidity},
					received: message.InternalDate,
				}
				if withHeaders {
					if message.GetBody(section) == nil {
						return nil, errors.New("IMAP response omitted a message header")
					}
					item, err := headerMessage(message.GetBody(section), account.ID, folder, mailbox, message.Uid, status.UidValidity, message.InternalDate)
					if err != nil {
						return nil, errors.New("IMAP 邮件头解析失败")
					}
					if !matchesAccountRecipient(account, item, recipient) {
						continue
					}
					entry.header = &item
				}
				entries = append(entries, entry)
			}
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return messageAfter(Message{ReceivedAt: entries[i].received, Source: entries[i].source}, Message{ReceivedAt: entries[j].received, Source: entries[j].source})
	})
	return entries, nil
}

func latestIMAPHeaders(connection *client.Client, account Account, recipient string, entries []latestIMAPEntry) (map[string]Message, error) {
	groups := map[string][]latestIMAPEntry{}
	mailboxes := []string{}
	matched := map[string]Message{}
	for _, entry := range entries {
		if entry.header != nil {
			matched[messageIdentity(Message{Source: entry.source})] = *entry.header
			continue
		}
		if _, exists := groups[entry.source.Mailbox]; !exists {
			mailboxes = append(mailboxes, entry.source.Mailbox)
		}
		groups[entry.source.Mailbox] = append(groups[entry.source.Mailbox], entry)
	}
	for _, mailbox := range mailboxes {
		group := groups[mailbox]
		status, err := connection.Select(mailbox, true)
		if err != nil {
			return nil, fmt.Errorf("IMAP 文件夹不可访问：%w", err)
		}
		if status.UidValidity != group[0].source.UIDValidity {
			return nil, errors.New("邮箱 UID 已变化，请刷新邮件列表")
		}
		set := new(imap.SeqSet)
		wanted := map[uint32]latestIMAPEntry{}
		for _, entry := range group {
			set.AddNum(entry.source.UID)
			wanted[entry.source.UID] = entry
		}
		section := &imap.BodySectionName{BodyPartName: imap.BodyPartName{Specifier: imap.HeaderSpecifier}, Peek: true}
		messages, err := fetchIMAP(connection, set, []imap.FetchItem{imap.FetchUid, section.FetchItem()}, true)
		if err != nil {
			return nil, fmt.Errorf("IMAP 邮件摘要读取失败：%w", err)
		}
		for _, message := range messages {
			entry, exists := wanted[message.Uid]
			if !exists || message.Uid == 0 || message.GetBody(section) == nil {
				return nil, errors.New("IMAP response omitted a message UID or header")
			}
			delete(wanted, message.Uid)
			item, err := headerMessage(message.GetBody(section), account.ID, entry.source.Folder, mailbox, message.Uid, status.UidValidity, entry.received)
			if err != nil {
				return nil, errors.New("IMAP 邮件头解析失败")
			}
			if matchesAccountRecipient(account, item, recipient) {
				matched[messageIdentity(item)] = item
			}
		}
	}
	return matched, nil
}

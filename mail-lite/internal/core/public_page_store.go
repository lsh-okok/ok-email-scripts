package core

import (
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const (
	publicPageSessionMaxBytes = 16 << 20
	publicPagesMaxBytes       = 64 << 20
	publicPagesMaxSessions    = 64
)

// Sessions are immutable after insertion, so retrying or concurrently using a
// cursor always reads the same snapshot. Only a small session reference travels
// in the URL; upstream IDs and remaining summaries stay in this bounded store.
type publicPageSession struct {
	cursor publicCursor
	items  []publicCursorItem
	bytes  int
}

func (s *publicPageSession) matches(c publicCursor) bool {
	expected := s.cursor
	return expected.Version == c.Version && expected.Session == c.Session &&
		expected.LinkID == c.LinkID && expected.AccountID == c.AccountID &&
		expected.Recipient == c.Recipient && expected.PlatformID == c.PlatformID &&
		expected.PolicyKey == c.PolicyKey && expected.Generation == c.Generation &&
		expected.Order == c.Order && expected.CreatedAt.Equal(c.CreatedAt) && expected.ExpiresAt.Equal(c.ExpiresAt)
}

type publicPageStore struct {
	mu       sync.Mutex
	sessions map[string]*publicPageSession
	bytes    int
}

func newPublicPageStore() *publicPageStore {
	return &publicPageStore{sessions: make(map[string]*publicPageSession)}
}

func (s *publicPageStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = make(map[string]*publicPageSession)
	s.bytes = 0
}

func (s *publicPageStore) remove(id string) {
	if session := s.sessions[id]; session != nil {
		s.bytes -= session.bytes
		delete(s.sessions, id)
	}
}

func (s *publicPageStore) prune(now time.Time) {
	for id, session := range s.sessions {
		if !now.Before(session.cursor.ExpiresAt) {
			s.remove(id)
		}
	}
}

func (s *publicPageStore) get(id string) *publicPageSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(time.Now())
	return s.sessions[id]
}

func (s *publicPageStore) put(session *publicPageSession) error {
	data, err := json.Marshal(session.items)
	if err != nil {
		return err
	}
	session.bytes = len(data)
	if session.bytes > publicPageSessionMaxBytes {
		return errors.New("邮件分页快照超过容量限制，请缩小邮箱范围后重试")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(time.Now())
	for id, current := range s.sessions {
		if current.cursor.AccountID != session.cursor.AccountID {
			continue
		}
		if current.cursor.Generation > session.cursor.Generation {
			return errPublicCursor
		}
		if current.cursor.Generation < session.cursor.Generation {
			s.remove(id)
		}
	}
	s.remove(session.cursor.Session)
	for len(s.sessions) >= publicPagesMaxSessions || s.bytes+session.bytes > publicPagesMaxBytes {
		var oldest *publicPageSession
		for _, current := range s.sessions {
			if oldest == nil || current.cursor.CreatedAt.Before(oldest.cursor.CreatedAt) {
				oldest = current
			}
		}
		s.remove(oldest.cursor.Session)
	}
	s.sessions[session.cursor.Session] = session
	s.bytes += session.bytes
	return nil
}

package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/remark/backend/app/store"
)

type MockDest struct {
	data   []Request
	id     int
	closed bool
	lock   sync.Mutex
}

func (m *MockDest) Send(ctx context.Context, r Request) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	select {
	case <-time.After(10 * time.Millisecond):
		m.data = append(m.data, r)
		log.Printf("sent %s -> %d", r.Comment.ID, m.id)
	case <-ctx.Done():
		log.Printf("ctx closed %d", m.id)
		m.closed = true
	}
	return nil
}

func (m *MockDest) Get() []Request {
	m.lock.Lock()
	defer m.lock.Unlock()
	res := make([]Request, len(m.data))
	copy(res, m.data)
	return res
}
func (m *MockDest) String() string { return fmt.Sprintf("mock id=%d, closed=%v", m.id, m.closed) }

type mockStore struct {
	data  map[string]store.Comment
	email bool
}

func (m mockStore) Get(_ store.Locator, id string, _ store.User) (store.Comment, error) {
	res, ok := m.data[id]
	if !ok {
		return store.Comment{}, errors.New("no such id")
	}
	return res, nil
}

func (m mockStore) GetUserEmail(_ store.Locator, userID string) (string, error) {
	if !m.email {
		return "", errors.New("no such user")
	}
	return "test@example.org", nil
}

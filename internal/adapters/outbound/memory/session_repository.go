package memory

import (
	"context"
	"sync"

	app "meal-recommendation/internal/application/recommendation"
	domain "meal-recommendation/internal/domain/recommendation"
)

type SessionRepository struct {
	mu       sync.RWMutex
	sessions map[string]*domain.Session
}

func NewSessionRepository() *SessionRepository {
	return &SessionRepository{sessions: make(map[string]*domain.Session)}
}

func (r *SessionRepository) Create(_ context.Context, session *domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[session.ID] = session.Clone()
	return nil
}

func (r *SessionRepository) Get(_ context.Context, id string) (*domain.Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	session, ok := r.sessions[id]
	if !ok {
		return nil, app.ErrSessionNotFound
	}
	return session.Clone(), nil
}

func (r *SessionRepository) Save(_ context.Context, session *domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[session.ID]; !ok {
		return app.ErrSessionNotFound
	}
	r.sessions[session.ID] = session.Clone()
	return nil
}

var _ app.SessionRepository = (*SessionRepository)(nil)

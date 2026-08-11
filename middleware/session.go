package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const CookieName = "PSW_SESSION_ID"
const CSRFTokenHeaderName = "X-CSRF-Token"

type Session struct {
	Username  string
	ExpiresAt time.Time
	CSRFToken string
}

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]Session
	ttl      time.Duration
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	store := &SessionStore{sessions: map[string]Session{}, ttl: ttl}
	go store.cleanLoop()
	return store
}

func (s *SessionStore) Create(username string) (string, string, error) {
	// Sixteen random bytes encode to the requested 32-character session ID.
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", "", err
	}
	id := hex.EncodeToString(idBytes)
	csrfToken, err := randomHex(32)
	if err != nil {
		return "", "", err
	}
	s.mu.Lock()
	s.sessions[id] = Session{Username: username, ExpiresAt: time.Now().Add(s.ttl), CSRFToken: csrfToken}
	s.mu.Unlock()
	return id, csrfToken, nil
}

func (s *SessionStore) Get(r *http.Request) (Session, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return Session{}, false
	}
	s.mu.RLock()
	session, ok := s.sessions[c.Value]
	s.mu.RUnlock()
	if !ok || time.Now().After(session.ExpiresAt) {
		if ok {
			s.Delete(c.Value)
		}
		return Session{}, false
	}
	return session, true
}

func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

func (s *SessionStore) DeleteByRequest(r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		s.Delete(c.Value)
	}
}

func (s *SessionStore) cleanLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for id, session := range s.sessions {
			if now.After(session.ExpiresAt) {
				delete(s.sessions, id)
			}
		}
		s.mu.Unlock()
	}
}

func SetCookie(w http.ResponseWriter, id string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
	})
}

func ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func RequireAuth(store *SessionStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := store.Get(r)
		if !ok {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"ok":false,"error":"unauthorized"}`))
			return
		}
		if requiresCSRFCheck(r.Method) {
			headerToken := r.Header.Get(CSRFTokenHeaderName)
			if headerToken != session.CSRFToken {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"ok":false,"error":"csrf token invalid"}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func requiresCSRFCheck(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

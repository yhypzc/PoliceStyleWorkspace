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

// TTL is the idle window: a session stays valid for this long after the last
// request that counted as user activity.
func (s *SessionStore) TTL() time.Duration { return s.ttl }

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

// Get validates the session cookie and slides its idle deadline forward, so a
// session lives `ttl` after the last request rather than `ttl` after login.
func (s *SessionStore) Get(r *http.Request) (Session, bool) {
	return s.load(r, true)
}

// GetPassive validates without sliding the deadline. Background polling (the
// clock sync) uses this so that an open-but-idle tab cannot keep itself signed
// in: only real activity extends the session.
func (s *SessionStore) GetPassive(r *http.Request) (Session, bool) {
	return s.load(r, false)
}

func (s *SessionStore) load(r *http.Request, refresh bool) (Session, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return Session{}, false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[c.Value]
	if !ok || now.After(session.ExpiresAt) {
		if ok {
			delete(s.sessions, c.Value)
		}
		return Session{}, false
	}
	if refresh {
		session.ExpiresAt = now.Add(s.ttl)
		s.sessions[c.Value] = session
	}
	return session, true
}

// Remaining reports how long the given session stays valid from now. It is used
// by the frontend to schedule its own idle logout.
func (s *SessionStore) Remaining(r *http.Request) time.Duration {
	session, ok := s.load(r, false)
	if !ok {
		return 0
	}
	if remaining := time.Until(session.ExpiresAt); remaining > 0 {
		return remaining
	}
	return 0
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
	return requireAuth(store, next, true)
}

// RequireAuthPassive authenticates without extending the session. It is used
// for background polling endpoints so that an idle tab still times out.
func RequireAuthPassive(store *SessionStore, next http.Handler) http.Handler {
	return requireAuth(store, next, false)
}

func requireAuth(store *SessionStore, next http.Handler, refresh bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := store.load(r, refresh)
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
		// 会话续期的同时把 Cookie 的到期时间一起往后推，否则浏览器会先丢掉
		// Cookie，服务端还活着的会话也再用不上。
		if refresh {
			if c, err := r.Cookie(CookieName); err == nil {
				SetCookie(w, c.Value, store.TTL())
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

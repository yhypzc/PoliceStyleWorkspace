package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"PoliceStyleWorkspace/middleware"
	"PoliceStyleWorkspace/models"
)

type App struct {
	DB         *sql.DB
	Sessions   *middleware.SessionStore
	LogPath    string
	ConfigDir  string
	httpServer *http.Server
	stop       func()
	loginGuard *loginGuard
}

func NewApp(db *sql.DB, sessions *middleware.SessionStore, logPath string, configDir ...string) *App {
	app := &App{DB: db, Sessions: sessions, LogPath: logPath, loginGuard: newLoginGuard()}
	if len(configDir) > 0 {
		app.ConfigDir = configDir[0]
	}
	return app
}

func (a *App) SetServer(s *http.Server) { a.httpServer = s }
func (a *App) SetStopFunc(stop func())  { a.stop = stop }

func (a *App) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username  string         `json:"username"`
		Password  string         `json:"password"`
		Timestamp loginTimestamp `json:"timestamp"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	timestamp := req.Timestamp.Value
	if timestamp <= 0 {
		timestamp, _ = strconv.ParseInt(strings.TrimSpace(r.Header.Get("X-Login-Timestamp")), 10, 64)
	}
	if guardErr := a.loginGuard.check(r, req.Username, timestamp); guardErr != nil {
		writeError(w, guardErr.status, guardErr.message)
		return
	}
	success := false
	defer func() {
		a.loginGuard.finish(r, req.Username, success)
	}()
	ok, err := models.ValidateLogin(a.DB, req.Username, req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	sessionID, csrfToken, err := a.Sessions.Create(req.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	middleware.SetCookie(w, sessionID, 30*time.Minute)
	success = true
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": models.User{Username: req.Username}, "csrf_token": csrfToken})
}

func (a *App) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "新密码长度至少 8 位")
		return
	}
	if err := models.ChangePassword(a.DB, req.OldPassword, req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Sessions.DeleteByRequest(r)
	middleware.ClearCookie(w)
	log.Println("[安全] admin 修改密码成功，当前 session 已失效")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) CheckAuth(w http.ResponseWriter, r *http.Request) {
	session, _ := a.Sessions.Get(r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "authenticated": true, "username": session.Username})
}

func (a *App) Logout(w http.ResponseWriter, r *http.Request) {
	a.Sessions.DeleteByRequest(r)
	middleware.ClearCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 请求体无效")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": msg})
}

type loginGuardError struct {
	status  int
	message string
}

type loginTimestamp struct {
	Value int64
}

func (t *loginTimestamp) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		t.Value = 0
		return nil
	}
	raw = strings.Trim(raw, `"`)
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return err
	}
	t.Value = value
	return nil
}

type loginAttemptState struct {
	inFlight    bool
	failCount   int
	failStarted time.Time
	lockedUntil time.Time
}

type loginGuard struct {
	mu            sync.Mutex
	seen          map[string]time.Time
	states        map[string]*loginAttemptState
	window        time.Duration
	skew          time.Duration
	seenTTL       time.Duration
	lockThreshold int
	lockDuration  time.Duration
}

func newLoginGuard() *loginGuard {
	return &loginGuard{
		seen:          map[string]time.Time{},
		states:        map[string]*loginAttemptState{},
		window:        3 * time.Second,
		skew:          2 * time.Minute,
		seenTTL:       10 * time.Minute,
		lockThreshold: 5,
		lockDuration:  10 * time.Second,
	}
}

func (g *loginGuard) check(r *http.Request, username string, timestamp int64) *loginGuardError {
	username = strings.TrimSpace(username)
	if username == "" {
		return &loginGuardError{status: http.StatusBadRequest, message: "用户名不能为空"}
	}
	if timestamp <= 0 {
		return &loginGuardError{status: http.StatusBadRequest, message: "登录时间戳不能为空"}
	}
	now := time.Now()
	ts := time.UnixMilli(timestamp)
	if ts.Before(now.Add(-g.skew)) || ts.After(now.Add(g.skew)) {
		return &loginGuardError{status: http.StatusBadRequest, message: "登录请求已过期，请刷新后重试"}
	}
	key := g.key(r, username)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cleanupLocked(now)
	state := g.stateLocked(key)
	if now.Before(state.lockedUntil) {
		return &loginGuardError{status: http.StatusTooManyRequests, message: "请求过于频繁"}
	}
	if state.inFlight {
		return &loginGuardError{status: http.StatusTooManyRequests, message: "登录请求处理中，请稍后再试"}
	}
	tsKey := keyWithTimestamp(key, timestamp)
	if _, ok := g.seen[tsKey]; ok {
		return &loginGuardError{status: http.StatusBadRequest, message: "登录请求已提交，请刷新后重试"}
	}
	state.inFlight = true
	g.seen[tsKey] = now
	return nil
}

func (g *loginGuard) finish(r *http.Request, username string, success bool) {
	username = strings.TrimSpace(username)
	if username == "" {
		return
	}
	key := g.key(r, username)
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.stateLocked(key)
	state.inFlight = false
	if success {
		state.failCount = 0
		state.failStarted = time.Time{}
		state.lockedUntil = time.Time{}
		return
	}
	if state.failStarted.IsZero() || !now.Before(state.failStarted.Add(g.window)) {
		state.failStarted = now
		state.failCount = 0
	}
	state.failCount++
	if state.failCount >= g.lockThreshold {
		state.lockedUntil = now.Add(g.lockDuration)
		state.failCount = 0
		state.failStarted = time.Time{}
	}
}

func (g *loginGuard) stateLocked(key string) *loginAttemptState {
	state, ok := g.states[key]
	if !ok {
		state = &loginAttemptState{}
		g.states[key] = state
	}
	return state
}

func (g *loginGuard) cleanupLocked(now time.Time) {
	cutoff := now.Add(-g.seenTTL)
	for key, ts := range g.seen {
		if ts.Before(cutoff) {
			delete(g.seen, key)
		}
	}
	for key, state := range g.states {
		if !state.failStarted.IsZero() && !now.Before(state.failStarted.Add(g.window)) {
			state.failStarted = time.Time{}
			state.failCount = 0
		}
		if !state.inFlight && state.failCount == 0 && now.After(state.lockedUntil) {
			delete(g.states, key)
		}
	}
}

func (g *loginGuard) key(r *http.Request, username string) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	return remote + "|" + username
}

func keyWithTimestamp(key string, timestamp int64) string {
	return fmt.Sprintf("%s|%d", key, timestamp)
}

// ── Semester handlers ──

func (a *App) ListSemesters(w http.ResponseWriter, r *http.Request) {
	list, err := models.ListSemesters(a.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []models.Semester{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "semesters": list})
}

func (a *App) CreateSemester(w http.ResponseWriter, r *http.Request) {
	var s models.Semester
	if !decodeJSON(w, r, &s) {
		return
	}
	if err := models.CreateSemester(a.DB, s); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "semester": s})
}

func (a *App) GetSemester(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "学期名称不能为空")
		return
	}
	s, err := models.GetSemester(a.DB, name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "semester": s})
}

func (a *App) UpdateSemester(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "学期名称不能为空")
		return
	}
	var s models.Semester
	if !decodeJSON(w, r, &s) {
		return
	}
	s.Name = name
	if err := models.UpdateSemester(a.DB, s); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "semester": s})
}

func (a *App) DeleteSemester(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "学期名称不能为空")
		return
	}
	if err := models.DeleteSemester(a.DB, name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

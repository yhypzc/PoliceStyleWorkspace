package handlers

import (
	"net/http"
	"time"
)

// ServerClock returns the server process time for client clock calibration.
func (a *App) ServerClock(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"unix_milliseconds": time.Now().UnixMilli(),
	})
}

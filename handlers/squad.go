package handlers

import (
	"net/http"

	"PoliceStyleWorkspace/models"
)

func (a *App) GetSquad(w http.ResponseWriter, r *http.Request) {
	name, err := models.GetSquadName(a.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "squad_name": name})
}

func (a *App) UpdateSquad(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SquadName string `json:"squad_name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := models.SetSquadName(a.DB, req.SquadName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "squad_name": req.SquadName})
}

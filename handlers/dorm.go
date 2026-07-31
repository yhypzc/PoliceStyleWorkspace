package handlers

import (
	"log"
	"net/http"

	"PoliceStyleWorkspace/models"
)

func (a *App) ListDorms(w http.ResponseWriter, r *http.Request) {
	dorms, err := models.ListDorms(a.DB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dorms": dorms})
}

func (a *App) CreateDorm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"dorm_name"`
		PhoneNumber string `json:"phone_number"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	dorm, err := models.CreateDorm(a.DB, req.Name, req.PhoneNumber)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[寝室] 新增 %q", dorm.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "dorm": dorm})
}

func (a *App) UpdateDorm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"dorm_name"`
		PhoneNumber string `json:"phone_number"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := models.UpdateDorm(a.DB, r.PathValue("name"), req.Name, req.PhoneNumber); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) DeleteDorm(w http.ResponseWriter, r *http.Request) {
	if err := models.DeleteDorm(a.DB, r.PathValue("name")); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) ReorderDorms(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Names []string `json:"names"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := models.ReorderDorms(a.DB, req.Names); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

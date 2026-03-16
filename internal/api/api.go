package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"rto-tracker/internal/db"
	"rto-tracker/internal/policy"
)

// Handler holds the API routes.
type Handler struct {
	store *db.Store
	mux   *http.ServeMux
}

// New creates a new API handler.
func New(store *db.Store) *Handler {
	h := &Handler{store: store, mux: http.NewServeMux()}
	h.registerRoutes()
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) registerRoutes() {
	h.mux.HandleFunc("/api/badge", h.handleBadge)
	h.mux.HandleFunc("/api/badges", h.handleBadges)
	h.mux.HandleFunc("/api/status", h.handleStatus)
	h.mux.HandleFunc("/api/policies", h.handlePolicies)
	h.mux.HandleFunc("/api/policy/activate", h.handleActivatePolicy)
	h.mux.HandleFunc("/api/policy", h.handlePolicy)
}

// POST /api/badge — badge in (optional ?date=YYYY-MM-DD, defaults to today)
// DELETE /api/badge?date=YYYY-MM-DD — remove a badge
func (h *Handler) handleBadge(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		date := r.URL.Query().Get("date")
		if date == "" {
			today, err := h.store.BadgeInToday()
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, map[string]string{"date": today, "status": "recorded"})
			return
		}
		if err := h.store.BadgeIn(date); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"date": date, "status": "recorded"})

	case http.MethodDelete:
		date := r.URL.Query().Get("date")
		if date == "" {
			writeError(w, http.StatusBadRequest, "date parameter required")
			return
		}
		if err := h.store.DeleteBadge(date); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "deleted"})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// GET /api/badges — list all badges
func (h *Handler) handleBadges(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	badges, err := h.store.GetBadges()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if badges == nil {
		badges = []db.BadgeRecord{}
	}
	writeJSON(w, badges)
}

// GET /api/status — compliance status
func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	status, err := policy.Evaluate(h.store)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, status)
}

// GET /api/policies — list all policies
// POST /api/policies — create a new policy
func (h *Handler) handlePolicies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		policies, err := h.store.GetPolicies()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if policies == nil {
			policies = []db.PolicyConfig{}
		}
		writeJSON(w, policies)

	case http.MethodPost:
		var p db.PolicyConfig
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		id, err := h.store.CreatePolicy(p)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		p.ID = id
		writeJSON(w, p)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// POST /api/policy/activate?id=N — set a policy as active
func (h *Handler) handleActivatePolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.store.SetActivePolicy(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "activated"})
}

// DELETE /api/policy?id=N — delete a policy
func (h *Handler) handlePolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.store.DeletePolicy(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "deleted"})
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

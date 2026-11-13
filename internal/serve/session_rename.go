package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"reasonix/internal/control"
	"reasonix/internal/session"
)

// renameSession sets a user-provided title on one canonical session. Titles
// live in the session's own event log, so only rows carrying a sessionId (the
// canonical catalog) can be renamed; legacy transcripts keep the title derived
// from their filename and first message, so they are rejected here.
func (s *Server) renameSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
		Title     string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sessionID := strings.TrimSpace(req.SessionID)
	// Collapse internal whitespace: a title renders on a single sidebar line,
	// so newlines and runs of blanks would break the row layout.
	title := strings.Join(strings.Fields(req.Title), " ")
	if sessionID == "" {
		http.Error(w, "sessionId required", http.StatusBadRequest)
		return
	}
	if title == "" {
		http.Error(w, "title required", http.StatusBadRequest)
		return
	}
	if runes := []rune(title); len(runes) > 200 {
		title = string(runes[:200])
	}
	identity, ok := s.ctl().(control.IdentityLifecycle)
	if !ok || !identity.UsesExclusiveSession() {
		http.Error(w, "canonical sessions disabled", http.StatusBadRequest)
		return
	}
	service := identity.SessionService()
	if service == nil {
		http.Error(w, "canonical sessions disabled", http.StatusBadRequest)
		return
	}
	ref := session.SessionRef{HostID: service.HostID(), SessionID: sessionID}
	if err := service.SetTitle(r.Context(), ref, title); err != nil {
		switch {
		case errors.Is(err, session.ErrSessionNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
		case errors.Is(err, session.ErrRuntimeBusy), errors.Is(err, session.ErrRuntimeBound):
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

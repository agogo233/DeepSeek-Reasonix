package serve

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"reasonix/internal/agent"
	"reasonix/internal/control"
	"reasonix/internal/session"
	"reasonix/internal/store"
)

// deleteSessions bulk-clears sessions from the web UI. mode "empty" prunes
// only turn-less sessions (the artifacts repeated `reasonix serve` starts
// leave behind); mode "all" clears every session the server can delete.
// Canonical sessions go through the shared Service.Prune; legacy transcripts
// are removed through the same ownership gate as the single-delete endpoint.
// The active and running sessions are skipped and reported as busy. dryRun
// only previews the match count, which the UI uses for its confirmation text.
func (s *Server) deleteSessions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode   string `json:"mode"`
		DryRun bool   `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Mode != "empty" && req.Mode != "all" {
		http.Error(w, "invalid mode", http.StatusBadRequest)
		return
	}
	report := session.PruneReport{Errors: []string{}, DryRun: req.DryRun}
	if identity, ok := s.ctl().(control.IdentityLifecycle); ok && identity.UsesExclusiveSession() {
		if service := identity.SessionService(); service != nil {
			prune := service.Prune(r.Context(), session.PruneOptions{
				EmptyOnly: req.Mode == "empty",
				DryRun:    req.DryRun,
			})
			mergePruneReport(&report, &prune)
		}
	}
	s.pruneLegacySessions(r, req.Mode, req.DryRun, &report)
	writeJSON(w, report)
}

// pruneLegacySessions removes matching legacy transcripts through the same
// ownership gate the single delete uses. Each transcript is checked and
// removed under its own bindMu acquisition, so a concurrent promote between
// two legacy deletions never frees a live controller's transcript.
func (s *Server) pruneLegacySessions(r *http.Request, mode string, dryRun bool, report *session.PruneReport) {
	dir := s.ctl().SessionDir()
	if dir == "" {
		return
	}
	entries, ok := readSessionDir(dir)
	if !ok {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !store.IsSessionTranscriptName(entry.Name()) {
			continue
		}
		path := agent.CanonicalSessionPath(filepath.Join(dir, entry.Name()))
		if agent.IsCleanupPending(path) {
			continue
		}
		if mode == "empty" {
			first, turns, cached := agent.SessionPreviewCached(path)
			if !cached {
				first, turns = agent.SessionPreview(path)
			}
			_ = first
			if turns > 0 {
				continue
			}
		}
		report.Matched++
		if dryRun {
			report.Deleted++
			continue
		}
		s.deleteOneLegacyTranscript(filepath.Clean(path), report)
	}
}

// deleteOneLegacyTranscript removes one legacy transcript under the ownership
// gate, classifying a live-session refusal as busy rather than an error.
func (s *Server) deleteOneLegacyTranscript(abs string, report *session.PruneReport) {
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if msg, status := s.legacyTranscriptDeleteRefusalLocked(abs); msg != "" {
		if status == http.StatusConflict {
			report.Busy++
			report.BusySessionID = append(report.BusySessionID, filepath.Base(abs))
			return
		}
		report.Errors = append(report.Errors, msg)
		return
	}
	if err := s.destroyLegacyTranscriptLocked(filepath.Dir(abs), abs); err != nil {
		report.Errors = append(report.Errors, err.Error())
		return
	}
	report.Deleted++
	report.DeletedSessionID = append(report.DeletedSessionID, filepath.Base(abs))
}

func mergePruneReport(dst, src *session.PruneReport) {
	dst.Matched += src.Matched
	dst.Deleted += src.Deleted
	dst.Busy += src.Busy
	dst.Unknown += src.Unknown
	dst.AlreadyDeleted += src.AlreadyDeleted
	dst.Errors = append(dst.Errors, src.Errors...)
	dst.DeletedSessionID = append(dst.DeletedSessionID, src.DeletedSessionID...)
	dst.BusySessionID = append(dst.BusySessionID, src.BusySessionID...)
}

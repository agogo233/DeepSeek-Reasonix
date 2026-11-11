package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"reasonix/internal/state/sessionstore"
)

// sweepSessions previews which conversations a cleanup would remove, deleting
// nothing: every workspace the hub lists is walked, each session is matched
// against the caller's filters, and pinned, running and recovery-copy rows are
// held back. The caller confirms and drives the actual removal through the
// remove-many route, so a batch is either validated whole or refused whole.
//
// The idle filter uses each session's last-activity time; the empty filter
// re-proves a candidate holds no user message before it is offered, so a cold
// sidecar can never talk a cleanup into erasing a conversation that has one.
func (h *Hub) sweepSessions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OlderThanDays   int  `json:"olderThanDays"`
		Empty           bool `json:"empty"`
		All             bool `json:"all"`
		IncludeArchived bool `json:"includeArchived"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		badBody(w)
		return
	}
	if req.OlderThanDays < 0 || req.OlderThanDays > 3650 {
		refuse(w, http.StatusBadRequest, codeBadValue, "the idle period is out of range", nil)
		return
	}
	if !req.All && !req.Empty && req.OlderThanDays == 0 {
		refuse(w, http.StatusBadRequest, codeBadValue, "a sweep needs all, empty, or an idle period", nil)
		return
	}

	var cutoff time.Time
	if req.OlderThanDays > 0 {
		cutoff = time.Now().AddDate(0, 0, -req.OlderThanDays)
	}

	type row struct {
		Path       string    `json:"path"`
		Title      string    `json:"title"`
		LastActive time.Time `json:"lastActive"`
		Archived   bool      `json:"archived"`
		Turns      int       `json:"turns"`
	}
	report := struct {
		Matched   int      `json:"matched"`
		Rows      []row    `json:"rows"`
		Pinned    int      `json:"pinned"`
		Running   int      `json:"running"`
		Recovered int      `json:"recovered"`
		Errors    []string `json:"errors"`
	}{
		Rows: []row{},
	}
	open := h.openSessions()
	now := time.Now()
	for _, ref := range h.roots() {
		dir := SessionDirFor(ref.dir)
		if _, err := os.Stat(dir); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			report.Errors = append(report.Errors, dir+": "+err.Error())
			continue
		}
		rows, err := sessionstore.ListSessionOrder(dir)
		if err != nil {
			report.Errors = append(report.Errors, dir+": "+err.Error())
			continue
		}
		var empty map[string]bool
		if req.Empty {
			paths, err := sessionstore.ReclaimableEmptySessions(dir, now, sessionstore.EmptySessionGracePeriod)
			if err != nil {
				report.Errors = append(report.Errors, dir+": "+err.Error())
				continue
			}
			empty = make(map[string]bool, len(paths))
			for _, p := range paths {
				empty[p] = true
			}
		}
		for _, s := range rows {
			if s.Recovered {
				report.Recovered++
				continue
			}
			if s.Pinned {
				report.Pinned++
				continue
			}
			if !req.IncludeArchived && s.Archived {
				continue
			}
			if open[sessionstore.CanonicalSessionPath(s.Path)] != "" {
				report.Running++
				continue
			}
			if !req.All {
				if req.Empty && !empty[filepath.Clean(s.Path)] {
					continue
				}
				// A session with no last-activity record is kept: matching it
				// as "idle" would let a metadata gap talk the sweep into
				// erasing a conversation whose age is unknown.
				if req.OlderThanDays > 0 && (s.LastActivityAt.IsZero() || !s.LastActivityAt.Before(cutoff)) {
					continue
				}
			}
			report.Matched++
			title := s.CustomTitle
			if title == "" {
				title = s.TopicTitle
			}
			report.Rows = append(report.Rows, row{s.Path, title, s.LastActivityAt, s.Archived, s.Turns})
		}
	}
	writeJSON(w, report)
}

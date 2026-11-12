package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	filelock "reasonix/internal/identitylock"
)

const (
	// prunePageLimit is the catalog cursor page size. The walk follows the
	// cursor to the end without an artificial row ceiling; a broken cursor is
	// caught by the strict-advance check rather than silently truncating the
	// accumulated sessions a host asks to prune.
	prunePageLimit = 100
	// pruneLockTimeout bounds one delete against a resident runtime that holds
	// the session's writer lock. Busy sessions are skipped, not waited on
	// forever.
	pruneLockTimeout = 5 * time.Second
)

// PruneOptions selects which sessions a prune touches. A zero value matches
// every session; EmptyOnly narrows the match to sessions with no completed
// turns, and a non-zero Cutoff keeps only sessions idle before it. The two
// combine as AND, which is how the CLI's --empty/--older-than filters work.
type PruneOptions struct {
	EmptyOnly bool
	Cutoff    time.Time
	DryRun    bool
}

// PruneReport is the outcome of one Prune call, shared by the CLI and the
// serve endpoint so both surfaces report the same numbers.
type PruneReport struct {
	Matched          int      `json:"matched"`
	Deleted          int      `json:"deleted"`
	Busy             int      `json:"busy"`
	Unknown          int      `json:"unknown"` // empty-filter candidates whose metadata could not be refreshed
	AlreadyDeleted   int      `json:"alreadyDeleted"`
	Errors           []string `json:"errors"`
	DeletedSessionID []string `json:"deletedSessionIds,omitempty"`
	BusySessionID    []string `json:"busySessionIds,omitempty"`
	DryRun           bool     `json:"dryRun"`
}

// Prune walks the canonical store this service owns and deletes (or, in a dry
// run, counts) the sessions matching opts. It is the single logic source for
// bulk cleanup behind the CLI and the web UI. Deletion reuses Service.Delete,
// so a runtime that still has client bindings or active work is refused and
// reported as busy instead of being torn down underneath a user.
func (s *Service) Prune(ctx context.Context, opts PruneOptions) PruneReport {
	report := PruneReport{Errors: []string{}, DryRun: opts.DryRun}
	if s == nil {
		report.Errors = append(report.Errors, "prune: nil session service")
		return report
	}
	query := s.Query()
	if query == nil {
		report.Errors = append(report.Errors, "prune: nil session query")
		return report
	}
	matches := []SessionRef{}
	cursor := ""
	for {
		page, err := query.List(ctx, cursor, prunePageLimit)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("prune: list: %v", err))
			return report
		}
		for i := range page.Sessions {
			info := page.Sessions[i]
			if !pruneMatches(ctx, query, info, opts, &report) {
				continue
			}
			report.Matched++
			matches = append(matches, info.Ref)
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor <= cursor {
			report.Errors = append(report.Errors, "prune: catalog cursor did not advance")
			return report
		}
		cursor = page.NextCursor
	}
	if opts.DryRun {
		// A dry run reports the sessions that would be deleted, matching the
		// CLI's "would delete: N" summary and its id preview. Busy is
		// unknowable without attempting the delete, so it stays zero.
		report.Deleted = report.Matched
		for _, ref := range matches {
			report.DeletedSessionID = append(report.DeletedSessionID, ref.SessionID)
		}
		return report
	}
	for _, ref := range matches {
		if !pruneDelete(ctx, s, ref, &report) {
			continue
		}
		report.Deleted++
		report.DeletedSessionID = append(report.DeletedSessionID, ref.SessionID)
	}
	return report
}

// pruneMatches applies the AND-ed filters to one catalog row. A cold catalog
// row can carry a stale Turns==0 before its metadata is rebuilt, so the empty
// filter refreshes metadata synchronously before trusting a zero turn count;
// a filled session is then never matched as empty. Age uses the row's
// UpdatedAt (initialised from CreatedAt and advanced by the revision file),
// which is never zero. Sessions whose refresh fails are counted as unknown
// and left alone.
func pruneMatches(ctx context.Context, query *Query, info SessionInfo, opts PruneOptions, report *PruneReport) bool {
	if opts.EmptyOnly && info.Turns == 0 {
		refreshed, err := query.RefreshMetadata(ctx, info.Ref)
		if err != nil {
			report.Unknown++
			return false
		}
		info = refreshed
	}
	if opts.EmptyOnly && info.Turns > 0 {
		return false
	}
	if !opts.Cutoff.IsZero() && !info.UpdatedAt.Before(opts.Cutoff) {
		return false
	}
	return true
}

// pruneDelete deletes one session with a bounded lock wait and classifies a
// resident-runtime hold as busy rather than an error. Service.Delete closes
// any local runtime first, so this path never deletes a directory out from
// under a writer; a foreign process that holds the writer lock simply times
// out and is reported as busy.
func pruneDelete(ctx context.Context, service *Service, ref SessionRef, report *PruneReport) bool {
	deleteCtx, cancel := context.WithTimeout(ctx, pruneLockTimeout)
	defer cancel()
	err := service.Delete(deleteCtx, ref)
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, filelock.ErrHeld),
		errors.Is(err, ErrRuntimeBusy),
		errors.Is(err, ErrRuntimeBound):
		report.Busy++
		report.BusySessionID = append(report.BusySessionID, ref.SessionID)
		return false
	case errors.Is(err, ErrSessionNotFound):
		report.AlreadyDeleted++
		return false
	default:
		report.Errors = append(report.Errors, fmt.Sprintf("prune: delete %s: %v", ref.SessionID, err))
		return false
	}
}

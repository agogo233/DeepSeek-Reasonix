package session

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func createPruneSession(t *testing.T, service *Service, id, prompt string) {
	t.Helper()
	prepared, err := service.PrepareCreate(t.Context(), CreateOptions{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	sess := prepared.Runtime().Session()
	if prompt != "" {
		if _, err := sess.AppendBatch(t.Context(), "seed", []Event{
			{Kind: "message/complete", Payload: []byte(`{"message":{"id":"` + id + `-m1","role":"user","content":"` + prompt + `"}}`)},
			{Kind: "turn/start"},
			{Kind: "turn/end", Payload: []byte(`{"status":"completed"}`)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sess.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	published, err := service.Publish(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), published.Runtime().Ref()); err != nil {
		t.Fatal(err)
	}
}

func newPruneService(t *testing.T) *Service {
	t.Helper()
	root := filepath.Join(t.TempDir(), "sessions-v4")
	service, err := NewService("prune-test", NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	return service
}

func TestPruneEmptyDeletesOnlyTurnlessSessions(t *testing.T) {
	service := newPruneService(t)
	createPruneSession(t, service, "empty-a", "")
	createPruneSession(t, service, "empty-b", "")
	createPruneSession(t, service, "filled", "hello")

	report := service.Prune(t.Context(), PruneOptions{EmptyOnly: true})
	if report.Matched != 2 || report.Deleted != 2 {
		t.Fatalf("matched=%d deleted=%d, want 2/2", report.Matched, report.Deleted)
	}
	if len(report.Errors) != 0 {
		t.Fatalf("errors: %v", report.Errors)
	}
	remaining := listPruneIDs(t, service)
	if len(remaining) != 1 || remaining[0] != "filled" {
		t.Fatalf("remaining = %v, want [filled]", remaining)
	}
}

func TestPruneAllDeletesEverything(t *testing.T) {
	service := newPruneService(t)
	createPruneSession(t, service, "empty", "")
	createPruneSession(t, service, "filled", "hello")

	report := service.Prune(t.Context(), PruneOptions{})
	if report.Matched != 2 || report.Deleted != 2 {
		t.Fatalf("matched=%d deleted=%d, want 2/2", report.Matched, report.Deleted)
	}
	if remaining := listPruneIDs(t, service); len(remaining) != 0 {
		t.Fatalf("remaining = %v, want none", remaining)
	}
}

func TestPruneDryRunKeepsSessions(t *testing.T) {
	service := newPruneService(t)
	createPruneSession(t, service, "empty", "")
	createPruneSession(t, service, "filled", "hello")

	report := service.Prune(t.Context(), PruneOptions{EmptyOnly: true, DryRun: true})
	if !report.DryRun || report.Matched != 1 || report.Deleted != 1 {
		t.Fatalf("dryRun=%v matched=%d deleted=%d, want true/1/1", report.DryRun, report.Matched, report.Deleted)
	}
	if remaining := listPruneIDs(t, service); len(remaining) != 2 {
		t.Fatalf("dry run removed sessions: %v", remaining)
	}
}

func TestPruneCutoffKeepsRecent(t *testing.T) {
	service := newPruneService(t)
	createPruneSession(t, service, "old", "")
	createPruneSession(t, service, "recent", "")
	// A cutoff a day in the past is before every just-created session's
	// UpdatedAt, so neither is idle long enough and the age filter matches
	// nothing — fresh sessions are never mistaken for ancient ones.
	report := service.Prune(t.Context(), PruneOptions{Cutoff: time.Now().Add(-24 * time.Hour)})
	if report.Matched != 0 || report.Deleted != 0 {
		t.Fatalf("matched=%d deleted=%d, want 0/0 (both newer than cutoff)", report.Matched, report.Deleted)
	}
	if remaining := listPruneIDs(t, service); len(remaining) != 2 {
		t.Fatalf("remaining = %v, want both", remaining)
	}
}

func TestPruneCutoffMatchesAllBeforeFuture(t *testing.T) {
	service := newPruneService(t)
	createPruneSession(t, service, "empty", "")
	createPruneSession(t, service, "filled", "hello")
	// A cutoff in the future is after every just-created session's UpdatedAt,
	// so the age filter alone matches the whole store.
	report := service.Prune(t.Context(), PruneOptions{Cutoff: time.Now().Add(24 * time.Hour)})
	if report.Matched != 2 || report.Deleted != 2 {
		t.Fatalf("matched=%d deleted=%d, want 2/2", report.Matched, report.Deleted)
	}
	if remaining := listPruneIDs(t, service); len(remaining) != 0 {
		t.Fatalf("remaining = %v, want none", remaining)
	}
}

func TestPruneCombinesEmptyAndCutoff(t *testing.T) {
	service := newPruneService(t)
	createPruneSession(t, service, "old-empty", "")
	createPruneSession(t, service, "new-empty", "")
	createPruneSession(t, service, "old-filled", "hello")
	// Cutoff far in the future: every session is older than it, so only the
	// empty/filled distinction remains.
	report := service.Prune(t.Context(), PruneOptions{EmptyOnly: true, Cutoff: time.Now().Add(24 * time.Hour)})
	if report.Matched != 2 || report.Deleted != 2 {
		t.Fatalf("matched=%d deleted=%d, want 2/2", report.Matched, report.Deleted)
	}
	remaining := listPruneIDs(t, service)
	if len(remaining) != 1 || remaining[0] != "old-filled" {
		t.Fatalf("remaining = %v, want [old-filled]", remaining)
	}
}

func TestPruneSkipsBoundRuntime(t *testing.T) {
	service := newPruneService(t)
	createPruneSession(t, service, "idle", "")
	// Prepare and keep a published runtime bound by a client (Open increments
	// the runtime's binding count). Deleting it must be refused as busy, not
	// torn down underneath the client.
	prepared, err := service.PrepareCreate(t.Context(), CreateOptions{SessionID: "bound"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Runtime().Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	published, err := service.Publish(prepared)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := service.Open(t.Context(), published.Runtime().Ref())
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Release(t.Context())

	report := service.Prune(t.Context(), PruneOptions{})
	if report.Deleted != 1 || report.Busy != 1 {
		t.Fatalf("deleted=%d busy=%d, want 1/1", report.Deleted, report.Busy)
	}
	if remaining := listPruneIDs(t, service); len(remaining) != 1 || remaining[0] != "bound" {
		t.Fatalf("remaining = %v, want [bound]", remaining)
	}
}

func listPruneIDs(t *testing.T, service *Service) []string {
	t.Helper()
	page, err := service.Query().List(t.Context(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(page.Sessions))
	for _, info := range page.Sessions {
		ids = append(ids, info.SessionID)
	}
	return ids
}

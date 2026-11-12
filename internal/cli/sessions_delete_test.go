package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupSessionDeleteStore(t *testing.T) (sessionDir, v4root string) {
	t.Helper()
	root := t.TempDir()
	sessionDir = filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	v4root = filepath.Join(root, "sessions-v4")
	createCanonicalTestSession(t, v4root, "empty-old", "")
	createCanonicalTestSession(t, v4root, "empty-new", "")
	createCanonicalTestSession(t, v4root, "filled", "hello")
	return sessionDir, v4root
}

func sessionIDsInRoot(t *testing.T, v4root string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(v4root)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == ".query-cache" || name == ".recovery-cache" || name == ".trash" {
			continue
		}
		ids[name] = true
	}
	return ids
}

func runSessionsDelete(t *testing.T, args ...string) (sessionDeleteReport, int) {
	t.Helper()
	var code int
	out := captureStdout(t, func() {
		code = sessionsDeleteCommand(args)
	})
	var report sessionDeleteReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode delete report from %q: %v", out, err)
	}
	return report, code
}

func TestSessionsDeleteRequiresFilter(t *testing.T) {
	sessionDir, _ := setupSessionDeleteStore(t)
	if code := sessionsDeleteCommand([]string{"--dir", sessionDir}); code != 2 {
		t.Fatalf("no filter exit code = %d, want 2", code)
	}
	if code := sessionsDeleteCommand([]string{"--dir", sessionDir, "--older-than", "not-a-duration"}); code != 2 {
		t.Fatalf("invalid duration exit code = %d, want 2", code)
	}
	// A day expansion that would overflow the duration arithmetic must be
	// rejected rather than silently producing a far-future cutoff.
	if code := sessionsDeleteCommand([]string{"--dir", sessionDir, "--older-than", "99999999999d"}); code != 2 {
		t.Fatalf("overflowing day duration exit code = %d, want 2", code)
	}
}

func TestSessionsDeleteDryRunKeepsSessions(t *testing.T) {
	sessionDir, v4root := setupSessionDeleteStore(t)
	report, code := runSessionsDelete(t, "--dir", sessionDir, "--empty", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if !report.DryRun {
		t.Fatalf("dry run not reported")
	}
	// In a dry run Deleted reports the sessions that would be deleted, matching
	// the text report's "would delete: N" summary.
	if report.Matched != 2 || report.Deleted != 2 {
		t.Fatalf("matched=%d deleted=%d, want 2/2", report.Matched, report.Deleted)
	}
	if got := sessionIDsInRoot(t, v4root); len(got) != 3 {
		t.Fatalf("dry run removed sessions: %v", got)
	}
}

func TestSessionsDeleteEmptyApplies(t *testing.T) {
	sessionDir, v4root := setupSessionDeleteStore(t)
	report, code := runSessionsDelete(t, "--dir", sessionDir, "--empty", "--apply", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if report.DryRun {
		t.Fatalf("apply run reported as dry run")
	}
	if report.Matched != 2 || report.Deleted != 2 {
		t.Fatalf("matched=%d deleted=%d, want 2/2", report.Matched, report.Deleted)
	}
	got := sessionIDsInRoot(t, v4root)
	if len(got) != 1 || !got["filled"] {
		t.Fatalf("remaining sessions = %v, want only filled", got)
	}
}

func TestSessionsDeleteV4DirDirect(t *testing.T) {
	sessionDir, v4root := setupSessionDeleteStore(t)
	report, code := runSessionsDelete(t, "--dir", v4root, "--empty", "--apply", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if report.Deleted != 2 {
		t.Fatalf("deleted = %d, want 2", report.Deleted)
	}
	if got := sessionIDsInRoot(t, v4root); len(got) != 1 || !got["filled"] {
		t.Fatalf("remaining sessions = %v, want only filled", got)
	}
	_ = sessionDir
}

func TestSessionsDeleteOlderThanApplies(t *testing.T) {
	sessionDir, v4root := setupSessionDeleteStore(t)
	// Fresh sessions are not idle for an hour, so the age filter matches
	// nothing and never mistakes a just-created (or cold-metadata) session
	// for an ancient one.
	report, code := runSessionsDelete(t, "--dir", sessionDir, "--older-than", "1h", "--apply", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if report.Deleted != 0 {
		t.Fatalf("deleted = %d, want 0 (fresh sessions are not an hour old)", report.Deleted)
	}
	if got := sessionIDsInRoot(t, v4root); len(got) != 3 {
		t.Fatalf("remaining sessions = %v, want all three", got)
	}
}

func TestSessionsDeleteEmptyKeepsFilledOnColdMetadata(t *testing.T) {
	// The store is created without waiting for catalog metadata rebuilds, so a
	// naive Turns==0 test would classify the filled session as empty. The
	// RefreshMetadata step must keep it.
	sessionDir, v4root := setupSessionDeleteStore(t)
	report, code := runSessionsDelete(t, "--dir", sessionDir, "--empty", "--apply", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if report.Matched != 2 {
		t.Fatalf("matched = %d, want 2 (filled must not be matched)", report.Matched)
	}
	if got := sessionIDsInRoot(t, v4root); len(got) != 1 || !got["filled"] {
		t.Fatalf("remaining sessions = %v, want only filled", got)
	}
}

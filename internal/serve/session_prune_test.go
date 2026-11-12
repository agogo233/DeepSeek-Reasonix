package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/session"
)

func createServeCanonicalSession(t *testing.T, service *session.Service, id, prompt string) {
	t.Helper()
	prepared, err := service.PrepareCreate(t.Context(), session.CreateOptions{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	sess := prepared.Runtime().Session()
	if prompt != "" {
		if _, err := sess.AppendBatch(t.Context(), "seed", []session.Event{
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

func newPruneServeFixture(t *testing.T) (*Server, *session.Service, string, string) {
	t.Helper()
	legacyDir := t.TempDir()
	v4root := filepath.Join(t.TempDir(), "sessions-v4")
	persistence := session.NewFilesystemPersistence(v4root)
	service, err := session.NewService("serve-prune-test", persistence)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	exec := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: exec, SessionDir: legacyDir, SessionService: service, ExclusiveSession: true})
	t.Cleanup(ctrl.Close)
	ref, err := ctrl.BindFreshSession(t.Context(), "foreground")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a browser client holding the current session so a bulk prune
	// must skip it as busy rather than tear it down. The release cleanup is
	// registered after newLifecycleTestServer so it runs (LIFO) first.
	binding, err := service.Open(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	srv := newLifecycleTestServer(t, ctrl, NewBroadcaster(), config.ServeConfig{})
	t.Cleanup(func() { _ = binding.Release(context.Background()) })
	return srv, service, legacyDir, v4root
}

func writeLegacyTranscript(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func postDeleteSessions(t *testing.T, srv *Server, mode string, dryRun bool) session.PruneReport {
	t.Helper()
	body, err := json.Marshal(map[string]any{"mode": mode, "dryRun": dryRun})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	srv.deleteSessions(w, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/delete-sessions", bytes.NewReader(body)))
	var report session.PruneReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode report from %q: %v", w.Body.String(), err)
	}
	return report
}

func canonicalDirExists(v4root, id string) bool {
	_, err := os.Stat(filepath.Join(v4root, id))
	return err == nil
}

func TestDeleteSessionsEmptyPrunesTurnlessOnly(t *testing.T) {
	srv, service, legacyDir, v4root := newPruneServeFixture(t)
	createServeCanonicalSession(t, service, "c-empty", "")
	createServeCanonicalSession(t, service, "c-filled", "hello")
	writeLegacyTranscript(t, legacyDir, "legacy-empty.jsonl", "")
	writeLegacyTranscript(t, legacyDir, "legacy-filled.jsonl", `{"role":"user","content":"hi"}`+"\n")

	report := postDeleteSessions(t, srv, "empty", false)
	if report.Deleted != 2 || report.Busy != 1 {
		t.Fatalf("deleted=%d busy=%d, want 2/1", report.Deleted, report.Busy)
	}
	if canonicalDirExists(v4root, "c-empty") {
		t.Fatal("empty canonical session survived prune")
	}
	if !canonicalDirExists(v4root, "c-filled") {
		t.Fatal("filled canonical session was pruned")
	}
	if !canonicalDirExists(v4root, "foreground") {
		t.Fatal("bound current session was pruned")
	}
	if _, err := os.Stat(filepath.Join(legacyDir, "legacy-empty.jsonl")); !os.IsNotExist(err) {
		t.Fatal("empty legacy transcript survived prune")
	}
	if _, err := os.Stat(filepath.Join(legacyDir, "legacy-filled.jsonl")); err != nil {
		t.Fatal("filled legacy transcript was pruned")
	}
}

func TestDeleteSessionsAllClearsEverythingButBound(t *testing.T) {
	srv, service, legacyDir, v4root := newPruneServeFixture(t)
	createServeCanonicalSession(t, service, "c-empty", "")
	createServeCanonicalSession(t, service, "c-filled", "hello")
	writeLegacyTranscript(t, legacyDir, "legacy-empty.jsonl", "")
	writeLegacyTranscript(t, legacyDir, "legacy-filled.jsonl", `{"role":"user","content":"hi"}`+"\n")

	report := postDeleteSessions(t, srv, "all", false)
	if report.Deleted != 4 || report.Busy != 1 {
		t.Fatalf("deleted=%d busy=%d, want 4/1", report.Deleted, report.Busy)
	}
	if canonicalDirExists(v4root, "c-empty") || canonicalDirExists(v4root, "c-filled") {
		t.Fatal("canonical sessions survived all-prune")
	}
	if !canonicalDirExists(v4root, "foreground") {
		t.Fatal("bound current session was pruned")
	}
	if _, err := os.Stat(filepath.Join(legacyDir, "legacy-empty.jsonl")); !os.IsNotExist(err) {
		t.Fatal("empty legacy transcript survived all-prune")
	}
	if _, err := os.Stat(filepath.Join(legacyDir, "legacy-filled.jsonl")); !os.IsNotExist(err) {
		t.Fatal("filled legacy transcript survived all-prune")
	}
}

func TestDeleteSessionsDryRunPreviewsWithoutRemoving(t *testing.T) {
	srv, service, legacyDir, v4root := newPruneServeFixture(t)
	createServeCanonicalSession(t, service, "c-empty", "")
	createServeCanonicalSession(t, service, "c-filled", "hello")
	writeLegacyTranscript(t, legacyDir, "legacy-empty.jsonl", "")
	writeLegacyTranscript(t, legacyDir, "legacy-filled.jsonl", `{"role":"user","content":"hi"}`+"\n")

	report := postDeleteSessions(t, srv, "empty", true)
	if !report.DryRun {
		t.Fatal("dry run not reported")
	}
	// Turn-less sessions only: c-empty, legacy-empty, plus the bound empty
	// foreground that a real run would skip as busy.
	if report.Matched != 3 || report.Deleted != 3 {
		t.Fatalf("matched=%d deleted=%d, want 3/3", report.Matched, report.Deleted)
	}
	if !canonicalDirExists(v4root, "c-empty") || !canonicalDirExists(v4root, "c-filled") {
		t.Fatal("dry run removed canonical sessions")
	}
	if _, err := os.Stat(filepath.Join(legacyDir, "legacy-empty.jsonl")); err != nil {
		t.Fatal("dry run removed legacy transcripts")
	}
}

func TestDeleteSessionsInvalidModeRejected(t *testing.T) {
	srv, _, _, _ := newPruneServeFixture(t)
	body, err := json.Marshal(map[string]string{"mode": "nonsense"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	srv.deleteSessions(w, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/delete-sessions", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode code = %d, want 400", w.Code)
	}
}

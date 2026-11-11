package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/base/testenv"
	"reasonix/internal/state/sessionstore"
	"reasonix/internal/state/store"
)

func postSweepPreview(t *testing.T, srv *httptest.Server, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(srv.URL+"/tree/sessions/sweep-preview", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

type sweepPreviewResponse struct {
	Matched   int `json:"matched"`
	Pinned    int `json:"pinned"`
	Running   int `json:"running"`
	Recovered int `json:"recovered"`
	Rows      []struct {
		Path       string    `json:"path"`
		Title      string    `json:"title"`
		LastActive time.Time `json:"lastActive"`
		Archived   bool      `json:"archived"`
		Turns      int       `json:"turns"`
	} `json:"rows"`
}

func sweepHub(t *testing.T) (*Hub, *httptest.Server, string) {
	t.Helper()
	t.Setenv("REASONIX_HOME", testenv.TempDir(t))
	root := testenv.TempDir(t)
	h := NewHub(HubOptions{})
	hubRuntime(t, h, root)
	srv := httptest.NewServer(operatorHandler(h))
	t.Cleanup(srv.Close)
	return h, srv, root
}

// writeEmptySessionAt writes a transcript that holds no user message at all.
func writeEmptySessionAt(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	s := sessionstore.NewSession("sys")
	if err := s.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot empty: %v", err)
	}
}

// ageSession sets a session's content (transcript and event log) into the past,
// which is what the idle filter and the empty-grace period read. The listing
// sidecar is left alone: dropping it would make the empty filter distrust the
// zero count.
func ageSession(t *testing.T, path string, days int) {
	t.Helper()
	old := time.Now().AddDate(0, 0, -days)
	for _, p := range []string{path, store.SessionEventLog(path)} {
		if p == "" {
			continue
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("Chtimes %s: %v", p, err)
		}
	}
}

func TestSweepPreviewsOlderThan(t *testing.T) {
	_, srv, root := sweepHub(t)
	old := filepath.Join(SessionDirFor(root), "old.jsonl")
	new := filepath.Join(SessionDirFor(root), "new.jsonl")
	writeSessionAt(t, old)
	writeSessionAt(t, new)
	// Drop the sidecar so the listing falls back to content mtimes, which the
	// idle filter can then see as old.
	if err := os.Remove(sessionstore.BranchMetaPath(old)); err != nil {
		t.Fatalf("remove old sidecar: %v", err)
	}
	ageSession(t, old, 10)

	resp := postSweepPreview(t, srv, map[string]any{"olderThanDays": 7})
	defer resp.Body.Close()
	var out sweepPreviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sweep = %d, %v", resp.StatusCode, err)
	}
	if out.Matched != 1 || len(out.Rows) != 1 || filepath.Base(out.Rows[0].Path) != "old.jsonl" {
		t.Fatalf("older-than matched %d rows %v, want only old.jsonl", out.Matched, out.Rows)
	}
}

func TestSweepPreviewsEmptyOnly(t *testing.T) {
	_, srv, root := sweepHub(t)
	empty := filepath.Join(SessionDirFor(root), "empty.jsonl")
	full := filepath.Join(SessionDirFor(root), "full.jsonl")
	writeEmptySessionAt(t, empty)
	writeSessionAt(t, full)
	ageSession(t, empty, 10)
	ageSession(t, full, 10)

	resp := postSweepPreview(t, srv, map[string]any{"empty": true})
	defer resp.Body.Close()
	var out sweepPreviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sweep = %d, %v", resp.StatusCode, err)
	}
	if out.Matched != 1 || len(out.Rows) != 1 || filepath.Base(out.Rows[0].Path) != "empty.jsonl" {
		t.Fatalf("empty matched %d rows %v, want only empty.jsonl", out.Matched, out.Rows)
	}
}

func TestSweepAllExcludesPinned(t *testing.T) {
	_, srv, root := sweepHub(t)
	a := filepath.Join(SessionDirFor(root), "a.jsonl")
	b := filepath.Join(SessionDirFor(root), "b.jsonl")
	writeSessionAt(t, a)
	writeSessionAt(t, b)
	if err := sessionstore.SetSessionPinned(a, true); err != nil {
		t.Fatalf("SetSessionPinned: %v", err)
	}

	resp := postSweepPreview(t, srv, map[string]any{"all": true})
	defer resp.Body.Close()
	var out sweepPreviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sweep = %d, %v", resp.StatusCode, err)
	}
	if out.Matched != 1 || out.Pinned != 1 || len(out.Rows) != 1 || filepath.Base(out.Rows[0].Path) != "b.jsonl" {
		t.Fatalf("all matched %d, pinned %d, rows %v; want only b.jsonl with a held back", out.Matched, out.Pinned, out.Rows)
	}
}

func TestSweepRefusesWithoutAFilter(t *testing.T) {
	_, srv, _ := sweepHub(t)
	resp := postSweepPreview(t, srv, map[string]any{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("sweep without a filter = %d, want 400", resp.StatusCode)
	}
}

func TestSweepRefusesAnOutOfRangeAge(t *testing.T) {
	_, srv, _ := sweepHub(t)
	resp := postSweepPreview(t, srv, map[string]any{"olderThanDays": 99999})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("sweep with an out-of-range age = %d, want 400", resp.StatusCode)
	}
}

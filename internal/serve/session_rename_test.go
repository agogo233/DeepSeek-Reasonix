package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Renaming is a canonical-catalog mutation: the title is written into the
// session's event log, so it must come back out of /sessions normalized, and
// legacy/unknown identities must be refused rather than silently accepted.
func TestRenameSessionSetsCatalogTitle(t *testing.T) {
	srv, service, _, _ := newPruneServeFixture(t)
	createServeCanonicalSession(t, service, "rename-target", "hello world")

	post := func(body string) int {
		w := httptest.NewRecorder()
		srv.renameSession(w, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/rename-session", strings.NewReader(body)))
		return w.Code
	}

	if got := post(`{"sessionId":"rename-target","title":"  My   Renamed\n Session  "}`); got != http.StatusNoContent {
		t.Fatalf("rename status = %d, want 204", got)
	}

	w := httptest.NewRecorder()
	srv.sessions(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/sessions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("sessions status = %d, want 200", w.Code)
	}
	var rows []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	title := ""
	found := false
	for _, row := range rows {
		if row["sessionId"] == "rename-target" {
			found = true
			title, _ = row["title"].(string)
		}
	}
	if !found {
		t.Fatalf("renamed session missing from /sessions: %s", w.Body.String())
	}
	if title != "My Renamed Session" {
		t.Fatalf("renamed title = %q, want %q", title, "My Renamed Session")
	}

	if got := post(`{"sessionId":"rename-target"}`); got != http.StatusBadRequest {
		t.Fatalf("missing title status = %d, want 400", got)
	}
	if got := post(`{"title":"no session"}`); got != http.StatusBadRequest {
		t.Fatalf("missing sessionId status = %d, want 400", got)
	}
	if got := post(`{"sessionId":"does-not-exist","title":"x"}`); got != http.StatusNotFound {
		t.Fatalf("unknown session status = %d, want 404", got)
	}
}

func sessionTitleFromList(t *testing.T, srv *Server, sessionID string) string {
	t.Helper()
	w := httptest.NewRecorder()
	srv.sessions(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/sessions", nil))
	var rows []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row["sessionId"] == sessionID {
			title, _ := row["title"].(string)
			return title
		}
	}
	t.Fatalf("session %q missing from /sessions", sessionID)
	return ""
}

// Titles render on one sidebar line, so the endpoint collapses whitespace and
// caps the length in runes (not bytes) for CJK titles.
func TestRenameSessionNormalizesAndTruncatesTitle(t *testing.T) {
	srv, service, _, _ := newPruneServeFixture(t)
	createServeCanonicalSession(t, service, "rename-long", "seed")

	long := strings.Repeat("字", 250)
	w := httptest.NewRecorder()
	body := `{"sessionId":"rename-long","title":"  ` + long + ` \n\t more  "}`
	srv.renameSession(w, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/rename-session", strings.NewReader(body)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("rename status = %d, want 204", w.Code)
	}
	got := sessionTitleFromList(t, srv, "rename-long")
	want := strings.Repeat("字", 200)
	if got != want {
		t.Fatalf("truncated title = %q (runes %d), want %d runes", got, len([]rune(got)), len([]rune(want)))
	}
}

// The route must stay behind the same middleware chain as the other session
// mutations: the launch-token mutation gate plus csrfGuard, which rejects
// non-JSON bodies before the handler runs. operatorHandler injects the bearer
// token the gate requires, exactly as the browser client does.
func TestRenameSessionRequiresJSONContentType(t *testing.T) {
	srv, service, _, _ := newPruneServeFixture(t)
	createServeCanonicalSession(t, service, "rename-chain", "seed")
	handler := operatorHandler(srv)

	call := func(contentType string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/rename-session", strings.NewReader(`{"sessionId":"rename-chain","title":"chain"}`))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		handler.ServeHTTP(w, req)
		return w.Code
	}

	if got := call("application/json"); got != http.StatusNoContent {
		t.Fatalf("json rename through chain status = %d, want 204", got)
	}
	if got := call("text/plain"); got != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain rename status = %d, want 415", got)
	}
	if got := call(""); got != http.StatusUnsupportedMediaType {
		t.Fatalf("missing content-type rename status = %d, want 415", got)
	}
}

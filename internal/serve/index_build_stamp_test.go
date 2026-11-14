package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

// The page is compiled into the binary and changes with every build, so it must
// never be cached: a stale page hides newly deployed UI from the operator. The
// build stamp lets a stale page be told apart from the current one at a glance.
func TestIndexCacheControlAndBuildStamp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("index status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("index Cache-Control = %q, want no-store", got)
	}
	html := string(body)
	if strings.Contains(html, "__BUILD__") {
		t.Fatal("index still contains the unreplaced __BUILD__ placeholder")
	}
	if !strings.Contains(html, `class="sidebar__build"`) {
		t.Fatal("index missing the sidebar build stamp element")
	}
	if !strings.Contains(html, "id=\"sidebar-build\"") {
		t.Fatal("index build stamp element missing its id hook")
	}
}

package serve

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

type workspaceRootOverride struct {
	control.SessionAPI
	root string
}

func (s *workspaceRootOverride) WorkspaceRoot() string { return s.root }

func newWorkspaceFilesTestServer(t *testing.T, root string, opts ...func(*config.ServeConfig)) *httptest.Server {
	t.Helper()
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	api := &workspaceRootOverride{SessionAPI: ctrl, root: root}
	cfg := config.ServeConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return httptest.NewServer(operatorHandler(New(api, bc, cfg)))
}

func workspaceGet(t *testing.T, srv *httptest.Server, path string) *http.Response {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func workspaceListEntries(t *testing.T, resp *http.Response) workspaceListResponse {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp.StatusCode)
	}
	var out workspaceListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

type workspaceListResponse struct {
	Root    string              `json:"root"`
	Path    string              `json:"path"`
	Entries []workspaceDirEntry `json:"entries"`
}

func TestWorkspaceFileListRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b.txt", "a10.txt", "a2.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "inner.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.local"), []byte("SECRET=2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}

	srv := newWorkspaceFilesTestServer(t, root)
	defer srv.Close()
	out := workspaceListEntries(t, workspaceGet(t, srv, "/workspace/files"))

	if out.Root != root || out.Path != "" {
		t.Fatalf("root/path = %q/%q", out.Root, out.Path)
	}
	var got []string
	for _, e := range out.Entries {
		got = append(got, e.Name)
	}
	want := []string{"sub", "a2.txt", "a10.txt", "b.txt"}
	if len(got) != len(want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entries = %v, want %v", got, want)
		}
	}
	if !out.Entries[0].IsDir {
		t.Fatal("directory entry must be marked isDir")
	}
	if out.Entries[0].Path != "sub" {
		t.Fatalf("dir path = %q, want sub", out.Entries[0].Path)
	}
}

func TestWorkspaceFileListSubdirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "inner.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newWorkspaceFilesTestServer(t, root)
	defer srv.Close()
	out := workspaceListEntries(t, workspaceGet(t, srv, "/workspace/files?path="+url.QueryEscape("sub")))
	if out.Path != "sub" || len(out.Entries) != 2 {
		t.Fatalf("path/entries = %q/%d, want sub/2", out.Path, len(out.Entries))
	}
	if out.Entries[0].Name != "deep" || !out.Entries[0].IsDir {
		t.Fatalf("first entry = %#v, want dir deep", out.Entries[0])
	}
}

func TestWorkspaceFileListRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	srv := newWorkspaceFilesTestServer(t, root)
	defer srv.Close()
	for _, rel := range []string{"../..", "..", "/etc", "../../etc/passwd", "sub/../../.."} {
		resp := workspaceGet(t, srv, "/workspace/files?path="+url.QueryEscape(rel))
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("traversal %q status = %d, want 404", rel, resp.StatusCode)
		}
	}
}

func TestWorkspaceFileListEmptyRoot(t *testing.T) {
	srv := newWorkspaceFilesTestServer(t, "")
	defer srv.Close()
	resp := workspaceGet(t, srv, "/workspace/files")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty root status = %d, want 400", resp.StatusCode)
	}
}

func TestWorkspaceFilePreviewText(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("hello workspace"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newWorkspaceFilesTestServer(t, root)
	defer srv.Close()
	resp := workspaceGet(t, srv, "/workspace/file/preview?path="+url.QueryEscape("note.md"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preview status = %d, want 200", resp.StatusCode)
	}
	var out workspaceTextPreview
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Kind != "text" || out.Body != "hello workspace" || out.Truncated {
		t.Fatalf("preview = %#v", out)
	}
}

func TestWorkspaceFilePreviewTextTruncates(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("a", workspaceTextPreviewLimit+10)
	if err := os.WriteFile(filepath.Join(root, "big.log"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newWorkspaceFilesTestServer(t, root)
	defer srv.Close()
	resp := workspaceGet(t, srv, "/workspace/file/preview?path=big.log")
	var out workspaceTextPreview
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.Truncated || len(out.Body) != workspaceTextPreviewLimit {
		t.Fatalf("truncated=%v len=%d, want true/%d", out.Truncated, len(out.Body), workspaceTextPreviewLimit)
	}
}

func TestWorkspaceFilePreviewBinary(t *testing.T) {
	root := t.TempDir()
	payload := append([]byte("PK\x03\x04"), make([]byte, workspaceBinaryProbeLen)...)
	if err := os.WriteFile(filepath.Join(root, "blob.bin"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newWorkspaceFilesTestServer(t, root)
	defer srv.Close()
	resp := workspaceGet(t, srv, "/workspace/file/preview?path=blob.bin")
	var out workspaceTextPreview
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Kind != "binary" {
		t.Fatalf("kind = %q, want binary", out.Kind)
	}
}

func TestWorkspaceFilePreviewMediaAndHTML(t *testing.T) {
	root := t.TempDir()
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	if err := os.WriteFile(filepath.Join(root, "pic.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "page.html"), []byte("<html><body>hi</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newWorkspaceFilesTestServer(t, root)
	defer srv.Close()

	resp := workspaceGet(t, srv, "/workspace/file/preview?path=pic.png")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("png status/type = %d/%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp = workspaceGet(t, srv, "/workspace/file/preview?path=page.html")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("html status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("html content type = %q", ct)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); csp != "sandbox" {
		t.Fatalf("html CSP = %q, want sandbox", csp)
	}
}

func TestWorkspaceFilePreviewSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	srv := newWorkspaceFilesTestServer(t, root)
	defer srv.Close()
	resp := workspaceGet(t, srv, "/workspace/file/preview?path=leak.txt")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("symlink escape preview status = %d, want 404", resp.StatusCode)
	}
	out := workspaceListEntries(t, workspaceGet(t, srv, "/workspace/files"))
	for _, e := range out.Entries {
		if e.Name == "leak.txt" {
			t.Fatal("escaping symlink must not be listed")
		}
	}
}

func TestWorkspaceFileDownload(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("body-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newWorkspaceFilesTestServer(t, root)
	defer srv.Close()
	resp := workspaceGet(t, srv, "/workspace/file/download?path=report.txt")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d, want 200", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "report.txt") {
		t.Fatalf("content disposition = %q", cd)
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "body-bytes" {
		t.Fatalf("downloaded body = %q", buf.String())
	}
}

func TestWorkspaceFolderDownloadZip(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "b.log"), []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "node_modules", "x.js"), []byte("skip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", ".env"), []byte("SECRET=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newWorkspaceFilesTestServer(t, root)
	defer srv.Close()
	resp := workspaceGet(t, srv, "/workspace/files/download?path=pkg")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("zip status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("zip content type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "pkg.zip") {
		t.Fatalf("zip disposition = %q", cd)
	}
	body := new(bytes.Buffer)
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(body.Bytes()), int64(body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if len(names) != 2 || names[0] != "a.txt" || names[1] != "b.log" {
		t.Fatalf("zip entries = %v, want [a.txt b.log]", names)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	content := new(bytes.Buffer)
	if _, err := content.ReadFrom(rc); err != nil {
		t.Fatal(err)
	}
	if content.String() != "alpha" {
		t.Fatalf("zip entry content = %q", content.String())
	}
}

func TestWorkspaceFileOpenBlocksFilteredPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("gitconfig"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "x.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newWorkspaceFilesTestServer(t, root)
	defer srv.Close()
	for _, path := range []string{
		"/workspace/file/preview?path=.env",
		"/workspace/file/download?path=.env",
		"/workspace/file/preview?path=.git%2Fconfig",
		"/workspace/file/download?path=node_modules%2Fx.js",
		"/workspace/files/download?path=.git",
		"/workspace/files/download?path=node_modules",
	} {
		resp := workspaceGet(t, srv, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestWorkspaceFileEndpointsRequireAuth(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	api := &workspaceRootOverride{SessionAPI: ctrl, root: root}
	srv := httptest.NewServer(New(api, bc, config.ServeConfig{AuthMode: "token", Token: "secret-token"}).Handler())
	defer srv.Close()
	for _, path := range []string{
		"/workspace/files",
		"/workspace/file/preview?path=a.txt",
		"/workspace/file/download?path=a.txt",
		"/workspace/files/download?path=.",
	} {
		resp := workspaceGet(t, srv, path)
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotModified {
			t.Fatalf("%s without token status = %d, want rejected", path, resp.StatusCode)
		}
	}
}

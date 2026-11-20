package serve

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"reasonix/internal/fileref"
	"reasonix/internal/fileutil"
)

const (
	workspaceTextPreviewLimit = 2 << 20
	workspaceZipTotalLimit    = 512 << 20
	workspaceZipMaxFiles      = 50000
	workspaceBinaryProbeLen   = 8192
)

func (s *Server) registerWorkspaceFileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /workspace/files", s.workspaceFileList)
	mux.HandleFunc("GET /workspace/file/preview", s.workspaceFilePreview)
	mux.HandleFunc("GET /workspace/file/download", s.workspaceFileDownload)
	mux.HandleFunc("GET /workspace/files/download", s.workspaceFolderDownload)
}

type workspaceDirEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size,omitempty"`
	ModTime int64  `json:"mtime,omitempty"`
}

type workspaceTextPreview struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Body      string `json:"body,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

func (s *Server) workspaceBrowseRoot() (string, bool) {
	root := strings.TrimSpace(s.ctl().WorkspaceRoot())
	if root == "" {
		return "", false
	}
	return root, true
}

func workspaceRequestRel(r *http.Request) string {
	return strings.Trim(strings.TrimSpace(r.URL.Query().Get("path")), "/")
}

func workspaceSensitiveFileName(name string) bool {
	lower := strings.ToLower(name)
	return lower == ".env" || strings.HasPrefix(lower, ".env.")
}

func workspaceSkipEntry(name string, isDir bool) bool {
	return fileref.SkipBrowseEntry(name, isDir) || (!isDir && workspaceSensitiveFileName(name))
}

func workspacePathFiltered(rel string) bool {
	segs := strings.Split(filepath.ToSlash(rel), "/")
	for i, seg := range segs {
		if workspaceSkipEntry(seg, i < len(segs)-1) {
			return true
		}
	}
	return false
}

func (s *Server) workspaceFileList(w http.ResponseWriter, r *http.Request) {
	root, ok := s.workspaceBrowseRoot()
	if !ok {
		http.Error(w, "no active workspace", http.StatusBadRequest)
		return
	}
	rel := workspaceRequestRel(r)
	entries, err := listWorkspaceDirEntries(root, rel)
	if err != nil {
		http.Error(w, "directory unavailable", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"root": root, "path": rel, "entries": entries})
}

func listWorkspaceDirEntries(root, rel string) ([]workspaceDirEntry, error) {
	var dirents []fs.DirEntry
	var err error
	if rel == "" || rel == "." {
		dirents, err = os.ReadDir(root)
	} else {
		var f *os.File
		f, err = fileutil.OpenFileBeneath(root, rel)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		var info os.FileInfo
		info, err = f.Stat()
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.New("not a directory")
		}
		dirents, err = f.ReadDir(-1)
	}
	if err != nil {
		return nil, err
	}
	dirs, files := []workspaceDirEntry{}, []workspaceDirEntry{}
	for _, d := range dirents {
		name := d.Name()
		info, err := d.Info()
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := fileutil.OpenFileBeneath(root, path.Join(rel, name))
			if err != nil {
				continue
			}
			targetInfo, err := resolved.Stat()
			_ = resolved.Close()
			if err != nil {
				continue
			}
			info = targetInfo
		}
		isDir := info.IsDir()
		if workspaceSkipEntry(name, isDir) {
			continue
		}
		entry := workspaceDirEntry{
			Name:    name,
			Path:    path.Join(rel, name),
			IsDir:   isDir,
			Size:    info.Size(),
			ModTime: info.ModTime().UnixMilli(),
		}
		if isDir {
			dirs = append(dirs, entry)
		} else if info.Mode().IsRegular() {
			files = append(files, entry)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return fileref.NaturalLess(dirs[i].Name, dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return fileref.NaturalLess(files[i].Name, files[j].Name) })
	return append(dirs, files...), nil
}

func (s *Server) workspaceOpenFileFromRequest(w http.ResponseWriter, r *http.Request) (*os.File, os.FileInfo, string, bool) {
	root, ok := s.workspaceBrowseRoot()
	if !ok {
		http.Error(w, "no active workspace", http.StatusBadRequest)
		return nil, nil, "", false
	}
	rel := workspaceRequestRel(r)
	if rel == "" || rel == "." {
		http.Error(w, "file path required", http.StatusNotFound)
		return nil, nil, "", false
	}
	if workspacePathFiltered(rel) {
		http.Error(w, "file not found", http.StatusNotFound)
		return nil, nil, "", false
	}
	f, err := fileutil.OpenFileBeneath(root, rel)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return nil, nil, "", false
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		http.Error(w, "not a regular file", http.StatusBadRequest)
		return nil, nil, "", false
	}
	return f, info, rel, true
}

func workspaceMediaKind(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".htm":
		return "html"
	case ".svg":
		return "svg"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".bmp", ".avif":
		return "image"
	case ".mp3", ".wav", ".ogg", ".flac", ".m4a", ".aac", ".opus":
		return "audio"
	case ".mp4", ".webm", ".mov", ".m4v":
		return "video"
	case ".pdf":
		return "pdf"
	default:
		return ""
	}
}

func (s *Server) workspaceFilePreview(w http.ResponseWriter, r *http.Request) {
	f, info, rel, ok := s.workspaceOpenFileFromRequest(w, r)
	if !ok {
		return
	}
	defer f.Close()
	name := path.Base(rel)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	kind := workspaceMediaKind(name)
	if kind != "" {
		w.Header().Set("Content-Encoding", "identity")
		w.Header().Set("Content-Security-Policy", "sandbox")
	}
	switch kind {
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, name, info.ModTime(), f)
	case "svg":
		w.Header().Set("Content-Type", "image/svg+xml")
		http.ServeContent(w, r, name, info.ModTime(), f)
	case "image", "audio", "video", "pdf":
		http.ServeContent(w, r, name, info.ModTime(), f)
	default:
		data, err := io.ReadAll(io.LimitReader(f, workspaceTextPreviewLimit+1))
		if err != nil {
			http.Error(w, "read failed", http.StatusInternalServerError)
			return
		}
		truncated := int64(len(data)) > workspaceTextPreviewLimit
		if truncated {
			data = data[:workspaceTextPreviewLimit]
		}
		probe := data
		if len(probe) > workspaceBinaryProbeLen {
			probe = probe[:workspaceBinaryProbeLen]
		}
		if bytes.IndexByte(probe, 0) >= 0 {
			writeJSON(w, workspaceTextPreview{Kind: "binary", Name: name, Size: info.Size()})
			return
		}
		writeJSON(w, workspaceTextPreview{Kind: "text", Name: name, Body: string(data), Truncated: truncated, Size: info.Size()})
	}
}

func (s *Server) workspaceFileDownload(w http.ResponseWriter, r *http.Request) {
	f, info, rel, ok := s.workspaceOpenFileFromRequest(w, r)
	if !ok {
		return
	}
	defer f.Close()
	name := path.Base(rel)
	w.Header().Set("Content-Encoding", "identity")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeContent(w, r, name, info.ModTime(), f)
}

func (s *Server) workspaceFolderDownload(w http.ResponseWriter, r *http.Request) {
	root, ok := s.workspaceBrowseRoot()
	if !ok {
		http.Error(w, "no active workspace", http.StatusBadRequest)
		return
	}
	rel := workspaceRequestRel(r)
	absDir := root
	zipName := filepath.Base(root) + ".zip"
	if rel != "" && rel != "." {
		if workspacePathFiltered(rel) {
			http.Error(w, "directory not found", http.StatusNotFound)
			return
		}
		f, err := fileutil.OpenFileBeneath(root, rel)
		if err != nil {
			http.Error(w, "directory not found", http.StatusNotFound)
			return
		}
		info, err := f.Stat()
		_ = f.Close()
		if err != nil || !info.IsDir() {
			http.Error(w, "not a directory", http.StatusBadRequest)
			return
		}
		absDir = filepath.Join(root, filepath.FromSlash(rel))
		zipName = path.Base(rel) + ".zip"
	}
	total, count, err := workspaceZipScan(absDir)
	if err != nil {
		http.Error(w, "folder unavailable", http.StatusNotFound)
		return
	}
	if total > workspaceZipTotalLimit || count > workspaceZipMaxFiles {
		http.Error(w, "folder too large to pack", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Encoding", "identity")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": zipName}))
	zw := zip.NewWriter(w)
	if err := workspaceZipWrite(zw, root, absDir); err != nil {
		return
	}
	_ = zw.Close()
}

func workspaceZipScan(absDir string) (int64, int, error) {
	var total int64
	count := 0
	err := filepath.WalkDir(absDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != absDir && workspaceSkipEntry(d.Name(), true) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || workspaceSkipEntry(d.Name(), false) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		count++
		return nil
	})
	return total, count, err
}

func workspaceZipWrite(zw *zip.Writer, root, absDir string) error {
	return filepath.WalkDir(absDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != absDir && workspaceSkipEntry(d.Name(), true) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || workspaceSkipEntry(d.Name(), false) {
			return nil
		}
		fileRel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		f, err := fileutil.OpenFileBeneath(root, fileRel)
		if err != nil {
			return nil
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return nil
		}
		entryRel, err := filepath.Rel(absDir, p)
		if err != nil {
			return nil
		}
		hdr := &zip.Header{
			Name:     filepath.ToSlash(entryRel),
			Method:   zip.Deflate,
			Modified: info.ModTime(),
		}
		out, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, f)
		return err
	})
}

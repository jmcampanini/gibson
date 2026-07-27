package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

const indexPath = "index.html"

type spaHandler struct {
	static       fs.FS
	index        []byte
	indexModTime time.Time
}

func newSPAHandler(static fs.FS) (http.Handler, error) {
	info, err := fs.Stat(static, indexPath)
	if err != nil {
		return nil, fmt.Errorf("static assets are not ready: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("static assets are not ready: %s is not a file", indexPath)
	}

	index, err := fs.ReadFile(static, indexPath)
	if err != nil {
		return nil, fmt.Errorf("static assets are not ready: read %s: %w", indexPath, err)
	}

	return &spaHandler{static: static, index: index, indexModTime: info.ModTime()}, nil
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name != "" && fs.ValidPath(name) {
		info, err := fs.Stat(h.static, name)
		switch {
		case err == nil && info.Mode().IsRegular():
			h.serveFile(w, r, name, info.ModTime())
			return
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	}

	h.serveIndex(w, r)
}

func (h *spaHandler) serveFile(w http.ResponseWriter, r *http.Request, name string, modTime time.Time) {
	if name == indexPath {
		h.serveIndex(w, r)
		return
	}

	contents, err := fs.ReadFile(h.static, name)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, name, modTime, bytes.NewReader(contents))
}

func (h *spaHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	http.ServeContent(w, r, indexPath, h.indexModTime, bytes.NewReader(h.index))
}

package httpapi

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
)

type Options struct {
	Version  string
	StaticFS fs.FS
	DevProxy *url.URL
}

type server struct {
	version  string
	frontend http.Handler
}

type healthResponse struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func New(options Options) (http.Handler, error) {
	if options.StaticFS != nil && options.DevProxy != nil {
		return nil, fmt.Errorf("static filesystem and development proxy are mutually exclusive")
	}

	var frontend http.Handler
	if options.DevProxy != nil {
		frontend = newDevProxy(options.DevProxy)
	} else {
		if options.StaticFS == nil {
			return nil, fmt.Errorf("static filesystem or development proxy is required")
		}

		spa, err := newSPAHandler(options.StaticFS)
		if err != nil {
			return nil, err
		}
		frontend = spa
	}

	return &server{version: options.Version, frontend: frontend}, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/health" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, healthResponse{OK: true, Version: s.version})
		return
	}

	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "not_found", "API route not found")
		return
	}

	s.frontend.ServeHTTP(w, r)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

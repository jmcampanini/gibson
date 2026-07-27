package httpapi

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

type Options struct {
	Version  string
	StaticFS fs.FS
}

type server struct {
	version string
	spa     http.Handler
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
	if options.StaticFS == nil {
		return nil, fmt.Errorf("static filesystem is required")
	}

	spa, err := newSPAHandler(options.StaticFS)
	if err != nil {
		return nil, err
	}

	return &server{version: options.Version, spa: spa}, nil
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

	s.spa.ServeHTTP(w, r)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

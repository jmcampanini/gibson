package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testStaticFS = fstest.MapFS{
	"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><main id=app></main>")},
	"assets/app.js": &fstest.MapFile{Data: []byte("globalThis.gibson = true")},
}

func TestNewRequiresReadyIndex(t *testing.T) {
	t.Parallel()

	tests := map[string]fs.FS{
		"nil filesystem": nil,
		"missing index": fstest.MapFS{
			"assets/app.js": &fstest.MapFile{Data: []byte("asset")},
		},
		"index is directory": fstest.MapFS{
			"index.html": &fstest.MapFile{Mode: fs.ModeDir},
		},
	}

	for name, static := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler, err := New(Options{StaticFS: static})
			require.Error(t, err)
			assert.Nil(t, handler)
		})
	}
}

func TestHealth(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	response := request(handler, http.MethodGet, "/api/health")

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))

	var body healthResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, healthResponse{OK: true, Version: "test-version"}, body)
}

func TestUnknownAPIReturnsJSONNotFound(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api"},
		{method: http.MethodGet, path: "/api/unknown"},
		{method: http.MethodHead, path: "/api/health"},
		{method: http.MethodPost, path: "/api/health"},
		{method: http.MethodDelete, path: "/api/unknown"},
	}

	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			t.Parallel()

			response := request(handler, test.method, test.path)
			require.Equal(t, http.StatusNotFound, response.Code)
			assert.Equal(t, "application/json", response.Header().Get("Content-Type"))

			var body errorEnvelope
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, "not_found", body.Error.Code)
			assert.NotEmpty(t, body.Error.Message)
		})
	}
}

func TestServesSPAAndStaticAssets(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	tests := []struct {
		name        string
		path        string
		wantBody    string
		contentType string
	}{
		{name: "root shell", path: "/", wantBody: "<!doctype html><main id=app></main>", contentType: "text/html; charset=utf-8"},
		{name: "built asset", path: "/assets/app.js", wantBody: "globalThis.gibson = true", contentType: "text/javascript; charset=utf-8"},
		{name: "deep route", path: "/sessions/abc", wantBody: "<!doctype html><main id=app></main>", contentType: "text/html; charset=utf-8"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			response := request(handler, http.MethodGet, test.path)
			require.Equal(t, http.StatusOK, response.Code)
			assert.Equal(t, test.contentType, response.Header().Get("Content-Type"))
			assert.Equal(t, test.wantBody, response.Body.String())
		})
	}
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()

	handler, err := New(Options{Version: "test-version", StaticFS: testStaticFS})
	require.NoError(t, err)
	return handler
}

func request(handler http.Handler, method, target string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

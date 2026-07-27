package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testStaticFS = fstest.MapFS{
	"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><main id=app></main>")},
	"assets/app.js": &fstest.MapFile{Data: []byte("globalThis.gibson = true")},
}

func TestNewRequiresOneReadyFrontend(t *testing.T) {
	t.Parallel()

	tests := map[string]Options{
		"no frontend": {},
		"missing production index": {StaticFS: fstest.MapFS{
			"assets/app.js": &fstest.MapFile{Data: []byte("asset")},
		}},
		"production index is directory": {StaticFS: fstest.MapFS{
			"index.html": &fstest.MapFile{Mode: fs.ModeDir},
		}},
		"both frontends": {
			StaticFS: testStaticFS,
			DevProxy: &url.URL{Scheme: "http", Host: "localhost:5173"},
		},
	}

	for name, options := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler, err := New(options)
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

func TestDevelopmentProxyRoutesOnlyNonAPITraffic(t *testing.T) {
	t.Parallel()

	var proxied atomic.Int32
	vite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		_, _ = w.Write([]byte("vite:" + r.URL.RequestURI()))
	}))
	t.Cleanup(vite.Close)
	target, err := url.Parse(vite.URL)
	require.NoError(t, err)

	handler, err := New(Options{Version: "dev-version", DevProxy: target})
	require.NoError(t, err)

	response := request(handler, http.MethodGet, "/@vite/client?direct")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "vite:/@vite/client?direct", response.Body.String())
	assert.EqualValues(t, 1, proxied.Load())

	response = request(handler, http.MethodGet, "/api/health")
	require.Equal(t, http.StatusOK, response.Code)
	assert.JSONEq(t, `{"ok":true,"version":"dev-version"}`, response.Body.String())
	assert.EqualValues(t, 1, proxied.Load())

	response = request(handler, http.MethodGet, "/api/unknown")
	require.Equal(t, http.StatusNotFound, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.EqualValues(t, 1, proxied.Load())
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

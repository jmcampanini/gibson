package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"charm.land/log/v2"
	"github.com/jmcampanini/gibson/internal/pisession"
	"github.com/jmcampanini/gibson/internal/testws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var readyAssets = fstest.MapFS{
	"dist/index.html": &fstest.MapFile{Data: []byte("<!doctype html><div id=\"root\"></div>")},
}

func TestServeComposesStartupAndHTTPServer(t *testing.T) {
	ws := testws.New(t)
	nested := filepath.Join(ws.Checkout, "nested")
	require.NoError(t, os.Mkdir(nested, 0o755))
	pi := writeVersionPi(t, "0.82.1")
	ws.WriteConfig(t, fmt.Sprintf(`
[server]
port = 7311
pi_bin = %q

[sessions.test]
description = "Test session"
`, pi))

	listener := newTestListener(t)
	listenCall := make(chan string, 1)
	dependencies := testServeDependencies()
	dependencies.getwd = func() (string, error) { return nested, nil }
	dependencies.listen = func(network, address string) (net.Listener, error) {
		listenCall <- network + " " + address
		return listener, nil
	}
	var logs bytes.Buffer
	logger := log.New(&logs)

	port := 0
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() {
		result <- serve(ctx, ServeOptions{PortOverride: &port, Version: "test-version"}, logger, dependencies)
	}()

	select {
	case call := <-listenCall:
		assert.Equal(t, "tcp 127.0.0.1:0", call)
	case err := <-result:
		require.NoError(t, err)
		t.Fatal("server exited before listening")
	case <-time.After(5 * time.Second):
		t.Fatal("server did not listen")
	}

	requireHealth(t, listener.Addr(), "test-version")
	output := logs.String()
	address := "127.0.0.1:" + listenerPort(t, listener.Addr())
	assert.Contains(t, output, "Gibson is serving at http://"+address)
	assert.Contains(t, output, "server started")
	assert.Contains(t, output, "workspace="+ws.Root)
	assert.Contains(t, output, "checkout="+ws.Checkout)
	assert.Contains(t, output, "url=http://"+address)
	assert.Contains(t, output, "bind="+address)
	assert.Contains(t, output, "dev=false")
	assert.Contains(t, output, "pi_version=0.82.1")

	cancel()
	requireServeStops(t, result)
}

func TestServeWarningsDoNotPreventServing(t *testing.T) {
	ws := testws.New(t)
	pi := writeVersionPi(t, "0.83.0")
	ws.WriteConfig(t, fmt.Sprintf(`
[server]
port = 7311
pi_bin = %q
future_setting = true
`, pi))
	require.NoError(t, os.WriteFile(filepath.Join(ws.Checkout, ".gitignore"), nil, 0o644))

	listener := newTestListener(t)
	dependencies := testServeDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	dependencies.listen = func(_, _ string) (net.Listener, error) { return listener, nil }
	var logs bytes.Buffer
	logger := log.New(&logs)
	port := 0
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() {
		result <- serve(ctx, ServeOptions{PortOverride: &port, Version: "warnings-test"}, logger, dependencies)
	}()

	requireHealth(t, listener.Addr(), "warnings-test")
	output := logs.String()
	warnings := []string{
		"no session types configured",
		"pi version has not been verified with Gibson",
		"found=0.83.0",
	}
	startupAt := strings.Index(output, "server started")
	require.GreaterOrEqual(t, startupAt, 0)
	for _, warning := range warnings {
		assert.Contains(t, output, warning)
		assert.Less(t, strings.Index(output, warning), startupAt)
	}
	assert.NotContains(t, output, "unknown configuration keys")
	assert.NotContains(t, output, ".gibson/ is not ignored")

	cancel()
	requireServeStops(t, result)
}

func TestServeFailsPiPreflightBeforeListening(t *testing.T) {
	ws := testws.New(t)
	pi := writeVersionPi(t, "0.81.9")
	ws.WriteConfig(t, fmt.Sprintf(`
[server]
port = 7311
pi_bin = %q

[sessions.test]
description = "Test session"
`, pi))

	listenCalled := false
	dependencies := testServeDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	dependencies.listen = func(_, _ string) (net.Listener, error) {
		listenCalled = true
		return nil, nil
	}

	err := serve(context.Background(), ServeOptions{}, log.New(&bytes.Buffer{}), dependencies)
	require.ErrorIs(t, err, pisession.ErrPiVersionTooOld)
	assert.False(t, listenCalled)
}

func TestServeReportsOccupiedPortWithoutRetrying(t *testing.T) {
	ws := testws.New(t)
	pi := writeVersionPi(t, "0.82.1")
	occupied := newTestListener(t)
	port, err := strconv.Atoi(listenerPort(t, occupied.Addr()))
	require.NoError(t, err)
	ws.WriteConfig(t, fmt.Sprintf(`
[server]
port = %d
pi_bin = %q

[sessions.test]
description = "Test session"
`, port, pi))

	dependencies := testServeDependencies()
	dependencies.getwd = func() (string, error) { return ws.Checkout, nil }
	err = serve(context.Background(), ServeOptions{}, log.New(&bytes.Buffer{}), dependencies)

	require.Error(t, err)
	assert.Contains(t, err.Error(), strconv.Itoa(port))
	assert.Contains(t, err.Error(), "already in use")
	assert.Contains(t, err.Error(), "server.port")
	assert.Contains(t, err.Error(), "--port")
}

func TestBrowserURLUsesReachableLoopbackForWildcardBinds(t *testing.T) {
	t.Parallel()
	address := &net.TCPAddr{IP: net.IPv6unspecified, Port: 7311}
	tests := map[string]string{
		"0.0.0.0":   "http://127.0.0.1:7311",
		"::":        "http://[::1]:7311",
		"127.0.0.1": "http://127.0.0.1:7311",
		"10.0.0.7":  "http://10.0.0.7:7311",
	}

	for bind, want := range tests {
		t.Run(bind, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, want, browserURL(bind, address))
		})
	}
}

func testServeDependencies() serveDependencies {
	return serveDependencies{
		assets:         readyAssets,
		listen:         net.Listen,
		resolvePiBin:   pisession.ResolvePiBin,
		checkPiVersion: pisession.CheckPiVersion,
	}
}

func writeVersionPi(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pi")
	contents := "#!/bin/sh\nprintf '%s\\n' " + version + "\n"
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o755))
	return path
}

func newTestListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func listenerPort(t *testing.T, address net.Addr) string {
	t.Helper()
	_, port, err := net.SplitHostPort(address.String())
	require.NoError(t, err)
	return port
}

func requireHealth(t *testing.T, address net.Addr, wantVersion string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	var health struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	require.Eventually(t, func() bool {
		response, err := client.Get(fmt.Sprintf("http://%s/api/health", address))
		if err != nil {
			return false
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			return false
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&health)
		closeErr := response.Body.Close()
		return decodeErr == nil && closeErr == nil
	}, 5*time.Second, 20*time.Millisecond)
	assert.True(t, health.OK)
	assert.Equal(t, wantVersion, health.Version)
}

func requireServeStops(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}
}

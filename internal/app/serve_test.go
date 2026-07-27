package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServeComposesCheckoutConfigAndHTTPServer(t *testing.T) {
	workspaceRoot := t.TempDir()
	checkout := filepath.Join(workspaceRoot, "main")
	nested := filepath.Join(checkout, "nested")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	git := exec.Command("git", "init", "--quiet", checkout)
	output, err := git.CombinedOutput()
	require.NoError(t, err, string(output))
	require.NoError(t, os.WriteFile(
		filepath.Join(checkout, "gibson.toml"),
		[]byte("[server]\nport = 7311\n"),
		0o644,
	))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	listenCall := make(chan string, 1)
	dependencies := serveDependencies{
		assets: fstest.MapFS{
			"dist/index.html": &fstest.MapFile{Data: []byte("<!doctype html><div id=\"root\"></div>")},
		},
		getwd: func() (string, error) { return nested, nil },
		listen: func(network, address string) (net.Listener, error) {
			listenCall <- network + " " + address
			return listener, nil
		},
	}

	port := 0
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() {
		result <- serve(ctx, ServeOptions{PortOverride: &port, Version: "test-version"}, dependencies)
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

	client := &http.Client{Timeout: time.Second}
	var health struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	require.Eventually(t, func() bool {
		response, err := client.Get(fmt.Sprintf("http://%s/api/health", listener.Addr()))
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
	assert.Equal(t, "test-version", health.Version)

	cancel()
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}
}

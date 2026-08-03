package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindSessionFileUsesHeaderIdentity(t *testing.T) {
	storage := newTestStore(t)
	matching := writeSessionHeader(t, storage, "opaque-a.jsonl", "s-20260726-abc123")
	writeSessionHeader(t, storage, "s-20260726-abc123.jsonl", "s-20260726-other1")

	path, err := storage.FindSessionFile("s-20260726-abc123")

	require.NoError(t, err)
	assert.Equal(t, matching, path)
}

func TestFindSessionFileRejectsMalformedHeaders(t *testing.T) {
	tests := map[string]string{
		"empty":         "",
		"invalid json":  "{",
		"wrong type":    `{"type":"entry","version":3,"id":"s-20260726-abc123"}`,
		"wrong version": `{"type":"session","version":2,"id":"s-20260726-abc123"}`,
		"missing id":    `{"type":"session","version":3}`,
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			storage := newTestStore(t)
			require.NoError(t, os.WriteFile(filepath.Join(storage.SessionsDir(), "broken.jsonl"), []byte(contents), 0o600))

			_, err := storage.FindSessionFile("s-20260726-abc123")

			require.Error(t, err)
			assert.ErrorContains(t, err, "session header")
		})
	}
}

func TestFindSessionFileRejectsDuplicateHeaderIDs(t *testing.T) {
	storage := newTestStore(t)
	writeSessionHeader(t, storage, "opaque-a.jsonl", "s-20260726-abc123")
	writeSessionHeader(t, storage, "opaque-b.jsonl", "s-20260726-abc123")

	_, err := storage.FindSessionFile("s-20260726-abc123")

	require.Error(t, err)
	assert.ErrorContains(t, err, `duplicate session id "s-20260726-abc123"`)
}

func TestFindSessionFileRejectsSymlink(t *testing.T) {
	storage := newTestStore(t)
	target := filepath.Join(t.TempDir(), "outside.jsonl")
	require.NoError(t, os.WriteFile(target, []byte(`{"type":"session","version":3,"id":"s-20260726-abc123"}`), 0o600))
	require.NoError(t, os.Symlink(target, filepath.Join(storage.SessionsDir(), "linked.jsonl")))

	_, err := storage.FindSessionFile("s-20260726-abc123")

	require.Error(t, err)
	assert.ErrorContains(t, err, "symbolic links are not allowed")
}

func writeSessionHeader(t testing.TB, storage *Store, name, id string) string {
	t.Helper()
	path := filepath.Join(storage.SessionsDir(), name)
	header := fmt.Sprintf(`{"type":"session","version":3,"id":%q}`+"\n", id)
	require.NoError(t, os.WriteFile(path, []byte(header), 0o600))
	return path
}

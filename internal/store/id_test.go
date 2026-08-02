package store

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSessionIDUsesPiCompatibleFormat(t *testing.T) {
	before := time.Now().UTC().Format("20060102")
	id, err := Open(t.TempDir()).NewSessionID()
	require.NoError(t, err)
	after := time.Now().UTC().Format("20060102")

	require.Regexp(t, `^s-[0-9]{8}-[a-z0-9]{6}$`, id)
	assert.Contains(t, []string{before, after}, id[2:10])
}

func TestSessionIDDateIsUTC(t *testing.T) {
	localTime := time.Date(2026, 7, 27, 1, 30, 0, 0, time.FixedZone("test", 2*60*60))
	id, err := newSessionID(localTime, bytes.NewReader([]byte{0, 1, 2, 3, 4, 5}))
	require.NoError(t, err)

	assert.Equal(t, "s-20260726-abcdef", id)
}

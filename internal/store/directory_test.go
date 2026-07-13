package store

import (
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirectory_Register(t *testing.T) {
	tests := []struct {
		name    string
		dsn     string
		wantErr bool
	}{
		{"valid sqlite", ":memory:", false},
		{"file path", "file::memory:?cache=shared", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := newTestDirectory()
			defer pool.RemoveAll()

			err := pool.Register("db", testConfig(tt.dsn))
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDirectory_Register_Duplicate(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))

	err := pool.Register("primary", testConfig(":memory:"))
	assert.Error(t, err)
}

func TestDirectory_Register_UnknownDriver(t *testing.T) {
	pool := NewDirectory(NewDriverRegistry[*sqlx.DB]())
	defer pool.RemoveAll()

	err := pool.Register("db", SourceConfig{Driver: "unknown", DSN: ":memory:"})
	assert.Error(t, err)
}

func TestDirectory_Register_RequiresName(t *testing.T) {
	pool := newTestDirectory()
	defer pool.RemoveAll()

	err := pool.Register("", testConfig(":memory:"))
	require.Error(t, err)
	require.ErrorContains(t, err, "source name is required")
}

func TestDirectory_Remove(t *testing.T) {
	pool := newTestDirectory()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	require.NoError(t, pool.Remove("primary"))

	_, err := pool.get("primary")
	assert.Error(t, err)
}

func TestDirectory_Remove_NotFound(t *testing.T) {
	pool := newTestDirectory()

	err := pool.Remove("nonexistent")
	assert.Error(t, err)
}

func TestDirectory_RemoveAll(t *testing.T) {
	pool := newTestDirectory()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	require.NoError(t, pool.Register("analytics", testConfig(":memory:")))

	pool.RemoveAll()

	_, err := pool.get("primary")
	assert.Error(t, err)
	_, err = pool.get("analytics")
	assert.Error(t, err)
}

func TestDirectory_Get_AfterRemove(t *testing.T) {
	pool := newTestDirectory()

	require.NoError(t, pool.Register("primary", testConfig(":memory:")))
	require.NoError(t, pool.Remove("primary"))

	_, err := pool.get("primary")
	assert.Error(t, err)
}

package ratelimit

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPostgresMigrationsAreEmbedded(t *testing.T) {
	entries, err := migrationFiles.ReadDir("migrations")
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		contents, readErr := migrationFiles.ReadFile("migrations/" + entry.Name())
		require.NoError(t, readErr)
		require.NotEmpty(t, contents)
	}
}

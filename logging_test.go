package migrate //nolint:testpackage // Verify default logging and the existing callback contract.

import (
	"bytes"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultLogging(t *testing.T) {
	var output bytes.Buffer

	previousLogger := slog.Default()

	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	migrator, err := New(Options{})
	require.NoError(t, err)
	closeMigrationDB(t, migrator)

	migrator.task.opt.LogInfo("currently applied version: %d", 3)
	assert.Contains(t, output.String(), `"level":"INFO"`)
	assert.Contains(t, output.String(), `"msg":"currently applied version: 3"`)
}

func TestCustomLogging(t *testing.T) {
	var message string

	migrator, err := New(Options{
		LogInfo: func(format string, args ...any) {
			message = fmt.Sprintf(format, args...)
		},
	})
	require.NoError(t, err)
	closeMigrationDB(t, migrator)

	migrator.task.opt.LogInfo("applying forward migration %d (%s)", 2, "Add Email")
	assert.Equal(t, "applying forward migration 2 (Add Email)", message)
}

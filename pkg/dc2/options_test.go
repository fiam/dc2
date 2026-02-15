package dc2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseExitResourceMode(t *testing.T) {
	t.Parallel()

	t.Run("valid values", func(t *testing.T) {
		t.Parallel()
		testCases := []struct {
			input string
			want  ExitResourceMode
		}{
			{input: "cleanup", want: ExitResourceModeCleanup},
			{input: "keep", want: ExitResourceModeKeep},
			{input: "assert", want: ExitResourceModeAssert},
			{input: "  CLEANUP  ", want: ExitResourceModeCleanup},
		}
		for _, tc := range testCases {
			mode, err := ParseExitResourceMode(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.want, mode)
		}
	})

	t.Run("invalid value", func(t *testing.T) {
		t.Parallel()
		_, err := ParseExitResourceMode("nope")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid exit resource mode")
	})
}

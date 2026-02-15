package idgen

import (
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"
)

func TestHexLengthAndCharset(t *testing.T) {
	t.Parallel()

	id, err := Hex(AWSLikeHexIDLength)
	require.NoError(t, err)
	require.Len(t, id, AWSLikeHexIDLength)
	for _, r := range id {
		require.True(t, unicode.IsDigit(r) || (r >= 'a' && r <= 'f'))
	}
}

func TestWithPrefix(t *testing.T) {
	t.Parallel()

	const prefix = "vol-"
	id, err := WithPrefix(prefix, AWSLikeHexIDLength)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(id, prefix))
	require.Len(t, id, len(prefix)+AWSLikeHexIDLength)
}

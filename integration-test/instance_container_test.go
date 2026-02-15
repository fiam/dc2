package dc2_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func containerIDForInstanceID(t *testing.T, ctx context.Context, dockerHost string, instanceID string) string {
	t.Helper()

	runtimeID, ok := strings.CutPrefix(instanceID, "i-")
	require.True(t, ok, "unexpected instance id format: %s", instanceID)

	out, err := dockerCommandContext(
		ctx,
		dockerHost,
		"ps",
		"-aq",
		"--filter",
		fmt.Sprintf("label=dc2:instance-id=%s", runtimeID),
	).CombinedOutput()
	require.NoError(t, err, "docker ps output: %s", string(out))

	ids := strings.Fields(strings.TrimSpace(string(out)))
	require.Len(t, ids, 1, "unexpected containers for instance %s output: %s", instanceID, string(out))
	return ids[0]
}

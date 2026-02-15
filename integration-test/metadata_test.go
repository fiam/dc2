package dc2_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInternalMetadataEndpoint(t *testing.T) {
	t.Parallel()

	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.Endpoint+"/_dc2/metadata", nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var payload struct {
			Name   string `json:"name"`
			Region string `json:"region"`
			Build  struct {
				Version    string `json:"version"`
				Commit     string `json:"commit"`
				CommitTime string `json:"commit_time"`
				Dirty      bool   `json:"dirty"`
				GoVersion  string `json:"go_version"`
			} `json:"build"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
		assert.Equal(t, "dc2", payload.Name)
		assert.Equal(t, e.Region, payload.Region)
		assert.NotEmpty(t, payload.Build.Version)
		assert.NotEmpty(t, payload.Build.GoVersion)
	})
}

package dc2

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/api"
)

func TestServeReadyReportsStartupState(t *testing.T) {
	t.Parallel()

	srv := &Server{
		opts: options{
			Region:           "eu-test-1",
			InstanceNetwork:  "dc2-test-network",
			TestProfileInput: "delays: {}",
			ExitResourceMode: ExitResourceModeAssert,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/_dc2/ready", nil)
	rec := httptest.NewRecorder()
	srv.serveReady(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var payload struct {
		Name              string `json:"name"`
		Ready             bool   `json:"ready"`
		Region            string `json:"region"`
		ConfiguredNetwork string `json:"configured_instance_network"`
		ExitResourceMode  string `json:"exit_resource_mode"`
		IMDSBackendPort   int    `json:"imds_backend_port"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	assert.Equal(t, "dc2", payload.Name)
	assert.True(t, payload.Ready)
	assert.Equal(t, "eu-test-1", payload.Region)
	assert.Equal(t, "dc2-test-network", payload.ConfiguredNetwork)
	assert.Equal(t, string(ExitResourceModeAssert), payload.ExitResourceMode)
	assert.Zero(t, payload.IMDSBackendPort)
}

func TestServeAWSRecoversPanic(t *testing.T) {
	t.Parallel()

	srv := &Server{
		format: panicFormat{},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := api.ContextWithLogger(t.Context(), logger)
	req := httptest.NewRequest(http.MethodPost, "/?Action=DescribeInstances", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	srv.serveAWS(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "boom")
}

type panicFormat struct{}

func (panicFormat) DecodeRequest(*http.Request) (api.Request, error) {
	panic("boom")
}

func (panicFormat) EncodeError(context.Context, http.ResponseWriter, error) error {
	return nil
}

func (panicFormat) EncodeResponse(context.Context, http.ResponseWriter, api.Response) error {
	return nil
}

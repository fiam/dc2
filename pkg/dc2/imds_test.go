package dc2

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dc2docker "github.com/fiam/dc2/pkg/dc2/docker"
)

func TestIMDSTokenTTL(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{name: "min value", raw: "1", want: 1},
		{name: "max value", raw: "21600", want: 21600},
		{name: "trimmed value", raw: " 60 ", want: 60},
		{name: "empty", raw: "", wantErr: true},
		{name: "non integer", raw: "abc", wantErr: true},
		{name: "too small", raw: "0", wantErr: true},
		{name: "too large", raw: "21601", wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := imdsTokenTTL(tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIMDSClientIP(t *testing.T) {
	t.Parallel()

	t.Run("prefers first X-Forwarded-For address", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest("GET", "http://localhost/latest/meta-data/instance-id", nil)
		req.RemoteAddr = "10.0.0.2:1234"
		req.Header.Set("X-Forwarded-For", " 203.0.113.10, 198.51.100.9 ")

		assert.Equal(t, "203.0.113.10", imdsClientIP(req))
	})

	t.Run("falls back to remote host", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest("GET", "http://localhost/latest/meta-data/instance-id", nil)
		req.RemoteAddr = "192.0.2.10:4321"

		assert.Equal(t, "192.0.2.10", imdsClientIP(req))
	})

	t.Run("returns raw remote address when split fails", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest("GET", "http://localhost/latest/meta-data/instance-id", nil)
		req.RemoteAddr = "invalid-remote-addr"

		assert.Equal(t, "invalid-remote-addr", imdsClientIP(req))
	})
}

func TestSummaryHasIP(t *testing.T) {
	t.Parallel()

	assert.False(t, summaryHasIP(container.Summary{}, "10.0.0.5"))

	summary := container.Summary{
		NetworkSettings: &container.NetworkSettingsSummary{
			Networks: map[string]*network.EndpointSettings{
				"bridge": {
					IPAddress: "10.0.0.5",
				},
			},
		},
	}
	assert.True(t, summaryHasIP(summary, "10.0.0.5"))
	assert.False(t, summaryHasIP(summary, "10.0.0.6"))
}

func TestIMDSInstanceRuntimeID(t *testing.T) {
	t.Parallel()

	info := &container.InspectResponse{
		Config: &container.Config{
			Labels: map[string]string{dc2docker.LabelDC2InstanceID: "abc123"},
		},
	}
	id, ok := imdsInstanceRuntimeID(info)
	require.True(t, ok)
	assert.Equal(t, "abc123", id)

	_, ok = imdsInstanceRuntimeID(&container.InspectResponse{Config: &container.Config{Labels: map[string]string{}}})
	assert.False(t, ok)
}

func TestIMDSControllerTokenLifecycle(t *testing.T) {
	t.Parallel()

	c := &imdsController{}

	tokenA, err := c.issueToken("container-a", 10)
	require.NoError(t, err)
	assert.True(t, c.validateToken(tokenA, "container-a"))
	assert.False(t, c.validateToken(tokenA, "container-b"))

	c.tokens.Store(tokenA, imdsToken{
		containerID: "container-a",
		expiresAt:   time.Now().Add(-time.Second),
	})
	assert.False(t, c.validateToken(tokenA, "container-a"))
	_, found := c.tokens.Load(tokenA)
	assert.False(t, found)

	tokenB, err := c.issueToken("container-b", 10)
	require.NoError(t, err)
	tokenC, err := c.issueToken("container-c", 10)
	require.NoError(t, err)

	c.revokeTokensLocal("container-b")

	_, found = c.tokens.Load(tokenB)
	assert.False(t, found)
	_, found = c.tokens.Load(tokenC)
	assert.True(t, found)
}

func TestIMDSControllerEnablementAndHeaderValidation(t *testing.T) {
	t.Parallel()

	c := &imdsController{}

	token, err := c.issueToken("container-a", 10)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "http://localhost/latest/meta-data/instance-id", nil)
	req.Header.Set(imdsTokenHeader, token)
	assert.True(t, c.hasValidToken(req, "container-a"))

	require.NoError(t, c.SetEnabled("container-a", false))
	assert.False(t, c.Enabled("container-a"))
	assert.False(t, c.hasValidToken(req, "container-a"))

	require.NoError(t, c.SetEnabled("container-a", true))
	assert.True(t, c.Enabled("container-a"))
}

func TestIMDSControllerTags(t *testing.T) {
	t.Parallel()

	c := &imdsController{}

	require.NoError(t, c.SetTags("container-a", map[string]string{"Name": "example"}))
	tags := c.tags("container-a")
	assert.Equal(t, "example", tags["Name"])

	tags["Name"] = "changed"
	assert.Equal(t, "example", c.tags("container-a")["Name"])

	require.NoError(t, c.SetTags("container-a", nil))
	assert.Empty(t, c.tags("container-a"))

	c.instanceTags.Store("container-b", 42)
	assert.Empty(t, c.tags("container-b"))
	_, exists := c.instanceTags.Load("container-b")
	assert.False(t, exists)
}

func TestIMDSControllerSpotActions(t *testing.T) {
	t.Parallel()

	c := &imdsController{}

	terminationTime := time.Now().UTC().Add(2 * time.Minute).Round(0)
	require.NoError(t, c.SetSpotInstanceAction("container-a", "terminate", terminationTime))

	action, ok := c.spotAction("container-a")
	require.True(t, ok)
	assert.Equal(t, "terminate", action.Action)
	assert.True(t, action.TerminationTime.Equal(terminationTime.UTC()))

	require.NoError(t, c.ClearSpotInstanceAction("container-a"))
	_, ok = c.spotAction("container-a")
	assert.False(t, ok)

	c.spotActions.Store("container-b", 42)
	_, ok = c.spotAction("container-b")
	assert.False(t, ok)
	_, exists := c.spotActions.Load("container-b")
	assert.False(t, exists)
}

package dc2

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/fiam/dc2/pkg/dc2/api"
)

func TestRunInstancesSubnetID(t *testing.T) {
	t.Parallel()

	assert.Equal(t, defaultSubnetID, runInstancesSubnetID(nil))
	assert.Equal(t, defaultSubnetID, runInstancesSubnetID(&api.RunInstancesRequest{}))
	assert.Equal(
		t,
		"subnet-custom",
		runInstancesSubnetID(&api.RunInstancesRequest{SubnetID: "  subnet-custom  "}),
	)
}

func TestSubnetVPCID(t *testing.T) {
	t.Parallel()

	assert.Equal(t, defaultSubnetVPCID, subnetVPCID(""))
	assert.Equal(t, defaultSubnetVPCID, subnetVPCID(defaultSubnetID))

	first := subnetVPCID("subnet-dc2")
	second := subnetVPCID("subnet-dc2")
	assert.Equal(t, first, second)
	assert.NotEqual(t, defaultSubnetVPCID, first)
	assert.True(t, strings.HasPrefix(first, "vpc-"))
	assert.Len(t, first, len("vpc-")+17)
	assert.NotEqual(t, first, subnetVPCID("subnet-other"))
}

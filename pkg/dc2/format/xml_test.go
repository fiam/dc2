package format

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/api"
)

func TestEncodeNilFields(t *testing.T) {
	r1 := &api.DescribeVolumesResponse{}
	s1, err := encodeResponse(t.Context(), r1)
	require.NoError(t, err)

	// NextToken is nil, it should not be present in the doc, otherwise
	// the SDK will decode it as "" instead of nil.
	assert.NotContains(t, s1, "NextToken")

	// A non-nil but empty value should be present in the response
	r2 := &api.DescribeVolumesResponse{NextToken: aws.String("")}
	s2, err := encodeResponse(t.Context(), r2)
	require.NoError(t, err)
	assert.Contains(t, s2, "NextToken")

	// A non-nil non-empty value should be present in the response
	r3 := &api.DescribeVolumesResponse{NextToken: aws.String("foo")}
	s3, err := encodeResponse(t.Context(), r3)
	require.NoError(t, err)
	assert.Contains(t, s3, "NextToken")
	assert.Contains(t, s3, "foo")
}

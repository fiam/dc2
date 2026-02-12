package format

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/api"
)

func TestEncodeNilFields(t *testing.T) {
	t.Parallel()
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

func TestEncodeResponseNamespace(t *testing.T) {
	t.Parallel()

	ec2Resp := &api.DescribeVolumesResponse{}
	ec2XML, err := encodeResponse(t.Context(), ec2Resp)
	require.NoError(t, err)
	assert.Contains(t, ec2XML, "http://ec2.amazonaws.com/doc/2016-11-15/")

	asgResp := &api.DescribeAutoScalingGroupsResponse{}
	asgXML, err := encodeResponse(t.Context(), asgResp)
	require.NoError(t, err)
	assert.Contains(t, asgXML, "http://autoscaling.amazonaws.com/doc/2011-01-01/")
}

func TestEncodeResponseRequestIDLocation(t *testing.T) {
	t.Parallel()
	ctx := api.ContextWithRequestID(t.Context(), "req-123")

	ec2Resp := &api.DescribeVolumesResponse{}
	ec2XML, err := encodeResponse(ctx, ec2Resp)
	require.NoError(t, err)
	assert.Contains(t, ec2XML, "<RequestId>req-123</RequestId>")
	assert.NotContains(t, ec2XML, "<ResponseMetadata>")

	asgResp := &api.DescribeAutoScalingGroupsResponse{}
	asgXML, err := encodeResponse(ctx, asgResp)
	require.NoError(t, err)
	assert.Contains(t, asgXML, "<ResponseMetadata>\n    <RequestId>req-123</RequestId>")
	assert.NotContains(t, asgXML, "<DescribeAutoScalingGroupsResponse xmlns=\"http://autoscaling.amazonaws.com/doc/2011-01-01/\">\n  <RequestId>")
}

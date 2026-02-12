package docker

import "github.com/docker/docker/api/types/container"

const (
	LabelDC2Enabled      = "dc2:enabled"
	LabelDC2ImageID      = "dc2:image-id"
	LabelDC2IMDSHost     = "dc2:imds-backend-host"
	LabelDC2IMDSOwner    = "dc2:imds-owner"
	LabelDC2IMDSPort     = "dc2:imds-backend-port"
	LabelDC2InstanceNet  = "dc2:instance-network"
	LabelDC2InstanceType = "dc2:instance-type"
	LabelDC2KeyName      = "dc2:key-name"
	LabelDC2OwnedNetwork = "dc2:owned-network"
	LabelDC2UserData     = "dc2:user-data"
	LabelDC2Main         = "dc2:main"
)

func isDc2Container(c container.InspectResponse) bool {
	if c.Config == nil {
		return false
	}
	return c.Config.Labels[LabelDC2Enabled] == "true"
}

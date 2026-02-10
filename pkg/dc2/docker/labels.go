package docker

import "github.com/docker/docker/api/types"

const (
	LabelDC2Enabled      = "dc2:enabled"
	LabelDC2ImageID      = "dc2:image-id"
	LabelDC2InstanceType = "dc2:instance-type"
	LabelDC2KeyName      = "dc2:key-name"
	LabelDC2UserData     = "dc2:user-data"
	LabelDC2Main         = "dc2:main"
)

func isDc2Container(c types.ContainerJSON) bool {
	return c.Config.Labels[LabelDC2Enabled] == "true"
}

package dc2

import "github.com/docker/docker/api/types"

const (
	LabelDC2Enabled      = "dc2:enabled"
	LabelDC2ImageID      = "dc2:image-id"
	LabelDC2InstanceType = "dc2:instance-type"
	LabelDC2KeyName      = "dc2:key-name"
)

func isDc2Container(c types.ContainerJSON) bool {
	return c.Config.Labels[LabelDC2Enabled] == "true"
}

package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/executor"
	"github.com/google/uuid"
)

const (
	ContainerStateCreated  = "created"
	ContainerStateRunning  = "running"
	ContainerStatePaused   = "paused"
	ContainerStateStopped  = "stopped"
	ContainerStateExited   = "exited"
	ContainerStateDead     = "dead"
	ContainerStateRemoving = "removing"
)

const (
	mainVolumeName         = "dc2"
	mainVolumePath         = "/dc2"
	mainContainerImageName = "alpine:latest"
	mainContainerName      = "dc2"
	loopDevicePrefix       = "/dev/loop"
)

var _ executor.Executor = (*Executor)(nil)

type Executor struct {
	cli             *client.Client
	mainVolume      volume.Volume
	mainContainerID string
}

func NewExecutor(ctx context.Context) (*Executor, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating Docker client: %w", err)
	}

	pingContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(pingContext); err != nil {
		return nil, fmt.Errorf("pinging Docker daemon: %w", err)
	}

	u, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("generating executor suffix: %w", err)
	}
	suffix := "_" + u.String()[:8]

	// Creating an already existing volume is a valid operation
	vol, err := cli.VolumeCreate(ctx, volume.CreateOptions{
		Name: mainVolumeName + suffix,
	})

	if err != nil {
		return nil, fmt.Errorf("creating dc2 master volume")
	}

	id, err := createMainContainer(ctx, cli, mainContainerName+suffix)
	if err != nil {
		return nil, fmt.Errorf("creating main container: %w", err)
	}

	return &Executor{
		cli:             cli,
		mainVolume:      vol,
		mainContainerID: id,
	}, nil
}

func (e *Executor) Close(ctx context.Context) error {
	if err := e.cli.ContainerRemove(ctx, e.mainContainerID, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("removing main container %s: %w", e.mainContainerID, err)
	}
	if err := e.cli.VolumeRemove(ctx, e.mainVolume.Name, true); err != nil {
		return fmt.Errorf("removing main volume %s: %w", e.mainContainerID, err)
	}
	return nil
}

func (e *Executor) CreateInstances(ctx context.Context, req executor.CreateInstancesRequest) ([]executor.InstanceID, error) {
	if err := pullImage(ctx, e.cli, req.ImageID); err != nil {
		return nil, fmt.Errorf("pulling image: %w", err)
	}
	instanceIDs := make([]executor.InstanceID, req.Count)
	for i := range req.Count {
		containerConfig := &container.Config{
			Image: req.ImageID,
			Labels: map[string]string{
				LabelDC2Enabled:      "true",
				LabelDC2InstanceType: req.InstanceType,
				LabelDC2ImageID:      req.ImageID,
			},
		}
		hostConfig := &container.HostConfig{
			// Allow mounting block devices to attach volumes
			Privileged: true,
			Mounts:     dc2Mounts(),
		}
		networkingConfig := &network.NetworkingConfig{}
		cont, err := e.cli.ContainerCreate(ctx, containerConfig, hostConfig, networkingConfig, nil, "")
		if err != nil {
			return nil, fmt.Errorf("creating container: %w", err)
		}
		instanceIDs[i] = executor.InstanceID(cont.ID)
	}
	return instanceIDs, nil
}

func (e *Executor) DescribeInstances(ctx context.Context, req executor.DescribeInstancesRequest) ([]executor.InstanceDescription, error) {
	var descriptions []executor.InstanceDescription
	for _, id := range req.InstanceIDs {
		info, err := e.cli.ContainerInspect(ctx, string(id))
		if err != nil {
			// Specifying non-existing IDs is not an error
			if errdefs.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("getting spec for container %s: %w", id, err)
		}
		if !isDc2Container(info) {
			continue
		}
		desc, err := e.instanceDescription(ctx, &info)
		if err != nil {
			return nil, err
		}
		descriptions = append(descriptions, desc)
	}
	return descriptions, nil
}

func (e *Executor) StartInstances(ctx context.Context, req executor.StartInstancesRequest) ([]executor.InstanceStateChange, error) {
	containers, err := e.findContainers(ctx, req.InstanceIDs)
	if err != nil {
		return nil, err
	}
	changes := make([]executor.InstanceStateChange, len(containers))
	for i, c := range containers {
		previousState, err := instanceState(c.State)
		if err != nil {
			return nil, fmt.Errorf("determining previous state for instance %s: %w", c.ID, err)
		}
		if err := e.cli.ContainerStart(ctx, c.ID, container.StartOptions{}); err != nil {
			return nil, fmt.Errorf("starting instance %s: %w", c.ID, err)
		}
		info, err := e.cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("inspecting container %s: %w", c.ID, err)
		}
		currentState, err := instanceState(info.State)
		if err != nil {
			return nil, fmt.Errorf("determining current state for instance %s: %w", c.ID, err)
		}
		changes[i] = executor.InstanceStateChange{
			InstanceID:    executor.InstanceID(c.ID),
			PreviousState: previousState,
			CurrentState:  currentState,
		}
	}
	return changes, nil
}

func (e *Executor) StopInstances(ctx context.Context, req executor.StopInstancesRequest) ([]executor.InstanceStateChange, error) {
	containers, err := e.findContainers(ctx, req.InstanceIDs)
	if err != nil {
		return nil, err
	}
	var timeout *int
	if req.Force {
		zero := 0
		timeout = &zero
	}
	changes := make([]executor.InstanceStateChange, len(containers))
	for i, c := range containers {
		previousState, err := instanceState(c.State)
		if err != nil {
			return nil, fmt.Errorf("determining previous state for instance %s: %w", c.ID, err)
		}
		if err := e.cli.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: timeout}); err != nil {
			return nil, fmt.Errorf("stopping instance %s: %w", c.ID, err)
		}
		info, err := e.cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("inspecting container %s: %w", c.ID, err)
		}
		currentState, err := instanceState(info.State)
		if err != nil {
			return nil, fmt.Errorf("determining current state for instance %s: %w", c.ID, err)
		}
		changes[i] = executor.InstanceStateChange{
			InstanceID:    executor.InstanceID(c.ID),
			PreviousState: previousState,
			CurrentState:  currentState,
		}
	}
	return changes, nil
}

func (e *Executor) TerminateInstances(ctx context.Context, req executor.TerminateInstancesRequest) ([]executor.InstanceStateChange, error) {
	containers, err := e.findContainers(ctx, req.InstanceIDs)
	if err != nil {
		return nil, err
	}
	changes := make([]executor.InstanceStateChange, len(containers))
	for i, c := range containers {
		previousState, err := instanceState(c.State)
		if err != nil {
			return nil, fmt.Errorf("determining previous state for instance %s: %w", c.ID, err)
		}
		if c.State.Running {
			if err := e.cli.ContainerStop(ctx, c.ID, container.StopOptions{}); err != nil {
				return nil, fmt.Errorf("stopping instance %s: %w", c.ID, err)
			}
		}
		if err := e.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{}); err != nil {
			return nil, fmt.Errorf("removing instance %s: %w", c.ID, err)
		}

		changes[i] = executor.InstanceStateChange{
			InstanceID:    executor.InstanceID(c.ID),
			PreviousState: previousState,
			CurrentState:  api.InstanceStateTerminated,
		}
	}
	return changes, nil
}

func (e *Executor) CreateVolume(ctx context.Context, req executor.CreateVolumeRequest) (executor.VolumeID, error) {
	u, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	id := executor.VolumeID(hex.EncodeToString(u[:]))
	volumeFileCmd := []string{"truncate", "-s", strconv.FormatInt(req.Size, 10), internalVolumeFilePath(id)}
	if _, _, err := e.execInMainContainer(ctx, volumeFileCmd); err != nil {
		return "", fmt.Errorf("executing command to create volume file: %w", err)
	}
	attachmentsFileCmd := []string{"touch", internalVolumeAttachmentInfoPath(id)}
	if _, _, err := e.execInMainContainer(ctx, attachmentsFileCmd); err != nil {
		return "", fmt.Errorf("executing command to create volume attachments file: %w", err)
	}
	return id, nil
}

func (e *Executor) DeleteVolume(ctx context.Context, req executor.DeleteVolumeRequest) error {
	deleteVolumeCmd := []string{"rm", internalVolumeFilePath(req.VolumeID)}
	if _, _, err := e.execInMainContainer(ctx, deleteVolumeCmd); err != nil {
		return fmt.Errorf("executing command to delete volume: %w", err)
	}
	deleteAttachmentsCmd := []string{"rm", "-f", internalVolumeAttachmentInfoPath(req.VolumeID)}
	if _, _, err := e.execInMainContainer(ctx, deleteAttachmentsCmd); err != nil {
		return fmt.Errorf("executing command to delete volume attachments: %w", err)
	}
	return nil
}

func (e *Executor) AttachVolume(ctx context.Context, req executor.AttachVolumeRequest) (*executor.VolumeAttachment, error) {
	nextLoopDevice, _, err := e.execInContainer(ctx, string(req.InstanceID), []string{"losetup", "-f"})
	if err != nil {
		return nil, fmt.Errorf("find next available loop device: %w", err)
	}
	if !strings.HasPrefix(nextLoopDevice, loopDevicePrefix) {
		return nil, fmt.Errorf("unknown loop device %q", nextLoopDevice)
	}
	num, err := strconv.Atoi(strings.TrimSpace((nextLoopDevice[len(loopDevicePrefix):])))
	if err != nil {
		return nil, fmt.Errorf("invalid loop device number: %w", err)
	}
	deviceCmd := []string{
		"mknod",
		req.Device,
		"b",               // block device
		"7",               // major number for loop devices
		strconv.Itoa(num), // next available one
	}
	if _, _, err := e.execInContainer(ctx, string(req.InstanceID), deviceCmd); err != nil {
		return nil, fmt.Errorf("creating device %s: %w", req.Device, err)
	}
	setupCmd := []string{"losetup", req.Device, internalVolumeFilePath(req.VolumeID)}
	if _, _, err := e.execInContainer(ctx, string(req.InstanceID), setupCmd); err != nil {
		return nil, fmt.Errorf("setting up device device %s: %w", req.Device, err)
	}
	// Record the attachment
	info := deviceAttachment{
		InstanceID:    req.InstanceID,
		Device:        req.Device,
		LoopDeviceNum: num,
		AttachTime:    time.Now(),
	}
	if err := e.recordAttachment(ctx, req.VolumeID, info); err != nil {
		return nil, fmt.Errorf("recording attachment: %w", err)
	}
	return &executor.VolumeAttachment{
		Device:     req.Device,
		InstanceID: req.InstanceID,
		AttachTime: info.AttachTime,
	}, nil
}

func (e *Executor) DetachVolume(ctx context.Context, req executor.DetachVolumeRequest) (*executor.VolumeAttachment, error) {
	var attachment *deviceAttachment
	atts, err := e.findVolumeAttachments(ctx, req.VolumeID)
	if err != nil {
		return nil, fmt.Errorf("finding volume attachments: %w", err)
	}
	for _, a := range atts {
		if a.InstanceID == req.InstanceID && a.Device == req.Device {
			attachment = &a
			break
		}
	}
	if attachment == nil {
		return nil, fmt.Errorf("volume %s not attached to instance %s on device %s", req.VolumeID, req.InstanceID, req.Device)
	}
	losetupCmd := []string{"losetup", "-d", attachment.Device}
	if _, _, err := e.execInContainer(ctx, string(req.InstanceID), losetupCmd); err != nil {
		return nil, fmt.Errorf("removing loopback device %s: %w", req.Device, err)
	}
	deviceCmd := []string{"rm", "-f", attachment.Device}
	if _, _, err := e.execInContainer(ctx, string(req.InstanceID), deviceCmd); err != nil {
		return nil, fmt.Errorf("removing dev device %s: %w", req.Device, err)
	}
	if err := e.deleteAttachment(ctx, req.VolumeID, *attachment); err != nil {
		return nil, fmt.Errorf("deleting attachment info: %w", err)
	}
	return &executor.VolumeAttachment{
		Device:     req.Device,
		InstanceID: req.InstanceID,
		AttachTime: attachment.AttachTime,
	}, nil
}

func (e *Executor) DescribeVolumes(ctx context.Context, req executor.DescribeVolumesRequest) ([]executor.VolumeDescription, error) {
	descs := make([]executor.VolumeDescription, len(req.VolumeIDs))
	for i, id := range req.VolumeIDs {
		cmd := []string{"du", "-b", internalVolumeFilePath(id)}
		stdout, _, err := e.execInMainContainer(ctx, cmd)
		if err != nil {
			return nil, err
		}
		sep := strings.IndexByte(stdout, '\t')
		if sep == -1 {
			return nil, fmt.Errorf("invalid du output %q", stdout)
		}
		n, err := strconv.ParseInt(stdout[:sep], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid du output %q: %w", stdout, err)
		}
		atts, err := e.findVolumeAttachments(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("finding volume attachments: %w", err)
		}
		attachments := make([]executor.VolumeAttachment, len(atts))
		for i, a := range atts {
			attachments[i] = executor.VolumeAttachment{
				InstanceID: a.InstanceID,
				Device:     a.Device,
				AttachTime: a.AttachTime,
			}
		}
		descs[i] = executor.VolumeDescription{
			VolumeID:    id,
			Size:        n,
			Attachments: attachments,
		}
	}
	return descs, nil
}

func (e *Executor) findContainers(ctx context.Context, instanceIDs []executor.InstanceID) ([]*types.ContainerJSON, error) {
	var containers []*types.ContainerJSON
	// Validate all the instances first
	for _, id := range instanceIDs {
		info, err := e.cli.ContainerInspect(ctx, string(id))
		if err != nil {
			// Container doesn't exist
			if errdefs.IsNotFound(err) {
				return nil, api.ErrWithCode(api.ErrorCodeInstanceNotFound, fmt.Errorf("instance %s doesn't exist: %w", id, err))
			}
			// Error when talking to the daemon
			return nil, fmt.Errorf("retrieving container %s: %w", id, err)
		}
		if !isDc2Container(info) {
			return nil, api.ErrWithCode(api.ErrorCodeInstanceNotFound, fmt.Errorf("instance %s doesn't exist", id))
		}
		containers = append(containers, &info)
	}
	return containers, nil
}

func (e *Executor) instanceDescription(ctx context.Context, info *types.ContainerJSON) (executor.InstanceDescription, error) {
	created, err := time.Parse(time.RFC3339Nano, info.Created)
	if err != nil {
		return executor.InstanceDescription{}, fmt.Errorf("parsing container creation time: %w", err)
	}
	labels := info.Config.Labels
	image, _, err := e.cli.ImageInspectWithRaw(ctx, info.Image)
	if err != nil {
		return executor.InstanceDescription{}, fmt.Errorf("inspecting image: %w", err)
	}
	imageID := labels[LabelDC2ImageID]
	state, err := instanceState(info.State)
	if err != nil {
		return executor.InstanceDescription{}, fmt.Errorf("instance state: %w", err)
	}
	instanceType := labels[LabelDC2InstanceType]
	// First character in c.Name is /
	dnsName := info.Name[1:]
	return executor.InstanceDescription{
		InstanceID:     executor.InstanceID(info.ID),
		ImageID:        imageID,
		InstanceState:  state,
		PrivateDNSName: dnsName,
		InstanceType:   instanceType,
		Architecture:   awsArchFromDockerArch(image.Architecture),
		LaunchTime:     created,
	}, nil
}

func (e *Executor) execInMainContainer(ctx context.Context, cmd []string) (string, string, error) {
	return e.execInContainer(ctx, e.mainContainerID, cmd)
}

func (e *Executor) execInContainer(ctx context.Context, containerID string, cmd []string) (string, string, error) {
	opts := container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	}
	createResp, err := e.cli.ContainerExecCreate(ctx, containerID, opts)
	if err != nil {
		return "", "", fmt.Errorf("creating exec session: %w", err)
	}

	attachResp, err := e.cli.ContainerExecAttach(ctx, createResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", "", fmt.Errorf("attaching to exec session: %w", err)
	}
	defer attachResp.Close()

	var stdout, stderr bytes.Buffer

	_, err = stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
	if err != nil {
		return "", "", fmt.Errorf("reading exec session: %w", err)
	}

	inspectResp, err := e.cli.ContainerExecInspect(ctx, createResp.ID)
	if err != nil {
		return "", "", fmt.Errorf("inspecting exec session: %w", err)
	}
	if inspectResp.ExitCode != 0 {
		return "", "", fmt.Errorf("exec session returned non-zero exit code %d: stdout: %s, stderr: %s", inspectResp.ExitCode, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String(), nil
}

func (e *Executor) recordAttachment(ctx context.Context, vol executor.VolumeID, info deviceAttachment) error {
	recordCmd := []string{"sh", "-c", fmt.Sprintf("echo %s >> %s", info.String(), internalVolumeAttachmentInfoPath(vol))}
	if _, _, err := e.execInMainContainer(ctx, recordCmd); err != nil {
		return fmt.Errorf("recording attachment: %w", err)
	}
	return nil
}

func (e *Executor) deleteAttachment(ctx context.Context, vol executor.VolumeID, info deviceAttachment) error {
	deleteCmd := []string{"sh", "-c", fmt.Sprintf("sed -i '#%s#d' %s", info.String(), internalVolumeAttachmentInfoPath(vol))}
	if _, _, err := e.execInMainContainer(ctx, deleteCmd); err != nil {
		return fmt.Errorf("deleting attachment: %w", err)
	}
	return nil
}

func (e *Executor) findVolumeAttachments(ctx context.Context, vol executor.VolumeID) ([]deviceAttachment, error) {
	stdout, _, err := e.execInMainContainer(ctx, []string{"cat", internalVolumeAttachmentInfoPath(vol)})
	if err != nil {
		return nil, fmt.Errorf("reading volume attachments: %w", err)
	}
	var attachments []deviceAttachment
	r := bufio.NewScanner(strings.NewReader(stdout))
	for r.Scan() {
		line := r.Text()
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 4 {
			return nil, fmt.Errorf("bad volume attachment info %q", line)
		}
		loopDeviceNum, err := strconv.Atoi(parts[2])
		if err != nil {
			return nil, fmt.Errorf("parsing loop device number: %w", err)
		}
		attachTimeUnixNano, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing volume attach time: %w", err)
		}
		info := deviceAttachment{
			InstanceID:    executor.InstanceID(parts[0]),
			Device:        parts[1],
			LoopDeviceNum: loopDeviceNum,
			AttachTime:    time.Unix(0, attachTimeUnixNano),
		}
		attachments = append(attachments, info)
	}
	if err := r.Err(); err != nil {
		return nil, fmt.Errorf("scanning volume attachments: %w", err)
	}
	return attachments, nil
}

func instanceState(state *types.ContainerState) (api.InstanceState, error) {
	if state == nil {
		return api.InstanceState{}, errors.New("nil container state")
	}

	switch {
	case state.Status == "created":
		return api.InstanceStatePending, nil
	case state.Running && !state.Paused:
		return api.InstanceStateRunning, nil
	case state.Paused:
		return api.InstanceStateStopping, nil
	case state.Status == "exited":
		return api.InstanceStateStopped, nil
	case state.Dead:
		return api.InstanceStateTerminated, nil
	case state.Status == "removing":
		return api.InstanceStateShuttingDown, nil
	default:
		return api.InstanceState{}, errors.New("unknown container state")
	}
}

func awsArchFromDockerArch(arch string) string {
	if arch == "amd64" {
		return "x86_64"
	}
	return arch
}

func createMainContainer(ctx context.Context, cli *client.Client, name string) (string, error) {
	if err := pullImage(ctx, cli, mainContainerImageName); err != nil {
		return "", fmt.Errorf("pulling image for main container: %w", err)
	}

	containerConfig := &container.Config{
		Image: mainContainerImageName,
		Cmd:   strslice.StrSlice([]string{"sleep", "infinity"}),
	}
	hostConfig := &container.HostConfig{
		AutoRemove: true,
		Mounts:     dc2Mounts(),
	}
	networkingConfig := &network.NetworkingConfig{}
	cont, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, networkingConfig, nil, name)
	if err != nil {
		return "", fmt.Errorf("creating main container: %w", err)
	}

	if err := cli.ContainerStart(ctx, cont.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("starting main container: %w", err)
	}

	return cont.ID, nil
}

func pullImage(ctx context.Context, cli *client.Client, imageName string) error {
	api.Logger(ctx).Debug("pulling image", slog.String("name", imageName))
	pullProgress, err := cli.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("starting pull for %s: %w", imageName, err)
	}
	if _, err := io.ReadAll(pullProgress); err != nil {
		pullProgress.Close()
		return fmt.Errorf("pulling %s: %w", imageName, err)
	}
	if err := pullProgress.Close(); err != nil {
		return fmt.Errorf("finalizing pull for %s: %w", imageName, err)
	}
	return nil
}

func dc2Mounts() []mount.Mount {
	return []mount.Mount{
		{
			Type:   mount.TypeVolume,
			Source: mainVolumeName,
			Target: mainVolumePath,
		},
	}
}

func internalVolumeFilePath(id executor.VolumeID) string {
	return fmt.Sprintf("%s/%s", mainVolumePath, id)
}

type deviceAttachment struct {
	InstanceID    executor.InstanceID
	Device        string
	LoopDeviceNum int
	AttachTime    time.Time
}

func (i *deviceAttachment) String() string {
	return fmt.Sprintf("%s:%s:%d:%d", i.InstanceID, i.Device, i.LoopDeviceNum, i.AttachTime.UnixNano())
}

func internalVolumeAttachmentInfoPath(id executor.VolumeID) string {
	return fmt.Sprintf("%s.attachments", internalVolumeFilePath(id))
}

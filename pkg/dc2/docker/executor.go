package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/fiam/dc2/pkg/dc2/api"
	"github.com/fiam/dc2/pkg/dc2/executor"
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

var _ executor.Executor = (*Executor)(nil)

type Executor struct {
	cli *client.Client
}

func NewExecutor() (*Executor, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating Docker client: %w", err)
	}

	pingContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(pingContext); err != nil {
		return nil, fmt.Errorf("pinging Docker daemon: %w", err)
	}
	return &Executor{
		cli: cli,
	}, nil
}

func (e *Executor) CreateInstances(ctx context.Context, req executor.CreateInstancesRequest) ([]string, error) {
	if err := e.pullImage(ctx, req.ImageID); err != nil {
		return nil, fmt.Errorf("pulling image: %w", err)
	}
	instanceIDs := make([]string, req.Count)
	for i := range req.Count {
		containerConfig := &container.Config{
			Image: req.ImageID,
			Labels: map[string]string{
				LabelDC2Enabled:      "true",
				LabelDC2InstanceType: req.InstanceType,
				LabelDC2ImageID:      req.ImageID,
			},
		}
		hostConfig := &container.HostConfig{}
		networkingConfig := &network.NetworkingConfig{}
		cont, err := e.cli.ContainerCreate(ctx, containerConfig, hostConfig, networkingConfig, nil, "")
		if err != nil {
			return nil, fmt.Errorf("creating container: %w", err)
		}
		instanceIDs[i] = cont.ID
	}
	return instanceIDs, nil
}

func (e *Executor) DescribeInstances(ctx context.Context, req executor.DescribeInstancesRequest) ([]executor.InstanceDescription, error) {
	var descriptions []executor.InstanceDescription
	for _, id := range req.InstanceIDs {
		info, err := e.cli.ContainerInspect(ctx, id)
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
	changes := make([]api.InstanceStateChange, len(containers))
	for i, c := range containers {
		previousState, err := instanceState(c.State)
		if err != nil {
			return nil, fmt.Errorf("determining previous state for instance %s: %w", c.ID, err)
		}
		if err := e.cli.ContainerStart(ctx, c.ID, container.StartOptions{}); err != nil {
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
		changes[i] = api.InstanceStateChange{
			InstanceID:    c.ID,
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
	changes := make([]api.InstanceStateChange, len(containers))
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
		changes[i] = api.InstanceStateChange{
			InstanceID:    c.ID,
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
			InstanceID:    c.ID,
			PreviousState: previousState,
			CurrentState:  api.InstanceStateTerminated,
		}
	}
	return changes, nil
}

func (e *Executor) pullImage(ctx context.Context, imageName string) error {
	api.Logger(ctx).Debug("pulling image", slog.String("name", imageName))
	pullProgress, err := e.cli.ImagePull(ctx, imageName, image.PullOptions{})
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

func (e *Executor) findContainers(ctx context.Context, instanceIDs []string) ([]*types.ContainerJSON, error) {
	var containers []*types.ContainerJSON
	// Validate all the instances first
	for _, id := range instanceIDs {
		info, err := e.cli.ContainerInspect(ctx, id)
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
		InstanceID:     info.ID,
		ImageID:        imageID,
		InstanceState:  state,
		PrivateDNSName: dnsName,
		InstanceType:   instanceType,
		Architecture:   awsArchFromDockerArch(image.Architecture),
		LaunchTime:     created,
	}, nil
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

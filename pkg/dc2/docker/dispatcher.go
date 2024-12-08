package docker

import (
	"context"
	"encoding/xml"
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

type Dispatcher struct {
	cli *client.Client
}

func NewDispatcher() (*Dispatcher, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating Docker client: %w", err)
	}

	pingContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(pingContext); err != nil {
		return nil, fmt.Errorf("pinging Docker daemon: %w", err)
	}
	return &Dispatcher{
		cli: cli,
	}, nil
}

func (d *Dispatcher) Exec(ctx context.Context, req api.Request) (api.Response, error) {
	switch req.Action() {
	case api.ActionRunInstances:
		resp, err := d.execRunInstances(ctx, req.(*api.RunInstancesRequest))
		if err != nil {
			return nil, err
		}
		return resp, nil
	case api.ActionDescribeInstances:
		resp, err := d.execDescribeInstances(ctx, req.(*api.DescribeInstancesRequest))
		if err != nil {
			return nil, err
		}
		return resp, nil
	case api.ActionStopInstances:
		resp, err := d.execStopInstances(ctx, req.(*api.StopInstancesRequest))
		if err != nil {
			return nil, err
		}
		return resp, nil
	case api.ActionStartInstances:
		resp, err := d.execStartInstances(ctx, req.(*api.StartInstancesRequest))
		if err != nil {
			return nil, err
		}
		return resp, nil
	case api.ActionTerminateInstances:
		resp, err := d.execTerminateInstances(ctx, req.(*api.TerminateInstancesRequest))
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
	return nil, api.ErrWithCode(api.ErrorCodeInvalidAction, fmt.Errorf("unhandled action %d", req.Action()))
}

func (d *Dispatcher) execRunInstances(ctx context.Context, req *api.RunInstancesRequest) (*api.RunInstancesResponse, error) {
	log := slog.Default()
	log.Debug("pulling image", slog.String("name", req.ImageID))
	pullProgress, err := d.cli.ImagePull(ctx, req.ImageID, image.PullOptions{})
	if err != nil {
		return nil, fmt.Errorf("starting pull for %s: %w", req.ImageID, err)
	}
	if _, err := io.ReadAll(pullProgress); err != nil {
		pullProgress.Close()
		return nil, fmt.Errorf("pulling %s: %w", req.ImageID, err)
	}
	if err := pullProgress.Close(); err != nil {
		return nil, fmt.Errorf("finalizing pull for %s: %w", req.ImageID, err)
	}
	var instances []api.Instance
	for i := 0; i < req.MaxCount; i++ {
		containerConfig := &container.Config{
			Image: req.ImageID,
			Labels: map[string]string{
				LabelDC2Enabled:      "true",
				LabelDC2InstanceType: req.InstanceType,
				LabelDC2ImageID:      req.ImageID,
				LabelDC2KeyName:      req.KeyName,
			},
		}
		hostConfig := &container.HostConfig{}
		networkingConfig := &network.NetworkingConfig{}
		cont, err := d.cli.ContainerCreate(ctx, containerConfig, hostConfig, networkingConfig, nil, "")
		if err != nil {
			return nil, fmt.Errorf("creating container: %w", err)
		}
		if err := d.cli.ContainerStart(ctx, cont.ID, container.StartOptions{}); err != nil {
			return nil, fmt.Errorf("starting container: %w", err)
		}
		info, err := d.cli.ContainerInspect(ctx, cont.ID)
		if err != nil {
			return nil, fmt.Errorf("inspecting container: %w", err)
		}
		ins, err := instanceFromContainerJSON(ctx, d.cli, info)
		if err != nil {
			return nil, err
		}
		instances = append(instances, ins)
	}
	return &api.RunInstancesResponse{
		XMLNamespace: "http://ec2.amazonaws.com/doc/2016-11-15/",
		InstancesSet: instances,
	}, nil
}

func (d *Dispatcher) execStopInstances(ctx context.Context, req *api.StopInstancesRequest) (*api.StopInstancesResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	containers, err := d.findContainers(ctx, req.InstanceIDs)
	if err != nil {
		return nil, err
	}
	var timeout *int
	if req.Force {
		zero := 0
		timeout = &zero
	}
	var instances []api.InstanceStateChange
	for _, c := range containers {
		previousState, err := instanceState(c.State)
		if err != nil {
			return nil, fmt.Errorf("determining previous state for instance %s: %w", c.ID, err)
		}
		if err := d.cli.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: timeout}); err != nil {
			return nil, fmt.Errorf("stopping instance %s: %w", c.ID, err)
		}
		info, err := d.cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("inspecting container %s: %w", c.ID, err)
		}
		currentState, err := instanceState(info.State)
		if err != nil {
			return nil, fmt.Errorf("determining current state for instance %s: %w", c.ID, err)
		}
		instances = append(instances, api.InstanceStateChange{
			InstanceID:    c.ID,
			PreviousState: previousState,
			CurrentState:  currentState,
		})
	}
	return &api.StopInstancesResponse{
		XMLNamespace:      "http://ec2.amazonaws.com/doc/2016-11-15/",
		StoppingInstances: instances,
	}, nil
}

func (d *Dispatcher) execStartInstances(ctx context.Context, req *api.StartInstancesRequest) (*api.StartInstancesResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	containers, err := d.findContainers(ctx, req.InstanceIDs)
	if err != nil {
		return nil, err
	}
	var instances []api.InstanceStateChange
	for _, c := range containers {
		previousState, err := instanceState(c.State)
		if err != nil {
			return nil, fmt.Errorf("determining previous state for instance %s: %w", c.ID, err)
		}
		if err := d.cli.ContainerStart(ctx, c.ID, container.StartOptions{}); err != nil {
			return nil, fmt.Errorf("stopping instance %s: %w", c.ID, err)
		}
		info, err := d.cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("inspecting container %s: %w", c.ID, err)
		}
		currentState, err := instanceState(info.State)
		if err != nil {
			return nil, fmt.Errorf("determining current state for instance %s: %w", c.ID, err)
		}
		instances = append(instances, api.InstanceStateChange{
			InstanceID:    c.ID,
			PreviousState: previousState,
			CurrentState:  currentState,
		})
	}

	return &api.StartInstancesResponse{
		XMLNamespace:      "http://ec2.amazonaws.com/doc/2016-11-15/",
		StartingInstances: instances,
	}, nil
}

func (d *Dispatcher) execDescribeInstances(ctx context.Context, req *api.DescribeInstancesRequest) (*api.DescribeInstancesResponse, error) {
	var instances []api.Instance
	for _, id := range req.InstanceIDs {
		info, err := d.cli.ContainerInspect(ctx, id)
		if err != nil {
			// Specifying non-existing IDs is not an error
			continue
		}
		if !isDc2Container(info) {
			continue
		}
		ins, err := instanceFromContainerJSON(ctx, d.cli, info)
		if err != nil {
			return nil, err
		}
		instances = append(instances, ins)
	}
	var reservations []api.Reservation
	if len(instances) > 0 {
		reservations = append(reservations, api.Reservation{InstancesSet: instances})
	}
	return &api.DescribeInstancesResponse{
		XMLNamespace:   "http://ec2.amazonaws.com/doc/2016-11-15/",
		ReservationSet: reservations,
	}, nil
}

func (d *Dispatcher) execTerminateInstances(ctx context.Context, req *api.TerminateInstancesRequest) (*api.TerminateInstancesResponse, error) {
	if req.DryRun {
		return nil, api.DryRunError()
	}
	containers, err := d.findContainers(ctx, req.InstanceIDs)
	if err != nil {
		return nil, err
	}
	var instances []api.InstanceStateChange
	for _, c := range containers {
		previousState, err := instanceState(c.State)
		if err != nil {
			return nil, fmt.Errorf("determining previous state for instance %s: %w", c.ID, err)
		}
		if c.State.Running {
			if err := d.cli.ContainerStop(ctx, c.ID, container.StopOptions{}); err != nil {
				return nil, fmt.Errorf("stopping instance %s: %w", c.ID, err)
			}
		}
		if err := d.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{}); err != nil {
			return nil, fmt.Errorf("removing instance %s: %w", c.ID, err)
		}

		instances = append(instances, api.InstanceStateChange{
			InstanceID:    c.ID,
			PreviousState: previousState,
			CurrentState:  api.InstanceStateTerminated,
		})
	}
	return &api.TerminateInstancesResponse{
		XMLName:              xml.Name{Local: "TerminateInstancesResponse"},
		XMLNamespace:         "http://ec2.amazonaws.com/doc/2016-11-15/",
		RequestID:            "12345678-1234-1234-1234-123456789012",
		TerminatingInstances: instances,
	}, nil
}

func (d *Dispatcher) findContainers(ctx context.Context, instanceIDs []string) ([]*types.ContainerJSON, error) {
	var containers []*types.ContainerJSON
	// Validate all the instances first
	for _, id := range instanceIDs {
		info, err := d.cli.ContainerInspect(ctx, id)
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

func instanceFromContainerJSON(ctx context.Context, cli *client.Client, c types.ContainerJSON) (api.Instance, error) {
	created, err := time.Parse(time.RFC3339Nano, c.Created)
	if err != nil {
		return api.Instance{}, fmt.Errorf("parsing container creation time: %w", err)
	}
	labels := c.Config.Labels
	image, _, err := cli.ImageInspectWithRaw(ctx, c.Image)
	if err != nil {
		return api.Instance{}, fmt.Errorf("inspecting image: %w", err)
	}
	imageID := labels[LabelDC2ImageID]
	state, err := instanceState(c.State)
	if err != nil {
		return api.Instance{}, fmt.Errorf("instance state: %w", err)
	}
	instanceType := labels[LabelDC2InstanceType]
	keyName := labels[LabelDC2KeyName]
	// First character in c.Name is /
	dnsName := c.Name[1:]
	return api.Instance{
		InstanceID:     c.ID,
		ImageID:        imageID,
		InstanceState:  state,
		PrivateDNSName: dnsName,
		KeyName:        keyName,
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

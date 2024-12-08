package dc2_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/fiam/dc2/pkg/dc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	imageName = "dc2"
)

type TestEnvironment struct {
	Endpoint string
	Client   *ec2.Client
}

func runTestInContainer() bool {
	return false
}

func buildImage(t *testing.T) {
	cmd := exec.Command("make", "image")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = "../"
	err := cmd.Run()
	require.NoError(t, err)
}

func randomTCPPort(t *testing.T) int {
	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	defer listener.Close()
	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port
}

func awsCredentials(_ context.Context) (aws.Credentials, error) {
	return aws.Credentials{
		AccessKeyID:     "fakeKey",
		SecretAccessKey: "fakeSecret",
		Source:          "github.com/fiam/dc2",
	}, nil
}

func testWithServer(t *testing.T, testFunc func(t *testing.T, ctx context.Context, e *TestEnvironment)) {
	const containerPort = 8080
	port := randomTCPPort(t)

	ctx := context.TODO()

	if runTestInContainer() {
		buildImage(t)
		dockerCmd := exec.Command("docker", "run", "--rm",
			"-p", fmt.Sprintf("%d:%d", port, containerPort),
			"-e", fmt.Sprintf("ADDR=0.0.0.0:%d", containerPort),
			"-e", "LOG_LEVEL=debug",
			"-v", "/var/run/docker.sock:/var/run/docker.sock",
			imageName)
		dockerCmd.Stdout = os.Stdout
		dockerCmd.Stderr = os.Stderr
		err := dockerCmd.Start()
		require.NoError(t, err)

		t.Cleanup(func() {
			t.Logf("stopping server")
			require.NoError(t, dockerCmd.Process.Signal(syscall.SIGTERM))
			_ = dockerCmd.Wait()
		})
	} else {
		srv, err := dc2.NewServer(":" + strconv.Itoa(port))
		require.NoError(t, err)
		go func() {
			err := srv.ListenAndServe()
			if err != http.ErrServerClosed {
				require.NoError(t, err)
			}
		}()
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			require.NoError(t, srv.Shutdown(ctx))
		})
	}

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(aws.CredentialsProviderFunc(awsCredentials)))
	require.NoError(t, err, "could not load AWS config")

	client := ec2.NewFromConfig(cfg, func(o *ec2.Options) {
		o.BaseEndpoint = aws.String(fmt.Sprintf("http://localhost:%d", port))
	})
	t.Logf("starting test")
	testFunc(t, ctx, &TestEnvironment{
		Client: client,
	})
}

func TestStartStopInstances(t *testing.T) {
	t.Parallel()

	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		createInstance := func(t *testing.T) string {
			runInstancesOutput, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
				ImageId:      aws.String("nginx"),
				InstanceType: "my-type",
				MinCount:     aws.Int32(1),
				MaxCount:     aws.Int32(1),
			})
			require.NoError(t, err)

			instanceID := *runInstancesOutput.Instances[0].InstanceId

			t.Cleanup(func() {
				_, err := e.Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
					InstanceIds: []string{instanceID},
				})
				require.NoError(t, err)
			})

			return instanceID
		}

		t.Run("stop", func(t *testing.T) {
			t.Parallel()

			instanceID := createInstance(t)

			stopInstancesOutput, err := e.Client.StopInstances(ctx, &ec2.StopInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, err)

			require.Len(t, stopInstancesOutput.StoppingInstances, 1)
			assert.Equal(t, instanceID, *stopInstancesOutput.StoppingInstances[0].InstanceId)

			describeInstancesOutput, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, err)
			require.Len(t, describeInstancesOutput.Reservations, 1)
			require.Len(t, describeInstancesOutput.Reservations[0].Instances, 1)
			assert.Equal(t, types.InstanceStateNameStopped, describeInstancesOutput.Reservations[0].Instances[0].State.Name)
		})

		t.Run("stop with dry run", func(t *testing.T) {
			t.Parallel()

			instanceID := createInstance(t)

			stopInstancesOutput, err := e.Client.StopInstances(ctx, &ec2.StopInstancesInput{
				InstanceIds: []string{instanceID},
				DryRun:      aws.Bool(true),
			})
			require.Nil(t, stopInstancesOutput)
			var apiErr smithy.APIError
			require.ErrorAs(t, err, &apiErr)

			describeInstancesOutput, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, err)
			require.Len(t, describeInstancesOutput.Reservations, 1)
			require.Len(t, describeInstancesOutput.Reservations[0].Instances, 1)
			assert.Equal(t, types.InstanceStateNameRunning, describeInstancesOutput.Reservations[0].Instances[0].State.Name)
		})

		t.Run("stop and restart", func(t *testing.T) {
			t.Parallel()

			instanceID := createInstance(t)

			stopInstancesOutput, err := e.Client.StopInstances(ctx, &ec2.StopInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, err)

			require.Len(t, stopInstancesOutput.StoppingInstances, 1)
			assert.Equal(t, instanceID, *stopInstancesOutput.StoppingInstances[0].InstanceId)

			describeInstancesOutput, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, err)
			require.Len(t, describeInstancesOutput.Reservations, 1)
			require.Len(t, describeInstancesOutput.Reservations[0].Instances, 1)
			assert.Equal(t, types.InstanceStateNameStopped, describeInstancesOutput.Reservations[0].Instances[0].State.Name)

			startInstancesOutput, err := e.Client.StartInstances(ctx, &ec2.StartInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, err)
			require.Len(t, startInstancesOutput.StartingInstances, 1)
			assert.Equal(t, instanceID, *startInstancesOutput.StartingInstances[0].InstanceId)

			describeInstancesOutput2, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, err)
			require.Len(t, describeInstancesOutput2.Reservations, 1)
			require.Len(t, describeInstancesOutput2.Reservations[0].Instances, 1)
			assert.Equal(t, types.InstanceStateNameRunning, describeInstancesOutput2.Reservations[0].Instances[0].State.Name)
		})
	})
}

func TestTerminateInstances(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		t.Run("non existing", func(t *testing.T) {
			t.Parallel()
			terminateInstancesOutput, err := e.Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{"does-not-exist"},
			})
			require.Nil(t, terminateInstancesOutput)
			require.Error(t, err)
			var apiErr smithy.APIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, "InvalidInstanceID.NotFound", apiErr.ErrorCode())
		})

		t.Run("non existing with container", func(t *testing.T) {
			t.Parallel()
			const imageName = "nginx"
			cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
			require.NoError(t, err)

			pullProgress, err := cli.ImagePull(ctx, imageName, image.PullOptions{})
			require.NoError(t, err)
			_, err = io.ReadAll(pullProgress)
			require.NoError(t, err)
			err = pullProgress.Close()
			require.NoError(t, err)
			containerConfig := &container.Config{
				Image: imageName,
			}
			hostConfig := &container.HostConfig{}
			networkingConfig := &network.NetworkingConfig{}
			cont, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, networkingConfig, nil, "")
			require.NoError(t, err)
			err = cli.ContainerStart(ctx, cont.ID, container.StartOptions{})
			require.NoError(t, err)

			t.Cleanup(func() {
				err := cli.ContainerRemove(ctx, cont.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
				require.NoError(t, err)
			})

			terminateInstancesOutput, err := e.Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{cont.ID},
			})
			require.Nil(t, terminateInstancesOutput)
			require.Error(t, err)
			var apiErr smithy.APIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, "InvalidInstanceID.NotFound", apiErr.ErrorCode())
		})

		t.Run("terminate with dry run", func(t *testing.T) {
			t.Parallel()

			runInstancesOutput, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
				ImageId:      aws.String("nginx"),
				InstanceType: "my-type",
				MinCount:     aws.Int32(1),
				MaxCount:     aws.Int32(1),
			})
			require.NoError(t, err)

			terminateInstancesOutput, err := e.Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{*runInstancesOutput.Instances[0].InstanceId},
				DryRun:      aws.Bool(true),
			})
			require.Nil(t, terminateInstancesOutput)
			var apiErr smithy.APIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, "DryRunOperation", apiErr.ErrorCode())

			_, err = e.Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{*runInstancesOutput.Instances[0].InstanceId},
			})
			require.NoError(t, err)
		})
	})
}

func TestRunInstance(t *testing.T) {
	t.Parallel()

	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		const (
			imageID      = "nginx"
			instanceType = types.InstanceTypeT2Micro
			keyName      = "my-key"
		)
		// Create and start the instance
		runInstancesOutput, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String(imageID),
			InstanceType: instanceType,
			MinCount:     aws.Int32(1),
			MaxCount:     aws.Int32(1),
			KeyName:      aws.String(keyName),
		})
		require.NoError(t, err)

		require.Len(t, runInstancesOutput.Instances, 1)
		instance := runInstancesOutput.Instances[0]
		require.NotNil(t, instance.InstanceId)
		assert.NotEmpty(t, instance.InstanceId)
		require.NotNil(t, instance.ImageId)
		assert.Equal(t, imageID, *instance.ImageId)
		assert.Equal(t, instanceType, instance.InstanceType)
		require.NotNil(t, instance.KeyName)
		assert.Equal(t, keyName, *instance.KeyName)
		require.NotNil(t, instance.LaunchTime)
		assert.WithinDuration(t, time.Now(), *instance.LaunchTime, 5*time.Second)
		require.NotNil(t, instance.State)
		assert.Equal(t, types.InstanceStateNameRunning, instance.State.Name)
		expectedArch := runtime.GOARCH
		if expectedArch == "amd64" {
			expectedArch = "x86_64"
		}
		assert.Equal(t, types.ArchitectureValues(expectedArch), instance.Architecture)

		describeInstancesOutput, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{*instance.InstanceId},
		})
		require.NoError(t, err)
		require.Len(t, describeInstancesOutput.Reservations, 1)
		require.Len(t, describeInstancesOutput.Reservations[0].Instances, 1)
		assert.Equal(t, instance, describeInstancesOutput.Reservations[0].Instances[0])

		terminateInstancesOutput, err := e.Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
			InstanceIds: []string{*instance.InstanceId},
		})
		require.NoError(t, err)
		require.Len(t, terminateInstancesOutput.TerminatingInstances, 1)
		terminatingInstance := terminateInstancesOutput.TerminatingInstances[0]
		require.NotNil(t, terminatingInstance.InstanceId)
		assert.Equal(t, *instance.InstanceId, *terminatingInstance.InstanceId)
		require.NotNil(t, terminatingInstance.PreviousState)
		assert.Equal(t, types.InstanceStateNameRunning, terminatingInstance.PreviousState.Name)
		require.NotNil(t, terminatingInstance.CurrentState)
		assert.Equal(t, types.InstanceStateNameTerminated, terminatingInstance.CurrentState.Name)

		// Some time later, DescribeInstances should return that the instance doesn't exist
		assert.Eventually(t, func() bool {
			describeInstancesOutput, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				InstanceIds: []string{*instance.InstanceId},
			})

			if err != nil {
				return false
			}

			return len(describeInstancesOutput.Reservations) == 0
		}, 10*time.Second, 1*time.Second)
	})
}

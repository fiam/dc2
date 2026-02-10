package dc2_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2"
)

const (
	imageName = "dc2"
)

type TestEnvironment struct {
	Endpoint          string
	Region            string
	Client            *ec2.Client
	AutoScalingClient *autoscaling.Client
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
		logLevel := slog.LevelInfo
		if level := os.Getenv("LOG_LEVEL"); level != "" {
			ll, err := parseLogLevel(level)
			if err != nil {
				t.Fatal(err)
			}
			logLevel = ll
		}

		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
		srv, err := dc2.NewServer(":"+strconv.Itoa(port), dc2.WithLogger(logger))
		require.NoError(t, err)
		go func() {
			err := srv.ListenAndServe()
			if err != http.ErrServerClosed {
				require.NoError(t, err)
			}
		}()
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
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
	autoScalingClient := autoscaling.NewFromConfig(cfg, func(o *autoscaling.Options) {
		o.BaseEndpoint = aws.String(fmt.Sprintf("http://localhost:%d", port))
	})
	testFunc(t, ctx, &TestEnvironment{
		Client:            client,
		AutoScalingClient: autoScalingClient,
		Region:            "us-east-1",
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
		require.NotNil(t, instance.Placement)
		require.NotNil(t, instance.Placement.AvailabilityZone)
		assert.Equal(t, e.Region+"a", *instance.Placement.AvailabilityZone)

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

func TestInstanceTags(t *testing.T) {
	t.Parallel()

	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		const (
			tagName   = "foo"
			tagValue  = "bar"
			tagValue2 = "baz"
		)
		runInstancesOutput, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx"),
			InstanceType: types.InstanceTypeT2Micro,
			MinCount:     aws.Int32(1),
			MaxCount:     aws.Int32(2),
			TagSpecifications: []types.TagSpecification{
				{
					ResourceType: types.ResourceTypeInstance,
					Tags: []types.Tag{
						{
							Key:   aws.String(tagName),
							Value: aws.String(tagValue),
						},
					},
				},
			},
		})

		require.NoError(t, err)

		require.Len(t, runInstancesOutput.Instances, 2)
		instance := runInstancesOutput.Instances[0]
		require.NotNil(t, instance.InstanceId)
		assert.NotEmpty(t, instance.InstanceId)

		require.Len(t, instance.Tags, 1)
		require.NotNil(t, instance.Tags[0].Key)
		assert.Equal(t, tagName, *instance.Tags[0].Key)
		require.NotNil(t, instance.Tags[0].Value)
		assert.Equal(t, tagValue, *instance.Tags[0].Value)

		// Retrieve both instances by tag
		describeInstancesByTagOutput1, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			Filters: []types.Filter{
				{
					Name:   aws.String("tag:" + tagName),
					Values: []string{tagValue},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, describeInstancesByTagOutput1.Reservations, 1)
		require.Len(t, describeInstancesByTagOutput1.Reservations[0].Instances, 2)

		// Now change the tag value for one of the instances
		_, err = e.Client.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{*instance.InstanceId},
			Tags: []types.Tag{
				{
					Key:   aws.String(tagName),
					Value: aws.String(tagValue2),
				},
			},
		})
		require.NoError(t, err)

		// Now this should return only the second instance
		describeInstancesByTagOutput2, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			Filters: []types.Filter{
				{
					Name:   aws.String("tag:" + tagName),
					Values: []string{tagValue},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, describeInstancesByTagOutput2.Reservations, 1)
		require.Len(t, describeInstancesByTagOutput2.Reservations[0].Instances, 1)
		assert.NotEqual(t, *instance.InstanceId, *describeInstancesByTagOutput2.Reservations[0].Instances[0].InstanceId)

		// Return all instances with the tag set to any of the two values
		describeInstancesByTagOutput3, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			Filters: []types.Filter{
				{
					Name:   aws.String("tag:" + tagName),
					Values: []string{tagValue, tagValue2},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, describeInstancesByTagOutput3.Reservations, 1)
		require.Len(t, describeInstancesByTagOutput3.Reservations[0].Instances, 2)

		// Return all instances with the tag set to any value
		describeInstancesByTagOutput4, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			Filters: []types.Filter{
				{
					Name:   aws.String("tag-key"),
					Values: []string{tagName},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, describeInstancesByTagOutput4.Reservations, 1)
		require.Len(t, describeInstancesByTagOutput4.Reservations[0].Instances, 2)

		// Remove the tag from the first instance
		_, err = e.Client.DeleteTags(ctx, &ec2.DeleteTagsInput{
			Resources: []string{*instance.InstanceId},
			Tags: []types.Tag{
				{
					Key: aws.String(tagName),
				},
			},
		})
		require.NoError(t, err)

		// Now this should return only the second instance
		describeInstancesByTagOutput5, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			Filters: []types.Filter{
				{
					Name:   aws.String("tag-key"),
					Values: []string{tagName},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, describeInstancesByTagOutput5.Reservations, 1)
		require.Len(t, describeInstancesByTagOutput5.Reservations[0].Instances, 1)
		assert.NotEqual(t, *instance.InstanceId, *describeInstancesByTagOutput5.Reservations[0].Instances[0].InstanceId)

		secondInstance := describeInstancesByTagOutput5.Reservations[0].Instances[0]

		// This should not remove the tag because it doesn't match the value
		_, err = e.Client.DeleteTags(ctx, &ec2.DeleteTagsInput{
			Resources: []string{*secondInstance.InstanceId},
			Tags: []types.Tag{
				{
					Key:   aws.String(tagName),
					Value: aws.String(tagValue2),
				},
			},
		})
		require.NoError(t, err)

		// This should still return the second instance
		describeInstancesByTagOutput6, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			Filters: []types.Filter{
				{
					Name:   aws.String("tag-key"),
					Values: []string{tagName},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, describeInstancesByTagOutput6.Reservations, 1)
		require.Len(t, describeInstancesByTagOutput6.Reservations[0].Instances, 1)
		assert.Equal(t, *secondInstance.InstanceId, *describeInstancesByTagOutput6.Reservations[0].Instances[0].InstanceId)

		// Now the value will match and the tag should be removed
		_, err = e.Client.DeleteTags(ctx, &ec2.DeleteTagsInput{
			Resources: []string{*secondInstance.InstanceId},
			Tags: []types.Tag{
				{
					Key:   aws.String(tagName),
					Value: aws.String(tagValue),
				},
			},
		})
		require.NoError(t, err)

		// This should now be empty
		describeInstancesByTagOutput7, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			Filters: []types.Filter{
				{
					Name:   aws.String("tag-key"),
					Values: []string{tagName},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, describeInstancesByTagOutput7.Reservations, 0)
	})
}

func parseLogLevel(level string) (slog.Level, error) {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q", level)
}

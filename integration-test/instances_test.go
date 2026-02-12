package dc2_test

import (
	"context"
	"encoding/base64"
	"errors"
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
	"sync"
	"sync/atomic"
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
	imageName            = "dc2"
	testContainerLabel   = "dc2-test-suite=true"
	testModeEnvVar       = "DC2_TEST_MODE"
	testModeHost         = "host"
	testModeContainer    = "container"
	imdsBaseURL          = "http://169.254.169.254"
	imdsTokenHeader      = "X-aws-ec2-metadata-token"
	imdsTokenTTLField    = "X-aws-ec2-metadata-token-ttl-seconds"
	serverStartupTimeout = 60 * time.Second
)

var (
	integrationTestSemaphore = make(chan struct{}, integrationTestConcurrency())
	buildImageOnce           sync.Once
	buildImageErr            error
	testContainerNameCounter uint64
)

func integrationTestConcurrency() int {
	parallelism := 1
	if raw := os.Getenv("DC2_TEST_PARALLELISM"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			parallelism = n
		}
	}
	return parallelism
}

type TestEnvironment struct {
	Endpoint          string
	Region            string
	DockerHost        string
	Client            *ec2.Client
	AutoScalingClient *autoscaling.Client
}

func runTestInContainer() bool {
	return testMode() == testModeContainer
}

func testMode() string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(testModeEnvVar)))
	switch mode {
	case "":
		return testModeHost
	case testModeContainer:
		return mode
	default:
		return testModeHost
	}
}

func dockerCommandContext(ctx context.Context, dockerHost string, args ...string) *exec.Cmd {
	argv := make([]string, 0, len(args)+2)
	if dockerHost != "" {
		argv = append(argv, "--host", dockerHost)
	}
	argv = append(argv, args...)
	return exec.CommandContext(ctx, "docker", argv...)
}

func dockerCommand(dockerHost string, args ...string) *exec.Cmd {
	argv := make([]string, 0, len(args)+2)
	if dockerHost != "" {
		argv = append(argv, "--host", dockerHost)
	}
	argv = append(argv, args...)
	return exec.Command("docker", argv...)
}

func cleanupAPICtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func waitForProcessExit(t *testing.T, cmd *exec.Cmd, timeout time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		if err == nil {
			return
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// Teardown-initiated container stops can surface as non-zero docker run exits.
			t.Logf("docker process exited after stop with code %d", exitErr.ExitCode())
			return
		}
		require.NoError(t, err)
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		t.Fatalf("process did not exit within %s", timeout)
	}
}

func stopContainerAndAssertStopped(t *testing.T, dockerHost string, containerRef string) {
	t.Helper()

	stopOut, stopErr := dockerCommand(dockerHost, "stop", "--time", "15", containerRef).CombinedOutput()
	if stopErr != nil {
		stopOutput := string(stopOut)
		if !strings.Contains(stopOutput, "No such container") && !strings.Contains(stopOutput, "No such object") {
			require.NoError(t, stopErr, "stopping container %s output=%s", containerRef, strings.TrimSpace(stopOutput))
		}
	}
}

func waitForDC2API(t *testing.T, endpoint string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{
		Timeout: time.Second,
	}
	body := "Action=DescribeInstances&Version=2016-11-15"
	var lastErr error
	var lastStatus int
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
		if err != nil {
			t.Fatalf("creating readiness request: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err == nil {
			lastStatus = resp.StatusCode
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		} else {
			lastErr = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("dc2 endpoint %s not ready: %v", endpoint, lastErr)
	}
	t.Fatalf("dc2 endpoint %s not ready: last status=%d", endpoint, lastStatus)
}

func buildImage() error {
	cmd := exec.Command("make", "image")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = "../"
	return cmd.Run()
}

func ensureTestImageBuilt(t *testing.T) {
	t.Helper()
	buildImageOnce.Do(func() {
		buildImageErr = buildImage()
	})
	require.NoError(t, buildImageErr)
}

func uniqueTestContainerName(prefix string) string {
	seq := atomic.AddUint64(&testContainerNameCounter, 1)
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), seq)
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
	testWithServerWithOptions(t, nil, testFunc)
}

func testWithServerWithOptions(t *testing.T, serverOpts []dc2.Option, testFunc func(t *testing.T, ctx context.Context, e *TestEnvironment)) {
	integrationTestSemaphore <- struct{}{}
	t.Cleanup(func() {
		<-integrationTestSemaphore
	})

	const containerPort = 8080
	port := randomTCPPort(t)
	dockerHost := ""

	ctx := t.Context()

	if runTestInContainer() {
		ensureTestImageBuilt(t)
		serverName := uniqueTestContainerName("dc2-test-server-host")
		dockerCmd := exec.Command("docker", "run", "--rm",
			"--name", serverName,
			"--label", testContainerLabel,
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
			t.Logf("stopping server container %s", serverName)
			stopContainerAndAssertStopped(t, "", serverName)
			waitForProcessExit(t, dockerCmd, 20*time.Second)
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
		opts := append([]dc2.Option{}, serverOpts...)
		opts = append(opts, dc2.WithLogger(logger))
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		tcpAddr, ok := listener.Addr().(*net.TCPAddr)
		require.True(t, ok)
		port = tcpAddr.Port

		srv, err := dc2.NewServer("127.0.0.1:0", opts...)
		require.NoError(t, err)
		go func() {
			err := srv.Serve(listener)
			if err != http.ErrServerClosed {
				require.NoError(t, err)
			}
		}()
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			require.NoError(t, srv.Shutdown(ctx))
		})
	}
	waitForDC2API(t, fmt.Sprintf("http://localhost:%d/", port), serverStartupTimeout)

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
		DockerHost:        dockerHost,
		Client:            client,
		AutoScalingClient: autoScalingClient,
		Region:            "us-east-1",
	})
}

func curlIMDS(ctx context.Context, dockerHost string, containerID string, path string, token string) ([]byte, error) {
	args := []string{"exec", containerID, "curl", "-fsS"}
	if token != "" {
		args = append(args, "-H", fmt.Sprintf("%s: %s", imdsTokenHeader, token))
	}
	args = append(args, imdsBaseURL+path)
	return dockerCommandContext(ctx, dockerHost, args...).CombinedOutput()
}

func fetchIMDSToken(t *testing.T, ctx context.Context, dockerHost string, containerID string) string {
	t.Helper()
	const ttlSeconds = 60
	out, err := dockerCommandContext(
		ctx,
		dockerHost,
		"exec",
		containerID,
		"curl",
		"-fsS",
		"-X",
		"PUT",
		"-H",
		fmt.Sprintf("%s: %d", imdsTokenTTLField, ttlSeconds),
		imdsBaseURL+"/latest/api/token",
	).CombinedOutput()
	require.NoError(t, err, "curl token output: %s", string(out))
	token := strings.TrimSpace(string(out))
	require.NotEmpty(t, token)
	return token
}

func TestInstanceUserDataViaIMDS(t *testing.T) {
	t.Parallel()
	userData := "#!/bin/sh\necho from-imds\n"
	encodedUserData := base64.StdEncoding.EncodeToString([]byte(userData))

	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		runResp, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx"),
			InstanceType: "my-type",
			UserData:     aws.String(encodedUserData),
			MinCount:     aws.Int32(1),
			MaxCount:     aws.Int32(1),
		})
		require.NoError(t, err)
		require.Len(t, runResp.Instances, 1)
		require.NotNil(t, runResp.Instances[0].InstanceId)
		instanceID := *runResp.Instances[0].InstanceId
		containerID := strings.TrimPrefix(instanceID, "i-")

		t.Cleanup(func() {
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, err := e.Client.TerminateInstances(cleanupCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, err)
		})

		token := fetchIMDSToken(t, ctx, e.DockerHost, containerID)

		instanceIDOutput, err := curlIMDS(ctx, e.DockerHost, containerID, "/latest/meta-data/instance-id", token)
		require.NoError(t, err, "curl instance-id output: %s", string(instanceIDOutput))
		assert.Equal(t, instanceID, strings.TrimSpace(string(instanceIDOutput)))

		userDataOutput, err := curlIMDS(ctx, e.DockerHost, containerID, "/latest/user-data", token)
		require.NoError(t, err, "curl user-data output: %s", string(userDataOutput))
		assert.Equal(t, userData, string(userDataOutput))
	})
}

func TestInstanceMetadataRequiresToken(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		runResp, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx"),
			InstanceType: "my-type",
			MinCount:     aws.Int32(1),
			MaxCount:     aws.Int32(1),
		})
		require.NoError(t, err)
		require.Len(t, runResp.Instances, 1)
		require.NotNil(t, runResp.Instances[0].InstanceId)

		instanceID := *runResp.Instances[0].InstanceId
		containerID := strings.TrimPrefix(instanceID, "i-")

		t.Cleanup(func() {
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, err := e.Client.TerminateInstances(cleanupCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, err)
		})

		missingTokenOutput, err := curlIMDS(ctx, e.DockerHost, containerID, "/latest/meta-data/instance-id", "")
		require.Error(t, err)
		assert.Contains(t, string(missingTokenOutput), "401")

		invalidTokenOutput, err := curlIMDS(ctx, e.DockerHost, containerID, "/latest/meta-data/instance-id", "invalid-token")
		require.Error(t, err)
		assert.Contains(t, string(invalidTokenOutput), "401")

		missingTTLTokenOutput, err := dockerCommandContext(
			ctx,
			e.DockerHost,
			"exec",
			containerID,
			"curl",
			"-fsS",
			"-X",
			"PUT",
			imdsBaseURL+"/latest/api/token",
		).CombinedOutput()
		require.Error(t, err)
		assert.Contains(t, string(missingTTLTokenOutput), "400")

		token := fetchIMDSToken(t, ctx, e.DockerHost, containerID)
		instanceIDOutput, err := curlIMDS(ctx, e.DockerHost, containerID, "/latest/meta-data/instance-id", token)
		require.NoError(t, err, "curl instance-id output: %s", string(instanceIDOutput))
		assert.Equal(t, instanceID, strings.TrimSpace(string(instanceIDOutput)))
	})
}

func TestInstanceTagsViaIMDS(t *testing.T) {
	t.Parallel()
	const (
		tagName   = "name"
		tagValue  = "first"
		tagValue2 = "updated"
	)

	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		runResp, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx"),
			InstanceType: "my-type",
			MinCount:     aws.Int32(1),
			MaxCount:     aws.Int32(1),
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
		require.Len(t, runResp.Instances, 1)
		require.NotNil(t, runResp.Instances[0].InstanceId)

		instanceID := *runResp.Instances[0].InstanceId
		containerID := strings.TrimPrefix(instanceID, "i-")

		t.Cleanup(func() {
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, err := e.Client.TerminateInstances(cleanupCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, err)
		})

		token := fetchIMDSToken(t, ctx, e.DockerHost, containerID)
		keysOutput, err := curlIMDS(ctx, e.DockerHost, containerID, "/latest/meta-data/tags/instance", token)
		require.NoError(t, err, "IMDS tag keys output: %s", string(keysOutput))
		assert.Equal(t, tagName, strings.TrimSpace(string(keysOutput)))

		valueOutput, err := curlIMDS(ctx, e.DockerHost, containerID, "/latest/meta-data/tags/instance/"+tagName, token)
		require.NoError(t, err, "IMDS tag value output: %s", string(valueOutput))
		assert.Equal(t, tagValue, strings.TrimSpace(string(valueOutput)))

		_, err = e.Client.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{instanceID},
			Tags: []types.Tag{
				{
					Key:   aws.String(tagName),
					Value: aws.String(tagValue2),
				},
			},
		})
		require.NoError(t, err)

		updatedOutput, err := curlIMDS(ctx, e.DockerHost, containerID, "/latest/meta-data/tags/instance/"+tagName, token)
		require.NoError(t, err, "updated IMDS tag value output: %s", string(updatedOutput))
		assert.Equal(t, tagValue2, strings.TrimSpace(string(updatedOutput)))

		_, err = e.Client.DeleteTags(ctx, &ec2.DeleteTagsInput{
			Resources: []string{instanceID},
			Tags: []types.Tag{
				{
					Key: aws.String(tagName),
				},
			},
		})
		require.NoError(t, err)

		deletedValueOutput, err := curlIMDS(ctx, e.DockerHost, containerID, "/latest/meta-data/tags/instance/"+tagName, token)
		require.Error(t, err)
		assert.Contains(t, string(deletedValueOutput), "404")
	})
}

func TestInstanceMetadataOptionsCanDisableIMDSAtRuntime(t *testing.T) {
	t.Parallel()
	userData := "#!/bin/sh\necho toggled-imds\n"
	encodedUserData := base64.StdEncoding.EncodeToString([]byte(userData))

	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		runResp, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx"),
			InstanceType: "my-type",
			UserData:     aws.String(encodedUserData),
			MinCount:     aws.Int32(1),
			MaxCount:     aws.Int32(1),
		})
		require.NoError(t, err)
		require.Len(t, runResp.Instances, 1)
		require.NotNil(t, runResp.Instances[0].InstanceId)

		instanceID := *runResp.Instances[0].InstanceId
		containerID := strings.TrimPrefix(instanceID, "i-")

		t.Cleanup(func() {
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, err := e.Client.TerminateInstances(cleanupCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceID},
			})
			require.NoError(t, err)
		})

		token := fetchIMDSToken(t, ctx, e.DockerHost, containerID)

		userDataOutput, err := curlIMDS(ctx, e.DockerHost, containerID, "/latest/user-data", token)
		require.NoError(t, err, "baseline IMDS curl output: %s", string(userDataOutput))
		assert.Equal(t, userData, string(userDataOutput))

		disableOutput, err := e.Client.ModifyInstanceMetadataOptions(ctx, &ec2.ModifyInstanceMetadataOptionsInput{
			InstanceId:   aws.String(instanceID),
			HttpEndpoint: types.InstanceMetadataEndpointStateDisabled,
		})
		require.NoError(t, err)
		require.NotNil(t, disableOutput.InstanceMetadataOptions)
		assert.Equal(t, types.InstanceMetadataEndpointStateDisabled, disableOutput.InstanceMetadataOptions.HttpEndpoint)
		assert.Equal(t, types.InstanceMetadataOptionsStateApplied, disableOutput.InstanceMetadataOptions.State)

		disabledOutput, err := curlIMDS(ctx, e.DockerHost, containerID, "/latest/user-data", token)
		require.Error(t, err)
		assert.Contains(t, string(disabledOutput), "404")

		enableOutput, err := e.Client.ModifyInstanceMetadataOptions(ctx, &ec2.ModifyInstanceMetadataOptionsInput{
			InstanceId:   aws.String(instanceID),
			HttpEndpoint: types.InstanceMetadataEndpointStateEnabled,
		})
		require.NoError(t, err)
		require.NotNil(t, enableOutput.InstanceMetadataOptions)
		assert.Equal(t, types.InstanceMetadataEndpointStateEnabled, enableOutput.InstanceMetadataOptions.HttpEndpoint)
		assert.Equal(t, types.InstanceMetadataOptionsStateApplied, enableOutput.InstanceMetadataOptions.State)

		reenabledToken := fetchIMDSToken(t, ctx, e.DockerHost, containerID)
		reenabledOutput, err := curlIMDS(ctx, e.DockerHost, containerID, "/latest/user-data", reenabledToken)
		require.NoError(t, err, "re-enabled IMDS curl output: %s", string(reenabledOutput))
		assert.Equal(t, userData, string(reenabledOutput))
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
			dockerOpts := []client.Opt{
				client.FromEnv,
				client.WithAPIVersionNegotiation(),
			}
			if e.DockerHost != "" {
				dockerOpts = append(dockerOpts, client.WithHost(e.DockerHost))
			}
			cli, err := client.NewClientWithOpts(dockerOpts...)
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

func TestDescribeInstanceStatus(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		runInstancesOutput, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx"),
			InstanceType: "my-type",
			MinCount:     aws.Int32(1),
			MaxCount:     aws.Int32(1),
		})
		require.NoError(t, err)
		require.Len(t, runInstancesOutput.Instances, 1)
		require.NotNil(t, runInstancesOutput.Instances[0].InstanceId)
		instanceID := *runInstancesOutput.Instances[0].InstanceId

		t.Cleanup(func() {
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, err := e.Client.TerminateInstances(cleanupCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceID},
			})
			if err != nil {
				var apiErr smithy.APIError
				if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidInstanceID.NotFound" {
					require.NoError(t, err)
				}
			}
		})

		runningStatusOutput, err := e.Client.DescribeInstanceStatus(ctx, &ec2.DescribeInstanceStatusInput{
			InstanceIds: []string{instanceID},
		})
		require.NoError(t, err)
		require.Len(t, runningStatusOutput.InstanceStatuses, 1)
		runningStatus := runningStatusOutput.InstanceStatuses[0]
		require.NotNil(t, runningStatus.InstanceId)
		assert.Equal(t, instanceID, *runningStatus.InstanceId)
		require.NotNil(t, runningStatus.InstanceState)
		assert.Equal(t, types.InstanceStateNameRunning, runningStatus.InstanceState.Name)
		require.NotNil(t, runningStatus.InstanceStatus)
		assert.Equal(t, "ok", string(runningStatus.InstanceStatus.Status))
		require.NotNil(t, runningStatus.SystemStatus)
		assert.Equal(t, "ok", string(runningStatus.SystemStatus.Status))

		_, err = e.Client.StopInstances(ctx, &ec2.StopInstancesInput{
			InstanceIds: []string{instanceID},
		})
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			out, err := e.Client.DescribeInstanceStatus(ctx, &ec2.DescribeInstanceStatusInput{
				InstanceIds: []string{instanceID},
			})
			return err == nil && len(out.InstanceStatuses) == 0
		}, 10*time.Second, 250*time.Millisecond)

		require.Eventually(t, func() bool {
			out, err := e.Client.DescribeInstanceStatus(ctx, &ec2.DescribeInstanceStatusInput{
				InstanceIds:         []string{instanceID},
				IncludeAllInstances: aws.Bool(true),
			})
			if err != nil || len(out.InstanceStatuses) != 1 {
				return false
			}
			status := out.InstanceStatuses[0]
			if status.InstanceState == nil || status.InstanceState.Name != types.InstanceStateNameStopped {
				return false
			}
			return status.InstanceStatus != nil &&
				string(status.InstanceStatus.Status) == "not-applicable" &&
				status.SystemStatus != nil &&
				string(status.SystemStatus.Status) == "not-applicable"
		}, 10*time.Second, 250*time.Millisecond)
	})
}

func TestInstanceLifecycleTransitionReasons(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		runInstancesOutput, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx"),
			InstanceType: "my-type",
			MinCount:     aws.Int32(1),
			MaxCount:     aws.Int32(1),
		})
		require.NoError(t, err)
		require.Len(t, runInstancesOutput.Instances, 1)
		require.NotNil(t, runInstancesOutput.Instances[0].InstanceId)
		instanceID := *runInstancesOutput.Instances[0].InstanceId

		t.Cleanup(func() {
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, err := e.Client.TerminateInstances(cleanupCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceID},
			})
			if err != nil {
				var apiErr smithy.APIError
				if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidInstanceID.NotFound" {
					require.NoError(t, err)
				}
			}
		})

		_, err = e.Client.StopInstances(ctx, &ec2.StopInstancesInput{
			InstanceIds: []string{instanceID},
		})
		require.NoError(t, err)

		stoppedOutput, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		require.NoError(t, err)
		require.Len(t, stoppedOutput.Reservations, 1)
		require.Len(t, stoppedOutput.Reservations[0].Instances, 1)
		stoppedInstance := stoppedOutput.Reservations[0].Instances[0]
		require.NotNil(t, stoppedInstance.State)
		assert.Equal(t, types.InstanceStateNameStopped, stoppedInstance.State.Name)
		require.NotNil(t, stoppedInstance.StateTransitionReason)
		assert.Contains(t, *stoppedInstance.StateTransitionReason, "User initiated (")
		assert.Contains(t, *stoppedInstance.StateTransitionReason, "GMT)")
		assert.Nil(t, stoppedInstance.StateReason)

		_, err = e.Client.StartInstances(ctx, &ec2.StartInstancesInput{
			InstanceIds: []string{instanceID},
		})
		require.NoError(t, err)

		startedOutput, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		require.NoError(t, err)
		require.Len(t, startedOutput.Reservations, 1)
		require.Len(t, startedOutput.Reservations[0].Instances, 1)
		startedInstance := startedOutput.Reservations[0].Instances[0]
		if startedInstance.StateTransitionReason != nil {
			assert.Empty(t, *startedInstance.StateTransitionReason)
		}
		assert.Nil(t, startedInstance.StateReason)

		_, err = e.Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
			InstanceIds: []string{instanceID},
		})
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			describeOutput, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				InstanceIds: []string{instanceID},
			})
			if err != nil || len(describeOutput.Reservations) != 1 || len(describeOutput.Reservations[0].Instances) != 1 {
				return false
			}
			terminatedInstance := describeOutput.Reservations[0].Instances[0]
			if terminatedInstance.State == nil || terminatedInstance.State.Name != types.InstanceStateNameTerminated {
				return false
			}
			if terminatedInstance.StateTransitionReason == nil ||
				!strings.Contains(*terminatedInstance.StateTransitionReason, "User initiated (") {
				return false
			}
			if terminatedInstance.StateReason == nil || terminatedInstance.StateReason.Code == nil {
				return false
			}
			return *terminatedInstance.StateReason.Code == "Client.UserInitiatedShutdown"
		}, 10*time.Second, 250*time.Millisecond)

		require.Eventually(t, func() bool {
			describeOutput, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				InstanceIds: []string{instanceID},
			})
			return err == nil && len(describeOutput.Reservations) == 0
		}, 15*time.Second, 250*time.Millisecond)
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
		require.NotNil(t, instance.PrivateIpAddress)
		assert.NotEmpty(t, *instance.PrivateIpAddress)
		require.NotNil(t, instance.PublicIpAddress)
		assert.NotEmpty(t, *instance.PublicIpAddress)
		assert.NotEqual(t, "169.254.169.254", *instance.PublicIpAddress)
		require.NotNil(t, instance.PrivateDnsName)
		assert.Equal(
			t,
			fmt.Sprintf("ip-%s.%s.compute.internal", strings.ReplaceAll(*instance.PrivateIpAddress, ".", "-"), e.Region),
			*instance.PrivateDnsName,
		)
		require.NotNil(t, instance.PublicDnsName)
		assert.Equal(
			t,
			fmt.Sprintf("ec2-%s.%s.compute.internal", strings.ReplaceAll(*instance.PublicIpAddress, ".", "-"), e.Region),
			*instance.PublicDnsName,
		)
		require.Len(t, instance.NetworkInterfaces, 1)
		networkInterface := instance.NetworkInterfaces[0]
		require.NotNil(t, networkInterface.NetworkInterfaceId)
		assert.True(t, strings.HasPrefix(*networkInterface.NetworkInterfaceId, "eni-"))
		require.NotNil(t, networkInterface.MacAddress)
		assert.NotEmpty(t, *networkInterface.MacAddress)
		require.NotNil(t, networkInterface.PrivateIpAddress)
		assert.Equal(t, *instance.PrivateIpAddress, *networkInterface.PrivateIpAddress)
		require.NotNil(t, networkInterface.PrivateDnsName)
		assert.Equal(t, *instance.PrivateDnsName, *networkInterface.PrivateDnsName)
		require.NotNil(t, networkInterface.Association)
		require.NotNil(t, networkInterface.Association.PublicIp)
		assert.Equal(t, *instance.PublicIpAddress, *networkInterface.Association.PublicIp)
		require.NotNil(t, networkInterface.Association.PublicDnsName)
		assert.Equal(t, *instance.PublicDnsName, *networkInterface.Association.PublicDnsName)
		require.Len(t, networkInterface.PrivateIpAddresses, 1)
		privateIPAssociation := networkInterface.PrivateIpAddresses[0]
		require.NotNil(t, privateIPAssociation.Primary)
		assert.True(t, *privateIPAssociation.Primary)
		require.NotNil(t, privateIPAssociation.PrivateIpAddress)
		assert.Equal(t, *instance.PrivateIpAddress, *privateIPAssociation.PrivateIpAddress)

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

func TestDescribeInstancesAdditionalFilters(t *testing.T) {
	t.Parallel()
	testWithServer(t, func(t *testing.T, ctx context.Context, e *TestEnvironment) {
		runA, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx"),
			InstanceType: "type-a",
			MinCount:     aws.Int32(1),
			MaxCount:     aws.Int32(1),
		})
		require.NoError(t, err)
		require.Len(t, runA.Instances, 1)
		instanceAID := aws.ToString(runA.Instances[0].InstanceId)

		runB, err := e.Client.RunInstances(ctx, &ec2.RunInstancesInput{
			ImageId:      aws.String("nginx"),
			InstanceType: "type-b",
			MinCount:     aws.Int32(1),
			MaxCount:     aws.Int32(1),
		})
		require.NoError(t, err)
		require.Len(t, runB.Instances, 1)
		instanceBID := aws.ToString(runB.Instances[0].InstanceId)

		t.Cleanup(func() {
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, err := e.Client.TerminateInstances(cleanupCtx, &ec2.TerminateInstancesInput{
				InstanceIds: []string{instanceAID, instanceBID},
			})
			if err != nil {
				var apiErr smithy.APIError
				if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidInstanceID.NotFound" {
					require.NoError(t, err)
				}
			}
		})

		_, err = e.Client.StopInstances(ctx, &ec2.StopInstancesInput{
			InstanceIds: []string{instanceAID},
		})
		require.NoError(t, err)

		describeB, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceBID},
		})
		require.NoError(t, err)
		require.Len(t, describeB.Reservations, 1)
		require.Len(t, describeB.Reservations[0].Instances, 1)
		instanceB := describeB.Reservations[0].Instances[0]
		require.NotNil(t, instanceB.PrivateIpAddress)
		require.NotNil(t, instanceB.PublicIpAddress)

		describeWithFilters := func(filters []types.Filter) []types.Instance {
			t.Helper()
			out, err := e.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				Filters: filters,
			})
			require.NoError(t, err)
			var instances []types.Instance
			for _, reservation := range out.Reservations {
				instances = append(instances, reservation.Instances...)
			}
			return instances
		}

		stoppedInstances := describeWithFilters([]types.Filter{
			{Name: aws.String("instance-state-name"), Values: []string{"stopped"}},
		})
		require.Len(t, stoppedInstances, 1)
		assert.Equal(t, instanceAID, aws.ToString(stoppedInstances[0].InstanceId))

		runningInstances := describeWithFilters([]types.Filter{
			{Name: aws.String("instance-state-name"), Values: []string{"running"}},
		})
		require.Len(t, runningInstances, 1)
		assert.Equal(t, instanceBID, aws.ToString(runningInstances[0].InstanceId))

		privateIPInstances := describeWithFilters([]types.Filter{
			{Name: aws.String("private-ip-address"), Values: []string{aws.ToString(instanceB.PrivateIpAddress)}},
		})
		require.Len(t, privateIPInstances, 1)
		assert.Equal(t, instanceBID, aws.ToString(privateIPInstances[0].InstanceId))

		publicIPInstances := describeWithFilters([]types.Filter{
			{Name: aws.String("ip-address"), Values: []string{aws.ToString(instanceB.PublicIpAddress)}},
		})
		require.Len(t, publicIPInstances, 1)
		assert.Equal(t, instanceBID, aws.ToString(publicIPInstances[0].InstanceId))

		instanceTypeA := describeWithFilters([]types.Filter{
			{Name: aws.String("instance-type"), Values: []string{"type-a"}},
		})
		require.Len(t, instanceTypeA, 1)
		assert.Equal(t, instanceAID, aws.ToString(instanceTypeA[0].InstanceId))

		byAvailabilityZone := describeWithFilters([]types.Filter{
			{Name: aws.String("availability-zone"), Values: []string{e.Region + "a"}},
		})
		require.Len(t, byAvailabilityZone, 2)
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
		instanceIDs := make([]string, 0, len(runInstancesOutput.Instances))
		for _, instance := range runInstancesOutput.Instances {
			require.NotNil(t, instance.InstanceId)
			instanceIDs = append(instanceIDs, *instance.InstanceId)
		}
		t.Cleanup(func() {
			cleanupCtx, cancel := cleanupAPICtx(t)
			defer cancel()
			_, err := e.Client.TerminateInstances(cleanupCtx, &ec2.TerminateInstancesInput{
				InstanceIds: instanceIDs,
			})
			if err != nil {
				var apiErr smithy.APIError
				if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidInstanceID.NotFound" {
					require.NoError(t, err)
				}
			}
		})
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

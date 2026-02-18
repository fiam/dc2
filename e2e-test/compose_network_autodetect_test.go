package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	composeNetworkAutodetectFile = "compose/network-autodetect/docker-compose.yaml"
	e2eRegion                    = "us-east-1"
	e2eDC2Image                  = "dc2"
	e2eComposeServiceDC2         = "dc2"
)

func TestComposeAutoDetectsWorkloadNetworkByDefault(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	requireDockerCompose(t, ctx)
	requireDockerImage(t, ctx, e2eDC2Image)
	composeFile := composeFilePath(t)

	projectName := fmt.Sprintf("dc2e2e%d", time.Now().UnixNano())
	expectedNetwork := projectName + "_default"

	upCtx, upCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer upCancel()
	upOut, upErr := dockerCommandContext(
		upCtx,
		nil,
		"compose",
		"-f",
		composeFile,
		"-p",
		projectName,
		"up",
		"-d",
	)
	require.NoError(t, upErr, "docker compose up output: %s", string(upOut))

	mainContainerID := strings.TrimSpace(composeServiceContainerID(t, ctx, projectName, e2eComposeServiceDC2))
	require.NotEmpty(t, mainContainerID)
	t.Cleanup(func() {
		downCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		downOut, downErr := dockerCommandContext(
			downCtx,
			nil,
			"compose",
			"-f",
			composeFile,
			"-p",
			projectName,
			"down",
			"--volumes",
			"--remove-orphans",
		)
		if downErr != nil {
			t.Logf("docker compose down failed: %v output: %s", downErr, strings.TrimSpace(string(downOut)))
		}
		cleanupOwnedInstanceContainers(t, context.Background(), mainContainerID)
	})

	endpoint := fmt.Sprintf(
		"http://%s",
		composeServiceHostPort(t, ctx, projectName, e2eComposeServiceDC2, "8080"),
	)
	waitForDC2API(t, endpoint, 60*time.Second)

	client := newEC2Client(t, ctx, endpoint)
	runOut, runErr := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("alpine:latest"),
		InstanceType: "my-type",
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	require.NoError(t, runErr)
	require.Len(t, runOut.Instances, 1)
	require.NotNil(t, runOut.Instances[0].InstanceId)

	instanceID := aws.ToString(runOut.Instances[0].InstanceId)
	runtimeInstanceID, ok := strings.CutPrefix(instanceID, "i-")
	require.True(t, ok, "unexpected instance id format: %s", instanceID)
	t.Cleanup(func() {
		terminateCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = client.TerminateInstances(terminateCtx, &ec2.TerminateInstancesInput{
			InstanceIds: []string{instanceID},
		})
	})

	instanceContainerID := waitForContainerWithLabel(
		t,
		ctx,
		fmt.Sprintf("dc2:instance-id=%s", runtimeInstanceID),
		90*time.Second,
	)
	require.NotEmpty(t, instanceContainerID)

	template := fmt.Sprintf("{{if index .NetworkSettings.Networks %q}}present{{else}}missing{{end}}", expectedNetwork)
	inspectCtx, inspectCancel := context.WithTimeout(ctx, 30*time.Second)
	defer inspectCancel()
	inspectOut, inspectErr := dockerCommandContext(inspectCtx, nil, "inspect", "-f", template, instanceContainerID)
	require.NoError(t, inspectErr, "docker inspect output: %s", strings.TrimSpace(string(inspectOut)))
	assert.Equal(t, "present", strings.TrimSpace(string(inspectOut)))
}

func dockerCommandContext(ctx context.Context, env map[string]string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for key := range env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		cmd.Env = append([]string{}, os.Environ()...)
		for _, key := range keys {
			cmd.Env = append(cmd.Env, key+"="+env[key])
		}
	}
	return cmd.CombinedOutput()
}

func composeServiceContainerID(t *testing.T, ctx context.Context, projectName string, service string) string {
	t.Helper()

	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := dockerCommandContext(
		cmdCtx,
		nil,
		"compose",
		"-f",
		composeFilePath(t),
		"-p",
		projectName,
		"ps",
		"-q",
		service,
	)
	require.NoError(t, err, "docker compose ps output: %s", strings.TrimSpace(string(out)))
	return strings.TrimSpace(string(out))
}

func composeServiceHostPort(t *testing.T, ctx context.Context, projectName string, service string, port string) string {
	t.Helper()

	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := dockerCommandContext(
		cmdCtx,
		nil,
		"compose",
		"-f",
		composeFilePath(t),
		"-p",
		projectName,
		"port",
		service,
		port,
	)
	require.NoError(t, err, "docker compose port output: %s", strings.TrimSpace(string(out)))

	hostPort := strings.TrimSpace(string(out))
	host, publishedPort, splitErr := net.SplitHostPort(hostPort)
	require.NoError(t, splitErr, "unexpected compose port output: %s", hostPort)
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, publishedPort)
}

func requireDockerCompose(t *testing.T, ctx context.Context) {
	t.Helper()

	cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := dockerCommandContext(cmdCtx, nil, "compose", "version")
	require.NoError(t, err, "docker compose version output: %s", strings.TrimSpace(string(out)))
}

func requireDockerImage(t *testing.T, ctx context.Context, imageName string) {
	t.Helper()

	cmdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := dockerCommandContext(cmdCtx, nil, "image", "inspect", imageName)
	require.NoError(
		t,
		err,
		"docker image %s is required for e2e tests; run `make image` first (inspect output: %s)",
		imageName,
		strings.TrimSpace(string(out)),
	)
}

func waitForDC2API(t *testing.T, endpoint string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	httpClient := &http.Client{Timeout: 3 * time.Second}
	body := "Action=DescribeInstances&Version=2016-11-15"
	var lastErr error
	var lastStatusCode int

	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := httpClient.Do(req)
		if err == nil {
			lastStatusCode = resp.StatusCode
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
		t.Fatalf("dc2 endpoint %s did not become ready: %v", endpoint, lastErr)
	}
	t.Fatalf("dc2 endpoint %s did not become ready: last status=%d", endpoint, lastStatusCode)
}

func newEC2Client(t *testing.T, ctx context.Context, endpoint string) *ec2.Client {
	t.Helper()

	cfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithRegion(e2eRegion),
		config.WithCredentialsProvider(aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     "test",
				SecretAccessKey: "test",
				Source:          "e2e-test",
			}, nil
		})),
	)
	require.NoError(t, err)

	return ec2.NewFromConfig(cfg, func(opts *ec2.Options) {
		opts.BaseEndpoint = aws.String(endpoint)
	})
}

func waitForContainerWithLabel(t *testing.T, ctx context.Context, labelFilter string, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := dockerCommandContext(cmdCtx, nil, "ps", "-aq", "--filter", "label="+labelFilter)
		cancel()
		require.NoError(t, err, "docker ps output: %s", strings.TrimSpace(string(out)))

		ids := strings.Fields(strings.TrimSpace(string(out)))
		if len(ids) > 0 {
			return ids[0]
		}
		time.Sleep(300 * time.Millisecond)
	}
	debugCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	debugOut, _ := dockerCommandContext(
		debugCtx,
		nil,
		"ps",
		"-a",
		"--filter",
		"label=dc2:enabled=true",
		"--format",
		"{{.ID}} {{.Status}} {{.Labels}}",
	)
	t.Fatalf("timed out waiting for container label=%s; dc2 containers: %s", labelFilter, strings.TrimSpace(string(debugOut)))
	return ""
}

func cleanupOwnedInstanceContainers(t *testing.T, ctx context.Context, ownerContainerID string) {
	t.Helper()

	if strings.TrimSpace(ownerContainerID) == "" {
		return
	}

	listCtx, listCancel := context.WithTimeout(ctx, 20*time.Second)
	defer listCancel()
	out, err := dockerCommandContext(
		listCtx,
		nil,
		"ps",
		"-aq",
		"--filter",
		"label=dc2:imds-owner="+ownerContainerID,
	)
	if err != nil {
		t.Logf("listing owner containers failed: %v output: %s", err, strings.TrimSpace(string(out)))
		return
	}
	ids := strings.Fields(strings.TrimSpace(string(out)))
	if len(ids) == 0 {
		return
	}

	rmArgs := append([]string{"rm", "-f"}, ids...)
	rmCtx, rmCancel := context.WithTimeout(ctx, 30*time.Second)
	defer rmCancel()
	rmOut, rmErr := dockerCommandContext(rmCtx, nil, rmArgs...)
	if rmErr != nil {
		t.Logf("removing owned containers failed: %v output: %s", rmErr, strings.TrimSpace(string(rmOut)))
	}
}

func composeFilePath(t *testing.T) string {
	t.Helper()

	require.FileExists(t, composeNetworkAutodetectFile)
	return composeNetworkAutodetectFile
}

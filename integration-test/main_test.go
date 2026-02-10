package dc2_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lmittmann/tint"
)

type testContainerSnapshot struct {
	testHarness map[string]struct{}
	instances   map[string]struct{}
	imdsProxy   map[string]struct{}
}

func TestMain(m *testing.M) {
	testLogDebug, _ := strconv.ParseBool(os.Getenv("DC2_TEST_LOG_DEBUG"))
	if testLogDebug {
		slog.SetDefault(slog.New(
			tint.NewHandler(os.Stderr, &tint.Options{
				Level:      slog.LevelDebug,
				TimeFormat: time.Kitchen,
			}),
		))
	}

	snapshot := testContainerSnapshot{
		testHarness: snapshotContainerIDs("--filter", "label="+testContainerLabel),
		instances:   snapshotContainerIDs("--filter", "label=dc2:enabled=true"),
		imdsProxy:   snapshotContainerIDs("--filter", "label=dc2:imds-proxy-version"),
	}

	exitCode := m.Run()
	for _, check := range []struct {
		name     string
		snapshot map[string]struct{}
		filters  []string
	}{
		{
			name:     "test harness containers",
			snapshot: snapshot.testHarness,
			filters:  []string{"--filter", "label=" + testContainerLabel},
		},
		{
			name:     "dc2 instance containers",
			snapshot: snapshot.instances,
			filters:  []string{"--filter", "label=dc2:enabled=true"},
		},
		{
			name:     "dc2 imds proxy containers",
			snapshot: snapshot.imdsProxy,
			filters:  []string{"--filter", "label=dc2:imds-proxy-version"},
		},
	} {
		if err := verifyNoNewContainers(check.snapshot, check.filters...); err != nil {
			slog.Error("container cleanup verification failed", "group", check.name, "error", err)
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

func snapshotContainerIDs(filters ...string) map[string]struct{} {
	ids, err := containerIDs(filters...)
	if err != nil {
		slog.Warn("failed to capture test container snapshot", "filters", strings.Join(filters, " "), "error", err)
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
}

func verifyNoNewContainers(snapshot map[string]struct{}, filters ...string) error {
	ids, err := containerIDs(filters...)
	if err != nil {
		return fmt.Errorf("listing containers with filters %q: %w", strings.Join(filters, " "), err)
	}

	leaked := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := snapshot[id]; !ok {
			leaked = append(leaked, id)
		}
	}
	if len(leaked) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args := append([]string{"rm", "-f"}, leaked...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"found %d leaked containers (%s) and failed cleanup: %w: %s",
			len(leaked),
			strings.Join(leaked, ","),
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return fmt.Errorf("found %d leaked containers (%s)", len(leaked), strings.Join(leaked, ","))
}

func containerIDs(filters ...string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args := append([]string{"ps", "-aq"}, filters...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.Fields(string(output)), nil
}

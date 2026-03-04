package dc2

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/storage"
)

type closeModeExecutor struct {
	*exitCleanupExecutor
	closeCalls      int
	disconnectCalls int
}

func (e *closeModeExecutor) Close(context.Context) error {
	e.closeCalls++
	return nil
}

func (e *closeModeExecutor) Disconnect() error {
	e.disconnectCalls++
	return nil
}

func TestDispatcherCloseAssertModeUsesExecutorClose(t *testing.T) {
	t.Parallel()

	exe := &closeModeExecutor{
		exitCleanupExecutor: &exitCleanupExecutor{},
	}
	dispatch := &Dispatcher{
		opts:    DispatcherOptions{ExitResourceMode: ExitResourceModeAssert},
		exe:     exe,
		storage: storage.NewMemoryStorage(),
	}

	err := dispatch.Close(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, exe.closeCalls)
	assert.Equal(t, 0, exe.disconnectCalls)
}

func TestDispatcherCloseKeepModeUsesExecutorClose(t *testing.T) {
	t.Parallel()

	exe := &closeModeExecutor{
		exitCleanupExecutor: &exitCleanupExecutor{},
	}
	dispatch := &Dispatcher{
		opts:    DispatcherOptions{ExitResourceMode: ExitResourceModeKeep},
		exe:     exe,
		storage: storage.NewMemoryStorage(),
	}

	err := dispatch.Close(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, exe.closeCalls)
	assert.Equal(t, 0, exe.disconnectCalls)
}

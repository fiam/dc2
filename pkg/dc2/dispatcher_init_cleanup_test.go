package dc2

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/docker"
	"github.com/fiam/dc2/pkg/dc2/executor"
	"github.com/fiam/dc2/pkg/dc2/instancetype"
)

type initCleanupExecutor struct {
	*exitCleanupExecutor
	closeCalls int
}

func (e *initCleanupExecutor) Close(context.Context) error {
	e.closeCalls++
	return nil
}

func TestNewDispatcherClosesExecutorOnCatalogLoadError(t *testing.T) {
	t.Parallel()

	exe := &initCleanupExecutor{
		exitCleanupExecutor: &exitCleanupExecutor{},
	}
	dispatch, err := newDispatcherWithHooks(
		context.Background(),
		DispatcherOptions{},
		&imdsController{},
		dispatcherInitHooks{
			newExecutor: func(context.Context, docker.ExecutorOptions) (executor.Executor, error) {
				return exe, nil
			},
			loadInstanceTypeCatalog: func() (*instancetype.Catalog, error) {
				return nil, errors.New("boom")
			},
		},
	)
	require.Error(t, err)
	assert.Nil(t, dispatch)
	assert.Contains(t, err.Error(), "loading instance type catalog")
	assert.Equal(t, 1, exe.closeCalls)
}

func TestNewDispatcherClosesExecutorOnProfileLoadError(t *testing.T) {
	t.Parallel()

	exe := &initCleanupExecutor{
		exitCleanupExecutor: &exitCleanupExecutor{},
	}
	dispatch, err := newDispatcherWithHooks(
		context.Background(),
		DispatcherOptions{
			TestProfileInput: "this-is-not-a-profile",
		},
		&imdsController{},
		dispatcherInitHooks{
			newExecutor: func(context.Context, docker.ExecutorOptions) (executor.Executor, error) {
				return exe, nil
			},
			loadInstanceTypeCatalog: func() (*instancetype.Catalog, error) {
				return &instancetype.Catalog{}, nil
			},
		},
	)
	require.Error(t, err)
	assert.Nil(t, dispatch)
	assert.Contains(t, err.Error(), "loading test profile")
	assert.Equal(t, 1, exe.closeCalls)
}

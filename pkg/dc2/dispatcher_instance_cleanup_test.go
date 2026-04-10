package dc2

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fiam/dc2/pkg/dc2/storage"
	"github.com/fiam/dc2/pkg/dc2/types"
)

func TestScheduleTerminatedInstanceStorageCleanupRemovesExpiredInstance(t *testing.T) {
	t.Parallel()

	store := storage.NewMemoryStorage()
	const instanceID = "i-terminated-cleanup"
	require.NoError(t, store.RegisterResource(storage.Resource{Type: types.ResourceTypeInstance, ID: instanceID}))

	terminatedAt := time.Now().UTC()
	require.NoError(t, store.SetResourceAttributes(instanceID, []storage.Attribute{
		{Key: attributeNameInstanceTerminatedAt, Value: terminatedAt.Format(time.RFC3339Nano)},
	}))

	dispatch := &Dispatcher{storage: store}
	dispatch.scheduleTerminatedInstanceStorageCleanup(instanceID, terminatedAt)

	require.Eventually(t, func() bool {
		_, err := store.ResourceAttributes(instanceID)
		var notFound storage.ErrResourceNotFound
		return errors.As(err, &notFound)
	}, terminatedStorageTTL+2*time.Second, 100*time.Millisecond)
}

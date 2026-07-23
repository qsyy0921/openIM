package agent

import (
	"context"
	"fmt"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/capability"
)

func (s *Store) LoadCapabilitySnapshot(ctx context.Context, run Run) (capability.Snapshot, error) {
	snapshot, err := capability.NewStore(s.pool).LoadSnapshot(ctx, run.TenantID, run.CapabilitySnapshotID)
	if err != nil {
		return capability.Snapshot{}, fmt.Errorf("load pinned Agent capability snapshot: %w", err)
	}
	return snapshot, nil
}

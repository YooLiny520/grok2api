package account

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/pkg/batch"
)

func TestIsUpstreamRateLimited(t *testing.T) {
	if !isUpstreamRateLimited(provider.ErrRateLimited) {
		t.Fatal("ErrRateLimited should match")
	}
	if !isUpstreamRateLimited(errors.New("Grok Session 接口返回 429")) {
		t.Fatal("429 message should match")
	}
	if isUpstreamRateLimited(errors.New("network timeout")) {
		t.Fatal("unrelated error should not match")
	}
}

func TestWithUpstreamRateLimitRetryEventuallySucceeds(t *testing.T) {
	var calls atomic.Int64
	err := withUpstreamRateLimitRetry(context.Background(), func(context.Context) error {
		if calls.Add(1) < 3 {
			return provider.ErrRateLimited
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retry err = %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestRunAccountBatchRetriesRateLimitedTasks(t *testing.T) {
	service := NewService(nil, nil, nil, nil, nil, nil, nil)
	pool := batch.NewPool(2)
	service.SetTaskPools(pool, pool, pool)

	var calls atomic.Int64
	ids := []uint64{1, 2, 3}
	succeeded, failed, err := service.runAccountBatch(context.Background(), "quota_sync_test", ids, pool, nil, func(ctx context.Context, id uint64) error {
		n := calls.Add(1)
		// First pass for every id fails once with 429, then succeeds.
		if n <= int64(len(ids)) {
			return provider.ErrRateLimited
		}
		return nil
	})
	if err != nil {
		t.Fatalf("batch err = %v", err)
	}
	if succeeded != len(ids) || failed != 0 {
		t.Fatalf("succeeded=%d failed=%d calls=%d", succeeded, failed, calls.Load())
	}
	if calls.Load() < int64(len(ids)*2) {
		t.Fatalf("expected retries, calls=%d", calls.Load())
	}
}

func TestRunAccountBatchWavePauseForLargeSets(t *testing.T) {
	service := NewService(nil, nil, nil, nil, nil, nil, nil)
	pool := batch.NewPool(2)
	service.SetTaskPools(pool, pool, pool)

	// Shrink wave size constants via local override is not possible; use enough ids to force >1 wave with default 30.
	// Instead, verify pause path by temporarily calling with many ids and ensuring total wall time grows.
	ids := make([]uint64, 0, 35)
	for i := uint64(1); i <= 35; i++ {
		ids = append(ids, i)
	}
	start := time.Now()
	succeeded, failed, err := service.runAccountBatch(context.Background(), "quota_sync_wave", ids, pool, nil, func(context.Context, uint64) error {
		return nil
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("batch err = %v", err)
	}
	if succeeded != len(ids) || failed != 0 {
		t.Fatalf("succeeded=%d failed=%d", succeeded, failed)
	}
	// One inter-wave pause of 8s is expected for 35 items with wave size ~15-40.
	if elapsed < 7*time.Second {
		t.Fatalf("expected wave pause, elapsed=%s", elapsed)
	}
}

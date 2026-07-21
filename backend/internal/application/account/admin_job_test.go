package account

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdminJobManagerRunsInBackground(t *testing.T) {
	manager := newAdminJobManager()
	started := make(chan struct{})
	job, err := manager.start(AdminJobWebQuotaSync, "test", func(ctx context.Context, report BatchProgressObserver) (int, int, error) {
		close(started)
		if err := report(1, 2); err != nil {
			return 0, 0, err
		}
		time.Sleep(100 * time.Millisecond)
		if err := report(2, 2); err != nil {
			return 0, 0, err
		}
		return 2, 0, nil
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if job.Status != AdminJobRunning {
		t.Fatalf("status = %s", job.Status)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		view, err := manager.snapshot(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == AdminJobSucceeded {
			if view.Succeeded != 2 || view.Failed != 0 {
				t.Fatalf("result = %+v", view)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not finish")
}

func TestAdminJobManagerRejectsConcurrentJobs(t *testing.T) {
	manager := newAdminJobManager()
	block := make(chan struct{})
	first, err := manager.start(AdminJobWebQuotaSync, "first", func(ctx context.Context, report BatchProgressObserver) (int, int, error) {
		<-block
		return 1, 0, nil
	})
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	_, err = manager.start(AdminJobWebAccountScripts, "second", func(ctx context.Context, report BatchProgressObserver) (int, int, error) {
		return 0, 0, nil
	})
	if !errors.Is(err, ErrAdminJobBusy) {
		t.Fatalf("second start err = %v", err)
	}
	close(block)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		view, _ := manager.snapshot(first.ID)
		if view.Status != AdminJobRunning {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("first job stuck")
}

func TestAdminJobManagerCancel(t *testing.T) {
	manager := newAdminJobManager()
	started := make(chan struct{})
	job, err := manager.start(AdminJobBillingSync, "cancel-me", func(ctx context.Context, report BatchProgressObserver) (int, int, error) {
		close(started)
		<-ctx.Done()
		return 0, 0, ctx.Err()
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	<-started
	view, err := manager.cancel(job.ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if view.Status != AdminJobCanceled && view.Status != AdminJobRunning {
		// may still be flipping; poll
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			view, _ = manager.snapshot(job.ID)
			if view.Status == AdminJobCanceled {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("status = %s", view.Status)
	}
}

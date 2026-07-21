package account

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrAdminJobBusy 表示已有后台账号任务在执行。
var ErrAdminJobBusy = errors.New("已有后台账号任务在执行")

// ErrAdminJobNotFound 表示后台任务不存在。
var ErrAdminJobNotFound = errors.New("后台任务不存在")

// AdminJobKind 标识后台账号任务类型。
type AdminJobKind string

const (
	AdminJobWebQuotaSync      AdminJobKind = "web_quota_sync"
	AdminJobConsoleQuotaSync  AdminJobKind = "console_quota_sync"
	AdminJobBillingSync       AdminJobKind = "billing_sync"
	AdminJobBatchQuotaSync    AdminJobKind = "batch_quota_sync"
	AdminJobWebAccountScripts AdminJobKind = "web_account_scripts"
)

// AdminJobStatus 后台任务状态。
type AdminJobStatus string

const (
	AdminJobRunning   AdminJobStatus = "running"
	AdminJobSucceeded AdminJobStatus = "succeeded"
	AdminJobFailed    AdminJobStatus = "failed"
	AdminJobCanceled  AdminJobStatus = "canceled"
)

// AdminJobView 对外暴露的后台任务快照。
type AdminJobView struct {
	ID         string
	Kind       AdminJobKind
	Status     AdminJobStatus
	Completed  int
	Total      int
	Succeeded  int
	Failed     int
	Message    string
	Error      string
	StartedAt  time.Time
	FinishedAt *time.Time
}

type adminJobRecord struct {
	view   AdminJobView
	cancel context.CancelFunc
}

type adminJobManager struct {
	mu       sync.RWMutex
	activeID string
	jobs     map[string]*adminJobRecord
	maxKeep  int
}

func newAdminJobManager() *adminJobManager {
	return &adminJobManager{
		jobs:    make(map[string]*adminJobRecord),
		maxKeep: 30,
	}
}

func (m *adminJobManager) start(kind AdminJobKind, message string, run func(ctx context.Context, report BatchProgressObserver) (succeeded, failed int, err error)) (AdminJobView, error) {
	if m == nil {
		return AdminJobView{}, fmt.Errorf("admin job manager is not initialized")
	}
	m.mu.Lock()
	if m.activeID != "" {
		if active, ok := m.jobs[m.activeID]; ok && active.view.Status == AdminJobRunning {
			view := active.view
			m.mu.Unlock()
			return view, ErrAdminJobBusy
		}
		m.activeID = ""
	}
	id := uuid.NewString()
	ctx, cancel := context.WithCancel(context.Background())
	record := &adminJobRecord{
		view: AdminJobView{
			ID:        id,
			Kind:      kind,
			Status:    AdminJobRunning,
			Message:   message,
			StartedAt: time.Now().UTC(),
		},
		cancel: cancel,
	}
	m.jobs[id] = record
	m.activeID = id
	m.mu.Unlock()

	go m.execute(id, ctx, cancel, run)
	return m.snapshot(id)
}

func (m *adminJobManager) execute(id string, ctx context.Context, cancel context.CancelFunc, run func(ctx context.Context, report BatchProgressObserver) (succeeded, failed int, err error)) {
	defer cancel()
	report := func(completed, total int) error {
		m.mu.Lock()
		if record, ok := m.jobs[id]; ok {
			record.view.Completed = completed
			record.view.Total = total
		}
		m.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	}

	succeeded, failed, err := run(ctx, report)
	finishedAt := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.jobs[id]
	if !ok {
		return
	}
	record.view.Succeeded = succeeded
	record.view.Failed = failed
	record.view.FinishedAt = &finishedAt
	if record.view.Total == 0 && record.view.Completed == 0 {
		record.view.Total = succeeded + failed
		record.view.Completed = succeeded + failed
	}
	switch {
	case errors.Is(err, context.Canceled):
		record.view.Status = AdminJobCanceled
		record.view.Error = "任务已取消"
	case err != nil:
		record.view.Status = AdminJobFailed
		record.view.Error = err.Error()
	default:
		record.view.Status = AdminJobSucceeded
	}
	if m.activeID == id {
		m.activeID = ""
	}
	m.pruneLocked()
}

func (m *adminJobManager) snapshot(id string) (AdminJobView, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	record, ok := m.jobs[id]
	if !ok {
		return AdminJobView{}, ErrAdminJobNotFound
	}
	return record.view, nil
}

func (m *adminJobManager) list() []AdminJobView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]AdminJobView, 0, len(m.jobs))
	for _, record := range m.jobs {
		result = append(result, record.view)
	}
	// newest first
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].StartedAt.After(result[i].StartedAt) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

func (m *adminJobManager) cancel(id string) (AdminJobView, error) {
	m.mu.Lock()
	record, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return AdminJobView{}, ErrAdminJobNotFound
	}
	if record.view.Status != AdminJobRunning {
		view := record.view
		m.mu.Unlock()
		return view, nil
	}
	cancel := record.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// wait briefly for status flip by execute
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		view, err := m.snapshot(id)
		if err != nil {
			return AdminJobView{}, err
		}
		if view.Status != AdminJobRunning {
			return view, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return m.snapshot(id)
}

func (m *adminJobManager) pruneLocked() {
	if len(m.jobs) <= m.maxKeep {
		return
	}
	type item struct {
		id        string
		startedAt time.Time
		running   bool
	}
	items := make([]item, 0, len(m.jobs))
	for id, record := range m.jobs {
		items = append(items, item{id: id, startedAt: record.view.StartedAt, running: record.view.Status == AdminJobRunning})
	}
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].startedAt.Before(items[i].startedAt) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	for _, entry := range items {
		if len(m.jobs) <= m.maxKeep {
			break
		}
		if entry.running || entry.id == m.activeID {
			continue
		}
		delete(m.jobs, entry.id)
	}
}

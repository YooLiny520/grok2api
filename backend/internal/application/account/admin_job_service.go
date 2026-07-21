package account

import (
	"context"
	"fmt"

	accountdomain "github.com/chenyme/grok2api/backend/internal/domain/account"
)

// StartWebQuotaSyncJob 在后台同步全部 Grok Web 额度。
func (s *Service) StartWebQuotaSyncJob() (AdminJobView, error) {
	return s.adminJobs.start(AdminJobWebQuotaSync, "同步 Grok Web 账号额度", func(ctx context.Context, report BatchProgressObserver) (int, int, error) {
		return s.SyncAllWebQuotasWithProgress(ctx, report)
	})
}

// StartConsoleQuotaSyncJob 在后台同步全部 Grok Console 额度。
func (s *Service) StartConsoleQuotaSyncJob() (AdminJobView, error) {
	return s.adminJobs.start(AdminJobConsoleQuotaSync, "同步 Grok Console 账号额度", func(ctx context.Context, report BatchProgressObserver) (int, int, error) {
		return s.SyncAllConsoleQuotasWithProgress(ctx, report)
	})
}

// StartBillingSyncJob 在后台同步全部 Billing 额度。
func (s *Service) StartBillingSyncJob() (AdminJobView, error) {
	return s.adminJobs.start(AdminJobBillingSync, "同步账号 Billing 额度", func(ctx context.Context, report BatchProgressObserver) (int, int, error) {
		return s.SyncAllBillingWithProgress(ctx, report)
	})
}

// StartBatchQuotaSyncJob 在后台同步指定账号额度。
func (s *Service) StartBatchQuotaSyncJob(ids []uint64, providerValue string) (AdminJobView, error) {
	values, err := normalizeBatchIDs(ids)
	if err != nil {
		return AdminJobView{}, err
	}
	message := fmt.Sprintf("同步 %d 个账号额度", len(values))
	return s.adminJobs.start(AdminJobBatchQuotaSync, message, func(ctx context.Context, report BatchProgressObserver) (int, int, error) {
		if providerValue == string(accountdomain.ProviderBuild) {
			return s.refreshBillings(ctx, values, report)
		}
		return s.runAccountBatch(ctx, "quota_sync", values, s.syncPool, report, func(workCtx context.Context, id uint64) error {
			_, err := s.RefreshQuota(workCtx, id)
			return err
		})
	})
}

// StartWebAccountScriptsJob 在后台执行 Grok Web 账号工具脚本。
func (s *Service) StartWebAccountScriptsJob(ids []uint64, all bool, options WebAccountScriptOptions) (AdminJobView, error) {
	options, err := normalizeWebAccountScriptOptions(options)
	if err != nil {
		return AdminJobView{}, err
	}
	message := "执行 Grok Web 账号工具"
	if all {
		message = "执行全部 Grok Web 账号工具"
	} else {
		message = fmt.Sprintf("执行 %d 个账号工具", len(ids))
	}
	return s.adminJobs.start(AdminJobWebAccountScripts, message, func(ctx context.Context, report BatchProgressObserver) (int, int, error) {
		if all {
			return s.RunAllWebAccountScriptsWithProgress(ctx, options, report)
		}
		return s.RunWebAccountScriptsWithProgress(ctx, ids, options, report)
	})
}

// GetAdminJob 返回后台任务快照。
func (s *Service) GetAdminJob(id string) (AdminJobView, error) {
	return s.adminJobs.snapshot(id)
}

// ListAdminJobs 返回最近后台任务列表。
func (s *Service) ListAdminJobs() []AdminJobView {
	return s.adminJobs.list()
}

// CancelAdminJob 取消正在运行的后台任务。
func (s *Service) CancelAdminJob(id string) (AdminJobView, error) {
	return s.adminJobs.cancel(id)
}

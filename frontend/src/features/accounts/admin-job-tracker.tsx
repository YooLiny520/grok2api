import { LoaderCircle, Square, X } from "lucide-react";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import {
  cancelAccountAdminJob,
  getAccountAdminJob,
  listAccountAdminJobs,
  type AccountAdminJobDTO,
} from "@/features/accounts/accounts-api";
import { cn } from "@/shared/lib/cn";

type TrackedAdminJob = AccountAdminJobDTO & {
  dismissed?: boolean;
};

type AdminJobTrackerContextValue = {
  jobs: TrackedAdminJob[];
  trackJob: (job: AccountAdminJobDTO) => void;
  dismissJob: (id: string) => void;
  cancelJob: (id: string) => Promise<void>;
  hasRunningJob: boolean;
};

const AdminJobTrackerContext = createContext<AdminJobTrackerContextValue | null>(null);

function sortJobs(jobs: TrackedAdminJob[]): TrackedAdminJob[] {
  return [...jobs].sort((a, b) => {
    const aRunning = a.status === "running" ? 1 : 0;
    const bRunning = b.status === "running" ? 1 : 0;
    if (aRunning !== bRunning) return bRunning - aRunning;
    return new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime();
  });
}

export function AdminJobTrackerProvider({ children }: { children: ReactNode }) {
  const [jobs, setJobs] = useState<TrackedAdminJob[]>([]);
  const queryClient = useQueryClient();
  const knownFinishedRef = useRef<Set<string>>(new Set());

  const upsertJob = useCallback((job: AccountAdminJobDTO) => {
    setJobs((current) => {
      const existing = current.find((item) => item.id === job.id);
      const next: TrackedAdminJob = {
        ...job,
        dismissed: existing?.dismissed && job.status !== "running" ? existing.dismissed : false,
      };
      const others = current.filter((item) => item.id !== job.id);
      return sortJobs([next, ...others]).slice(0, 8);
    });
  }, []);

  const trackJob = useCallback((job: AccountAdminJobDTO) => {
    knownFinishedRef.current.delete(job.id);
    upsertJob(job);
  }, [upsertJob]);

  const dismissJob = useCallback((id: string) => {
    setJobs((current) => current.map((job) => (job.id === id ? { ...job, dismissed: true } : job)));
  }, []);

  const cancelJob = useCallback(async (id: string) => {
    const job = await cancelAccountAdminJob(id);
    upsertJob(job);
  }, [upsertJob]);

  // Resume any running server jobs after refresh/navigation.
  useEffect(() => {
    let cancelled = false;
    void listAccountAdminJobs()
      .then((items) => {
        if (cancelled) return;
        items.filter((item) => item.status === "running").forEach((item) => upsertJob(item));
      })
      .catch(() => {
        // ignore bootstrap failures; page can still start new jobs
      });
    return () => {
      cancelled = true;
    };
  }, [upsertJob]);

  const runningJobIds = useMemo(
    () => jobs.filter((job) => job.status === "running").map((job) => job.id),
    [jobs],
  );

  // Poll running jobs while any are active.
  useEffect(() => {
    if (runningJobIds.length === 0) return;

    let cancelled = false;
    const tick = async () => {
      for (const id of runningJobIds) {
        try {
          const latest = await getAccountAdminJob(id);
          if (cancelled) return;
          upsertJob(latest);
          if (latest.status !== "running" && !knownFinishedRef.current.has(latest.id)) {
            knownFinishedRef.current.add(latest.id);
            void queryClient.invalidateQueries({ queryKey: ["accounts"] });
            void queryClient.invalidateQueries({ queryKey: ["account-summary"] });
          }
        } catch {
          // keep previous snapshot on transient poll errors
        }
      }
    };

    void tick();
    const timer = window.setInterval(() => {
      void tick();
    }, 1500);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [runningJobIds, queryClient, upsertJob]);

  const value = useMemo<AdminJobTrackerContextValue>(() => ({
    jobs,
    trackJob,
    dismissJob,
    cancelJob,
    hasRunningJob: jobs.some((job) => job.status === "running"),
  }), [jobs, trackJob, dismissJob, cancelJob]);

  return (
    <AdminJobTrackerContext.Provider value={value}>
      {children}
      <AdminJobTrackerBanner />
    </AdminJobTrackerContext.Provider>
  );
}

export function useAdminJobTracker(): AdminJobTrackerContextValue {
  const value = useContext(AdminJobTrackerContext);
  if (!value) {
    throw new Error("useAdminJobTracker must be used within AdminJobTrackerProvider");
  }
  return value;
}

function kindLabel(kind: string, t: (key: string) => string): string {
  switch (kind) {
    case "web_quota_sync":
      return t("accounts.jobKindWebQuota");
    case "console_quota_sync":
      return t("accounts.jobKindConsoleQuota");
    case "billing_sync":
      return t("accounts.jobKindBilling");
    case "batch_quota_sync":
      return t("accounts.jobKindBatchQuota");
    case "web_account_scripts":
      return t("accounts.jobKindScripts");
    default:
      return t("accounts.jobKindGeneric");
  }
}

function statusText(job: TrackedAdminJob, t: (key: string, options?: Record<string, unknown>) => string): string {
  if (job.status === "running") {
    const total = job.total > 0 ? job.total : "…";
    return t("accounts.backgroundJobProgress", { completed: job.completed, total });
  }
  if (job.status === "succeeded") {
    return t("accounts.allBillingRefreshed", { succeeded: job.succeeded, failed: job.failed });
  }
  if (job.status === "canceled") {
    return t("accounts.backgroundJobCanceled");
  }
  return job.error || t("accounts.backgroundJobFailed");
}

function AdminJobTrackerBanner() {
  const { t } = useTranslation();
  const { jobs, dismissJob, cancelJob } = useAdminJobTracker();
  const visible = jobs.filter((job) => !job.dismissed).slice(0, 3);
  if (visible.length === 0) return null;

  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-0 z-50 flex justify-center px-3 pb-3 sm:px-6 lg:pl-[300px]">
      <div className="pointer-events-auto w-full max-w-[720px] space-y-2">
        {visible.map((job) => {
          const running = job.status === "running";
          const failed = job.status === "failed";
          const percent = job.total > 0 ? Math.min(100, Math.round((job.completed / job.total) * 100)) : running ? 8 : 100;
          return (
            <div
              key={job.id}
              className={cn(
                "rounded-xl border bg-background/95 p-3 shadow-lg backdrop-blur supports-[backdrop-filter]:bg-background/85",
                failed ? "border-destructive/40" : "border-border/80",
              )}
            >
              <div className="flex items-start gap-3">
                <div className={cn(
                  "mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full",
                  failed ? "bg-destructive/10 text-destructive" : running ? "bg-primary/10 text-primary" : "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
                )}>
                  {running ? <LoaderCircle className="size-4 animate-spin" /> : <Square className="size-3.5 fill-current" />}
                </div>
                <div className="min-w-0 flex-1 space-y-1.5">
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium">{kindLabel(job.kind, t)}</p>
                      <p className="truncate text-xs text-muted-foreground">{job.message || statusText(job, t)}</p>
                    </div>
                    <div className="flex shrink-0 items-center gap-1">
                      {running ? (
                        <Button type="button" size="sm" variant="secondary" className="h-7 px-2 text-xs" onClick={() => void cancelJob(job.id)}>
                          {t("common.cancel")}
                        </Button>
                      ) : null}
                      <Button type="button" size="icon" variant="ghost" className="size-7" onClick={() => dismissJob(job.id)} aria-label={t("common.close")}>
                        <X className="size-3.5" />
                      </Button>
                    </div>
                  </div>
                  <div className="space-y-1">
                    <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                      <div
                        className={cn(
                          "h-full rounded-full transition-all duration-300",
                          failed ? "bg-destructive" : running ? "bg-primary" : "bg-emerald-500",
                        )}
                        style={{ width: `${percent}%` }}
                      />
                    </div>
                    <p className="text-[11px] tabular-nums text-muted-foreground">{statusText(job, t)}</p>
                  </div>
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

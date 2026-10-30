package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/schedule"
	"github.com/goravel/framework/support/env"

	"goravel/app/facades"
	"goravel/app/metrics"
	"goravel/app/services"
)

// Schedule runs inside the server process (docs/mvp.md, "Image pruning",
// "App status", "Metrics", "Scheduled tasks", "Backups" and
// "Notifications"; ADR 0021 for the update check). Artisan
// commands start the scheduler too, so they get no events: only the server
// prunes, polls, reads and rolls up metrics, and checks for updates.
func Schedule() []schedule.Event {
	if env.IsArtisan() {
		return nil
	}

	return []schedule.Event{
		facades.Schedule().Call(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			if err := services.PollAppStatus(ctx, time.Now()); err != nil {
				facades.Log().Error(fmt.Errorf("status poll: %w", err))
			}
		}).EveryThirtySeconds().SkipIfStillRunning().Name("echowa:poll-status"),

		facades.Schedule().Call(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			services.ReadMetrics(ctx)
		}).EveryFifteenSeconds().SkipIfStillRunning().Name("echowa:read-metrics"),

		facades.Schedule().Call(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
			defer cancel()
			services.CheckDatabases(ctx, time.Now())
		}).EveryMinute().SkipIfStillRunning().Name("echowa:check-databases"),

		// Starts the due tasks in the background and returns (ADR 0032).
		facades.Schedule().Call(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := services.RunDueTasks(ctx, time.Now()); err != nil {
				facades.Log().Error(fmt.Errorf("scheduled tasks: %w", err))
			}
		}).EveryMinute().SkipIfStillRunning().Name("echowa:run-scheduled-tasks"),

		// Starts the due backups in the background and returns (ADR 0034).
		facades.Schedule().Call(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := services.RunDueBackups(ctx, time.Now()); err != nil {
				facades.Log().Error(fmt.Errorf("backup schedules: %w", err))
			}
		}).EveryMinute().SkipIfStillRunning().Name("echowa:run-backups"),

		// Hourly at :05 for the hour before, daily at 00:15 for the day
		// before, and pruning at :30, clear of both.
		facades.Schedule().Call(func() {
			rollupMetrics(metrics.Hourly)
		}).HourlyAt([]string{"5"}).SkipIfStillRunning().Name("echowa:rollup-metrics-hourly"),

		facades.Schedule().Call(func() {
			rollupMetrics(metrics.Daily)
		}).DailyAt("00:15").SkipIfStillRunning().Name("echowa:rollup-metrics-daily"),

		facades.Schedule().Call(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if err := services.PruneMetrics(ctx, time.Now()); err != nil {
				facades.Log().Error(fmt.Errorf("metrics prune: %w", err))
			}
		}).HourlyAt([]string{"30"}).SkipIfStillRunning().Name("echowa:prune-metrics"),

		facades.Schedule().Call(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := services.PruneTaskRuns(ctx); err != nil {
				facades.Log().Error(fmt.Errorf("scheduled task runs prune: %w", err))
			}
		}).HourlyAt([]string{"45"}).SkipIfStillRunning().Name("echowa:prune-task-runs"),

		facades.Schedule().Call(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := services.PruneBackupRecords(ctx, time.Now()); err != nil {
				facades.Log().Error(fmt.Errorf("backup records prune: %w", err))
			}
		}).HourlyAt([]string{"50"}).SkipIfStillRunning().Name("echowa:prune-backup-records"),

		facades.Schedule().Call(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := services.PruneNotifications(ctx, time.Now()); err != nil {
				facades.Log().Error(fmt.Errorf("notifications prune: %w", err))
			}
		}).HourlyAt([]string{"55"}).SkipIfStillRunning().Name("echowa:prune-notifications"),

		// Tells providers how many notifications the storm guard held
		// back, once they have room (ADR 0040).
		facades.Schedule().Call(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := services.SendHeldBack(ctx); err != nil {
				facades.Log().Error(fmt.Errorf("held-back notifications: %w", err))
			}
		}).EveryMinute().SkipIfStillRunning().Name("echowa:send-held-back"),

		facades.Schedule().Call(func() {
			_, err := services.RunPrune(context.Background(), services.PruneScheduled)
			if errors.Is(err, services.ErrPruneRunning) {
				facades.Log().Info("prune (schedule): skipped, a prune is already running")
			}
		}).Daily().Name("echowa:prune-images"),

		facades.Schedule().Call(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			services.CheckForUpdate(ctx)
		}).DailyAt("06:00").Name("echowa:check-for-update"),
	}
}

func rollupMetrics(resolution metrics.Resolution) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := services.RollupMetrics(ctx, resolution, time.Now()); err != nil {
		facades.Log().Error(fmt.Errorf("metrics rollup (%s): %w", resolution.Key, err))
	}
}

package services

import (
	"context"
	"time"

	"github.com/goravel/framework/facades"
	"github.com/goravel/framework/support/carbon"

	"goravel/app/metrics"
)

// Activity is one thing that happened to an app: a deployment, a backup, a
// restore or a scheduled task's run. Name is a task's name, and Ref a
// deployment's. Mirrored by Activity in resources/js/types/models.ts.
type Activity struct {
	Kind      string           `json:"kind"`
	ID        uint             `json:"id"`
	AppID     uint             `json:"app_id"`
	Status    string           `json:"status"`
	Trigger   string           `json:"trigger"`
	Name      string           `json:"name"`
	Ref       string           `json:"ref"`
	CreatedAt *carbon.DateTime `json:"created_at"`
}

// recentActivity leaves out what happens all the time and goes fine: a
// scheduled task's run that succeeded or was skipped, and a backup skipped
// because the last one was still running.
const recentActivity = `
SELECT * FROM (
	SELECT 'deployment' AS kind, id, app_id, status, "trigger", '' AS name, ref, created_at FROM deployments
	UNION ALL
	SELECT 'backup', id, app_id, status, "trigger", '', '', created_at FROM backups WHERE status != 'skipped'
	UNION ALL
	SELECT 'restore', id, app_id, status, '', '', '', created_at FROM restores
	UNION ALL
	SELECT 'task', r.id, t.app_id, r.status, r."trigger", t.name, '', r.created_at
	FROM scheduled_task_runs r JOIN scheduled_tasks t ON t.id = r.scheduled_task_id
	WHERE r.status NOT IN ('succeeded', 'skipped') OR r."trigger" = 'manual'
)
ORDER BY created_at DESC, id DESC
LIMIT ?`

// RecentActivity is the newest activity across every app, newest first.
func RecentActivity(ctx context.Context, limit int) ([]Activity, error) {
	activity := []Activity{}
	err := facades.Orm().WithContext(ctx).Query().Raw(recentActivity, limit).Scan(&activity)

	return activity, err
}

// AppsCPU is every app's CPU over the last hour, by app ID, for the
// dashboard's sparklines. Empty without a metrics file.
type AppsCPU struct {
	metrics.Window
	Apps map[string][]metrics.Point `json:"apps"`
}

func RecentAppsCPU(ctx context.Context, now time.Time) (*AppsCPU, error) {
	r := metrics.ParseRange("1h")
	cpu := &AppsCPU{Window: metrics.WindowFor(r, now), Apps: map[string][]metrics.Point{}}
	db, err := MetricsDB()
	if err != nil {
		return cpu, nil // logged when it was opened
	}
	rows, err := metrics.Query(ctx, db, metrics.AppTable, []string{"cpu_percent"}, "", nil, r, now)
	if err != nil {
		return nil, err
	}
	for _, key := range rows.Keys {
		cpu.Apps[key] = rows.Get(key, "cpu_percent")
	}

	return cpu, nil
}

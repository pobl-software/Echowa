package feature

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"goravel/bootstrap"
)

// The metrics tasks run when ADR 0024 says, and never two at once.
func TestMetricsAreScheduled(t *testing.T) {
	crons := map[string]string{}
	for _, event := range bootstrap.Schedule() {
		crons[event.GetName()] = event.GetCron()
		if event.GetName() != "echowa:prune-images" && event.GetName() != "echowa:check-for-update" {
			assert.True(t, event.GetSkipIfStillRunning(), event.GetName())
		}
	}
	assert.Equal(t, "*/15 * * * * *", crons["echowa:read-metrics"])
	assert.Equal(t, "5 * * * *", crons["echowa:rollup-metrics-hourly"])
	assert.Equal(t, "15 00 * * *", crons["echowa:rollup-metrics-daily"])
	assert.Equal(t, "30 * * * *", crons["echowa:prune-metrics"])
}

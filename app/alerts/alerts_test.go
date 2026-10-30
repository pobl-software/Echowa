package alerts

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/models"
)

func app(template string) *models.App {
	if template == "" {
		return &models.App{Name: "blog"}
	}

	return &models.App{Name: "db", Template: &template}
}

func TestEachTriggerFitsTheAppsItIsAbout(t *testing.T) {
	custom, postgres, mysql, redis := app(""), app("postgres"), app("mysql"), app("redis")
	for _, tc := range []struct {
		trigger string
		fits    []*models.App
		doesnt  []*models.App
	}{
		{DeployFailed, []*models.App{custom, postgres, mysql, redis}, nil},
		{KilledForMemory, []*models.App{custom, redis}, nil},
		{BackupFailed, []*models.App{postgres, mysql}, []*models.App{custom, redis}},
		{RestoreSucceeded, []*models.App{postgres, mysql}, []*models.App{custom, redis}},
		{TaskFailed, []*models.App{custom}, []*models.App{postgres, redis}},
		{EchowaStarted, nil, []*models.App{custom, postgres}},
	} {
		trigger, ok := Find(tc.trigger)
		require.True(t, ok, tc.trigger)
		for _, a := range tc.fits {
			assert.True(t, trigger.Fits(a), "%s fits %s", tc.trigger, AppKind(a))
		}
		for _, a := range tc.doesnt {
			assert.False(t, trigger.Fits(a), "%s doesn't fit %s", tc.trigger, AppKind(a))
		}
	}
}

func TestTriggerIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, trigger := range All() {
		assert.False(t, seen[trigger.ID], trigger.ID)
		seen[trigger.ID] = true
		assert.NotEmpty(t, trigger.Label, trigger.ID)
	}
	_, ok := Find("disk.used.from-a-newer-release")
	assert.False(t, ok)
}

package alerts

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/models"
)

// check is one check of a condition: its minute, what it saw, and what
// the step should make of it.
type check struct {
	minute  float64
	value   any // a number, nil for not measured, or "stopped"
	phase   Phase
	outcome Outcome
}

func run(t *testing.T, trigger string, rule models.AlertRule, start State, checks []check) State {
	t.Helper()
	tr, ok := Find(trigger)
	require.True(t, ok)
	base := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	s := start
	for _, c := range checks {
		var o Observation
		switch v := c.value.(type) {
		case string:
			o.Stopped = true
		case float64:
			o.Value = &v
		case int:
			f := float64(v)
			o.Value = &f
		}
		var outcome Outcome
		s, outcome = Step(tr, rule, s, o, base.Add(time.Duration(c.minute*float64(time.Minute))))
		label := fmt.Sprintf("minute %v, %v", c.minute, c.value)
		assert.Equal(t, c.phase, s.Phase, label)
		assert.Equal(t, c.outcome, outcome, label)
	}

	return s
}

func threshold(v float64) *float64 { return &v }

func TestAConditionFiresRepeatsAndResolves(t *testing.T) {
	memory := models.AlertRule{Threshold: threshold(90), ForMinutes: 5, RepeatMinutes: 60, NotifyResolved: true}

	for _, tc := range []struct {
		name   string
		rule   models.AlertRule
		checks []check
	}{
		{"fires after the rule's minutes", memory, []check{
			{0, 95, Pending, Quiet},
			{1, 95, Pending, Quiet},
			{4, 95, Pending, Quiet},
			// A check a few seconds early still counts.
			{4.8, 95, Firing, Fire},
			{5, 95, Firing, Quiet},
		}},
		{"the threshold itself isn't a breach", memory, []check{
			{0, 90, OK, Quiet},
			{5, 90, OK, Quiet},
		}},
		{"not measured is neither breach nor clear", memory, []check{
			{0, 95, Pending, Quiet},
			{1, nil, Pending, Quiet},
			{5, 95, Firing, Fire},
			{6, nil, Firing, Quiet},
			{20, nil, Firing, Quiet},
		}},
		{"flapping while pending starts over", memory, []check{
			{0, 95, Pending, Quiet},
			{3, 85, OK, Quiet},
			{4, 95, Pending, Quiet},
			{8, 95, Pending, Quiet},
			{9, 95, Firing, Fire},
		}},
		{"flapping while firing doesn't resolve or fire again", memory, []check{
			{0, 95, Pending, Quiet},
			{5, 95, Firing, Fire},
			{6, 80, Firing, Quiet},
			{9, 80, Firing, Quiet},
			{10, 95, Firing, Quiet},
			{11, 80, Firing, Quiet},
			{15, 80, Firing, Quiet},
			{16, 80, OK, Resolve},
			{17, 95, Pending, Quiet},
		}},
		{"repeats every repeat minutes while it breaches", memory, []check{
			{0, 95, Pending, Quiet},
			{5, 95, Firing, Fire},
			{64, 95, Firing, Quiet},
			{65, 95, Firing, Repeat},
			{66, 95, Firing, Quiet},
			{125, 95, Firing, Repeat},
		}},
		{"never repeats at 0", models.AlertRule{Threshold: threshold(90), ForMinutes: 5}, []check{
			{0, 95, Pending, Quiet},
			{5, 95, Firing, Fire},
			{500, 95, Firing, Quiet},
		}},
		{"a repeat waits while it's clearing", memory, []check{
			{0, 95, Pending, Quiet},
			{5, 95, Firing, Fire},
			{65, 80, Firing, Quiet},
			{66, 95, Firing, Repeat},
		}},
		{"0 minutes fires and resolves at once", models.AlertRule{Threshold: threshold(90)}, []check{
			{0, 95, Firing, Fire},
			{1, 80, OK, Resolve},
		}},
		{"resolving waits at most 5 minutes", models.AlertRule{Threshold: threshold(90), ForMinutes: 30}, []check{
			{0, 95, Pending, Quiet},
			{30, 95, Firing, Fire},
			{31, 80, Firing, Quiet},
			{36, 80, OK, Resolve},
		}},
		{"a stopped app resolves without a message and stays OK", memory, []check{
			{0, 95, Pending, Quiet},
			{5, 95, Firing, Fire},
			{6, "stopped", OK, Quiet},
			{7, "stopped", OK, Quiet},
			{20, "stopped", OK, Quiet},
			{21, 95, Pending, Quiet},
		}},
		{"a stopped app's pending alert never fires", memory, []check{
			{0, 95, Pending, Quiet},
			{4, "stopped", OK, Quiet},
			{5, 95, Pending, Quiet},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run(t, AppMemory, tc.rule, State{}, tc.checks)
		})
	}
}

func TestAConditionCarriesOnAfterARestart(t *testing.T) {
	memory := models.AlertRule{Threshold: threshold(90), ForMinutes: 5, RepeatMinutes: 60}
	firing := run(t, AppMemory, memory, State{}, []check{
		{0, 95, Pending, Quiet},
		{5, 95, Firing, Fire},
		{10, 95, Firing, Quiet},
	})

	// Only what alert_states keeps comes back. The first checks after
	// the restart neither fire again nor resolve, and the repeat keeps
	// its time.
	stored := State{Phase: firing.Phase, Since: firing.Since, NotifiedAt: firing.NotifiedAt, Value: firing.Value}
	run(t, AppMemory, memory, stored, []check{
		{12, nil, Firing, Quiet},
		{13, 95, Firing, Quiet},
		{65, 95, Firing, Repeat},
	})

	clearing := run(t, AppMemory, memory, stored, []check{{14, 80, Firing, Quiet}})
	run(t, AppMemory, memory, clearing, []check{
		{17, nil, Firing, Quiet},
		{19, 80, OK, Resolve},
	})
}

func TestYesNoAndBelowConditions(t *testing.T) {
	run(t, Unhealthy, models.AlertRule{ForMinutes: 2}, State{}, []check{
		{0, 1, Pending, Quiet},
		{0.5, 1, Pending, Quiet},
		{2, 1, Firing, Fire},
		{2.5, 0, Firing, Quiet},
		{4.5, 0, OK, Resolve},
	})
	run(t, CacheHitRate, models.AlertRule{Threshold: threshold(90)}, State{}, []check{
		{0, 95, OK, Quiet},
		{1, 85, Firing, Fire},
		{2, 92, OK, Resolve},
	})
}

func TestDescribeAndFormat(t *testing.T) {
	describe := func(id string, rule models.AlertRule) string {
		tr, ok := Find(id)
		require.True(t, ok)
		return tr.Describe(rule)
	}
	assert.Equal(t, "Memory use above 90% of its limit for 5 minutes", describe(AppMemory, models.AlertRule{Threshold: threshold(90), ForMinutes: 5}))
	assert.Equal(t, "Cache hit rate below 99.5% for 1 hour 30 minutes", describe(CacheHitRate, models.AlertRule{Threshold: threshold(99.5), ForMinutes: 90}))
	assert.Equal(t, "Unhealthy for 2 minutes", describe(Unhealthy, models.AlertRule{ForMinutes: 2}))
	assert.Equal(t, "Transaction ID age above 1,500,000,000", describe(TransactionIDAge, models.AlertRule{Threshold: threshold(1.5e9)}))
	assert.Equal(t, "Longest open transaction above 1 minute", describe(LongestTransaction, models.AlertRule{Threshold: threshold(1)}))

	assert.Equal(t, "Certificate valid for less than 14 days", describe(CertificateExpiry, models.AlertRule{Threshold: threshold(14)}))
	assert.Equal(t, "Certificate valid for less than 1 day", describe(CertificateExpiry, models.AlertRule{Threshold: threshold(1)}))
	assert.Equal(t, "No successful backup for more than 26 hours", describe(BackupAge, models.AlertRule{Threshold: threshold(26)}))

	cert, _ := Find(CertificateExpiry)
	assert.Equal(t, "It expires in 3.5 days.", cert.NowText(3.46))
	assert.Equal(t, "It expired 2 days ago.", cert.NowText(-2))
	age, _ := Find(BackupAge)
	assert.Equal(t, "None for 1 hour.", age.NowText(1))
	memory, _ := Find(AppMemory)
	assert.Equal(t, "Now 93% of its limit.", memory.NowText(92.6))
	unhealthy, _ := Find(Unhealthy)
	assert.Empty(t, unhealthy.NowText(1), "a yes/no condition says what it means in its summary")

	gib, _ := Find(DBSize)
	assert.Equal(t, "2.5 GiB", gib.Format(2.46))
	assert.Equal(t, "12 GiB", gib.Format(12.4))
	evicted, _ := Find(EvictedKeys)
	assert.Equal(t, "0 per second", evicted.Format(0))
	assert.Equal(t, "1,234 per second", evicted.Format(1234.4))
}

func TestEveryConditionCanBeDescribed(t *testing.T) {
	for _, tr := range All() {
		if tr.Kind != KindCondition {
			continue
		}
		rule := models.AlertRule{Threshold: threshold(tr.Threshold), ForMinutes: tr.ForMinutes}
		assert.NotEmpty(t, tr.Describe(rule), tr.ID)
		if tr.Unit == UnitNone {
			assert.NotEmpty(t, tr.Summary, "%s says what it means when it fires", tr.ID)
		}
	}
}

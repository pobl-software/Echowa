package tasks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func at(t *testing.T, zone, value string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	require.NoError(t, err)
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	require.NoError(t, err)

	return parsed
}

// The presets the page offers.
func TestPresetsAreDueWhenTheyShouldBe(t *testing.T) {
	tests := []struct {
		name string
		expr string
		due  []string
		not  []string
	}{
		{"every minute", "* * * * *", []string{"2027-01-05 10:00", "2027-01-05 10:01"}, nil},
		{"every hour", "0 * * * *", []string{"2027-01-05 10:00", "2027-01-05 00:00"}, []string{"2027-01-05 10:01", "2027-01-05 10:59"}},
		{"every day at midnight", "0 0 * * *", []string{"2027-01-05 00:00"}, []string{"2027-01-05 00:01", "2027-01-05 12:00"}},
		{"every Sunday at midnight", "0 0 * * 0", []string{"2027-01-03 00:00"}, []string{"2027-01-04 00:00", "2027-01-03 00:01"}},
		{"the 1st of every month", "0 0 1 * *", []string{"2027-02-01 00:00"}, []string{"2027-02-02 00:00", "2027-02-01 01:00"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Parse(tt.expr, "UTC")
			require.NoError(t, err)
			for _, value := range tt.due {
				assert.True(t, s.Due(at(t, "UTC", value)), "due at %s", value)
			}
			for _, value := range tt.not {
				assert.False(t, s.Due(at(t, "UTC", value)), "not due at %s", value)
			}
		})
	}
}

func TestDueTakesAnyTimeInTheMinute(t *testing.T) {
	s, err := Parse("30 9 * * *", "UTC")
	require.NoError(t, err)

	assert.True(t, s.Due(at(t, "UTC", "2027-01-05 09:30").Add(59*time.Second)), "a late tick is still that minute")
	assert.False(t, s.Due(at(t, "UTC", "2027-01-05 09:31")))
}

func TestACustomExpression(t *testing.T) {
	s, err := Parse("*/15 9-17 * * 1-5", "UTC")
	require.NoError(t, err)

	assert.True(t, s.Due(at(t, "UTC", "2027-01-05 09:45")), "a Tuesday in hours")
	assert.False(t, s.Due(at(t, "UTC", "2027-01-05 09:50")))
	assert.False(t, s.Due(at(t, "UTC", "2027-01-05 18:00")), "after hours")
	assert.False(t, s.Due(at(t, "UTC", "2027-01-09 10:00")), "a Saturday")
}

func TestTheScheduleIsReadInItsZone(t *testing.T) {
	for _, zone := range []string{"Europe/London", "America/New_York", "Asia/Kathmandu"} {
		t.Run(zone, func(t *testing.T) {
			s, err := Parse("0 0 * * *", zone)
			require.NoError(t, err)

			midnight := at(t, zone, "2027-01-05 00:00")
			assert.True(t, s.Due(midnight))
			assert.True(t, s.Due(midnight.UTC()), "the runner's clock can be in any zone")
			if zone != "Europe/London" { // London is on UTC in January
				assert.False(t, s.Due(at(t, "UTC", "2027-01-05 00:00")), "not UTC's midnight")
			}
		})
	}
}

// Europe/London goes from 01:00 GMT to 02:00 BST on 28 March 2027, and
// back from 02:00 BST to 01:00 GMT on 31 October 2027.
func TestDaylightSaving(t *testing.T) {
	s, err := Parse("30 1 * * *", "Europe/London")
	require.NoError(t, err)

	var runs []time.Time
	for m := at(t, "UTC", "2027-03-27 00:00"); m.Before(at(t, "UTC", "2027-03-30 00:00")); m = m.Add(time.Minute) {
		if s.Due(m) {
			runs = append(runs, m.UTC())
		}
	}
	assert.Equal(t, []time.Time{at(t, "UTC", "2027-03-27 01:30"), at(t, "UTC", "2027-03-29 00:30")}, runs,
		"01:30 doesn't exist on the 28th, so it doesn't run that day")

	runs = nil
	for m := at(t, "UTC", "2027-10-31 00:00"); m.Before(at(t, "UTC", "2027-10-31 03:00")); m = m.Add(time.Minute) {
		if s.Due(m) {
			runs = append(runs, m.UTC())
		}
	}
	assert.Equal(t, []time.Time{at(t, "UTC", "2027-10-31 00:30"), at(t, "UTC", "2027-10-31 01:30")}, runs,
		"01:30 happens twice on the 31st, so it runs twice")
}

func TestNextListsTimesInTheZone(t *testing.T) {
	s, err := Parse("0 9 * * 1", "America/New_York")
	require.NoError(t, err)

	next := s.Next(at(t, "UTC", "2027-01-05 00:00"), 3)

	require.Len(t, next, 3)
	assert.Equal(t, "2027-01-11 09:00 EST", next[0].Format("2006-01-02 15:04 MST"))
	assert.Equal(t, "2027-01-18 09:00 EST", next[1].Format("2006-01-02 15:04 MST"))
	assert.Equal(t, "2027-01-25 09:00 EST", next[2].Format("2006-01-02 15:04 MST"))
}

func TestParseRefuses(t *testing.T) {
	for _, tt := range []struct{ expr, zone string }{
		{"", "UTC"},
		{"* * * *", "UTC"},
		{"0 * * * * *", "UTC"},
		{"@hourly", "UTC"},
		{"@every 5m", "UTC"},
		{"CRON_TZ=UTC 0 * * *", "UTC"},
		{"TZ=UTC 0 * * * *", "UTC"},
		{"60 * * * *", "UTC"},
		{"* 24 * * *", "UTC"},
		{"0 0 30 2 *", "UTC"},
		{"nonsense", "UTC"},
		{"* * * * *", ""},
		{"* * * * *", "Local"},
		{"* * * * *", "Mars/Olympus"},
		{"* * * * *", "../../etc/passwd"},
	} {
		_, err := Parse(tt.expr, tt.zone)
		assert.Error(t, err, "%q in %q", tt.expr, tt.zone)
	}
}

func TestParseTakesExtraSpaces(t *testing.T) {
	_, err := Parse("  */5   *  * * *  ", "UTC")
	assert.NoError(t, err)
}

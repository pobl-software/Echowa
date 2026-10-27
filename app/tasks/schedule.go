// Package tasks reads scheduled tasks' schedules (ADR 0032): five-field
// cron expressions, each in its own zone. The rules, the runner and the
// page all read them through Parse, so they agree on what's valid and when
// it's due.
package tasks

import (
	"errors"
	"fmt"
	"strings"
	"time"
	// Zones come with the binary, so the ones Echowa accepts don't depend
	// on the server's tzdata package.
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

// Timeouts a task may have, in minutes.
const (
	DefaultTimeoutMinutes = 15
	MaxTimeoutMinutes     = 24 * 60
)

// Descriptors (@hourly, @every) aren't accepted: presets are plain
// expressions, and @every has no fixed minute to be due at.
var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Schedule is a parsed expression in its zone.
type Schedule struct {
	spec *cron.SpecSchedule
}

// Parse reads a five-field cron expression, such as "*/5 * * * *", in the
// IANA zone named by zone.
func Parse(expr, zone string) (Schedule, error) {
	loc, err := LoadZone(zone)
	if err != nil {
		return Schedule{}, err
	}
	// Five fields also turns away CRON_TZ= prefixes: the zone is its own
	// field.
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return Schedule{}, errors.New("a schedule has five fields: minute, hour, day of month, month and day of week")
	}
	parsed, err := parser.Parse(strings.Join(fields, " "))
	if err != nil {
		return Schedule{}, err
	}
	spec := parsed.(*cron.SpecSchedule)
	spec.Location = loc
	// The parser takes dates that don't exist, such as the 30th of
	// February, and Next gives up after five years without one.
	if spec.Next(time.Now()).IsZero() {
		return Schedule{}, errors.New("this schedule never runs")
	}

	return Schedule{spec: spec}, nil
}

// LoadZone loads an IANA zone such as "Europe/London" or "UTC". The empty
// name and "Local" are refused: they'd mean the server's zone, which the
// admin can't see.
func LoadZone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("unknown time zone %q", name)
	}

	return time.LoadLocation(name)
}

// Due reports whether the schedule fires in the minute holding t.
func (s Schedule) Due(t time.Time) bool {
	minute := t.Truncate(time.Minute)

	return s.spec.Next(minute.Add(-time.Second)).Equal(minute)
}

// Next returns the schedule's next n times after after, in its zone.
func (s Schedule) Next(after time.Time, n int) []time.Time {
	times := make([]time.Time, 0, n)
	t := after.In(s.spec.Location)
	for range n {
		t = s.spec.Next(t)
		if t.IsZero() {
			break
		}
		times = append(times, t)
	}

	return times
}

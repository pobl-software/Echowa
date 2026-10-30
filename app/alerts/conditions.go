package alerts

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"goravel/app/models"
)

// Unit is what a condition's figure and threshold are measured in.
type Unit string

const (
	// UnitNone is a yes/no condition, such as unhealthy: its figure is 1
	// while true, and it has no threshold.
	UnitNone      Unit = ""
	UnitPercent   Unit = "percent"
	UnitGiB       Unit = "gib"
	UnitMinutes   Unit = "minutes"
	UnitCount     Unit = "count"
	UnitPerSecond Unit = "per_second"
)

// Phase is where a condition is for one subject (ADR 0040).
type Phase string

const (
	OK      Phase = "ok"
	Pending Phase = "pending"
	Firing  Phase = "firing"
)

// State is a condition rule's state for one subject, as alert_states
// keeps it. A zero time is unset.
type State struct {
	Phase Phase
	// Since is when it entered Phase.
	Since time.Time
	// ClearSince is when a firing condition's figure stopped breaching.
	// Zero while it breaches.
	ClearSince time.Time
	// NotifiedAt is when it last fired or repeated.
	NotifiedAt time.Time
	// Value is the last figure measured.
	Value *float64
}

// Observation is a condition's figure for one subject at one check.
type Observation struct {
	// Value is nil when it wasn't measured, which is neither breach nor
	// clear.
	Value *float64
	// Stopped is an app that wants no replicas (ADR 0036). It resolves
	// without a message and stays OK until it starts.
	Stopped bool
}

// Outcome is what a step has to tell the rule's providers.
type Outcome int

const (
	Quiet Outcome = iota
	Fire
	Repeat
	Resolve
)

const (
	// maxClear is the longest a firing condition must be clear to resolve.
	maxClear = 5 * time.Minute
	// slack lets a check that runs a little early count: the checks run
	// every minute or 30 seconds, never exactly.
	slack = 15 * time.Second
)

// Step moves a condition rule's state for one subject on by one check at
// now. It fires after the figure breaches for the rule's minutes, repeats
// every repeat minutes while it still breaches, and resolves after it's
// clear for the rule's minutes, at most 5.
func Step(t Trigger, rule models.AlertRule, s State, o Observation, now time.Time) (State, Outcome) {
	if s.Phase == "" {
		s.Phase = OK
	}
	if o.Stopped {
		if s.Phase == OK {
			return s, Quiet
		}
		return State{Phase: OK, Since: now, Value: s.Value}, Quiet
	}
	if o.Value == nil {
		return s, Quiet
	}
	s.Value = o.Value
	breach := t.Breaches(rule, *o.Value)
	hold := time.Duration(rule.ForMinutes) * time.Minute

	switch s.Phase {
	case Firing:
		if breach {
			s.ClearSince = time.Time{}
			if rule.RepeatMinutes > 0 && reached(s.NotifiedAt, time.Duration(rule.RepeatMinutes)*time.Minute, now) {
				s.NotifiedAt = now
				return s, Repeat
			}
			return s, Quiet
		}
		if s.ClearSince.IsZero() {
			s.ClearSince = now
		}
		if !reached(s.ClearSince, min(hold, maxClear), now) {
			return s, Quiet
		}
		return State{Phase: OK, Since: now, Value: s.Value}, Resolve
	case Pending:
		if !breach {
			return State{Phase: OK, Since: now, Value: s.Value}, Quiet
		}
	default:
		if !breach {
			return s, Quiet
		}
		s = State{Phase: Pending, Since: now, Value: s.Value}
	}
	if !reached(s.Since, hold, now) {
		return s, Quiet
	}

	return State{Phase: Firing, Since: now, NotifiedAt: now, Value: s.Value}, Fire
}

func reached(since time.Time, d time.Duration, now time.Time) bool {
	return now.Sub(since) >= d-slack
}

// Breaches says whether v is past the rule's threshold.
func (t Trigger) Breaches(rule models.AlertRule, v float64) bool {
	if t.Unit == UnitNone {
		return v > 0
	}
	threshold := t.Threshold
	if rule.Threshold != nil {
		threshold = *rule.Threshold
	}
	if t.Below {
		return v < threshold
	}

	return v > threshold
}

// Describe is a condition rule in words, as its list and its messages
// show it: "Memory use above 90% of its limit for 5 minutes".
func (t Trigger) Describe(rule models.AlertRule) string {
	words := []string{t.Label}
	if t.Unit != UnitNone {
		threshold := t.Threshold
		if rule.Threshold != nil {
			threshold = *rule.Threshold
		}
		direction := "above"
		if t.Below {
			direction = "below"
		}
		words = append(words, direction, t.withUnit(exact(threshold)))
		if t.Of != "" {
			words = append(words, t.Of)
		}
	}
	if rule.ForMinutes > 0 {
		words = append(words, "for", Minutes(rule.ForMinutes))
	}

	return strings.Join(words, " ")
}

// Format is a measured v in the trigger's unit, rounded: "93%", "12 GiB",
// "17 minutes", "1,500,000,000" or "3.2 per second".
func (t Trigger) Format(v float64) string {
	return t.withUnit(number(v))
}

func (t Trigger) withUnit(n string) string {
	switch t.Unit {
	case UnitPercent:
		return n + "%"
	case UnitGiB:
		return n + " GiB"
	case UnitMinutes:
		if n == "1" {
			return "1 minute"
		}
		return n + " minutes"
	case UnitPerSecond:
		return n + " per second"
	default:
		return n
	}
}

// number is v with one decimal under 10 and none from there, grouped in
// thousands.
func number(v float64) string {
	if math.Abs(v) < 10 && v != math.Trunc(v) {
		return strings.TrimSuffix(strconv.FormatFloat(v, 'f', 1, 64), ".0")
	}

	return grouped(math.Round(v))
}

// exact is a threshold as it was typed: whole numbers grouped in
// thousands, others with their decimals.
func exact(v float64) string {
	if v != math.Trunc(v) {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}

	return grouped(v)
}

func grouped(v float64) string {
	digits := strconv.FormatFloat(math.Abs(v), 'f', 0, 64)
	var b strings.Builder
	if v < 0 {
		b.WriteByte('-')
	}
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}

	return b.String()
}

// Minutes is n minutes in words: "1 minute", "5 minutes", "2 hours",
// "1 hour 30 minutes".
func Minutes(n int) string {
	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}
	if n < 60 {
		return plural(n, "minute")
	}
	if n%60 == 0 {
		return plural(n/60, "hour")
	}

	return plural(n/60, "hour") + " " + plural(n%60, "minute")
}

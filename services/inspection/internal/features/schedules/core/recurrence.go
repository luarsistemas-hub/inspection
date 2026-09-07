// Package core owns recurrence and schedule lifecycle rules shared by schedule operations.
package core

import (
	"fmt"
	"strings"
	"time"

	rrule "github.com/teambition/rrule-go"
)

// Recurrence is a validated daily-or-slower rule bound to an IANA timezone.
type Recurrence struct {
	rule   *rrule.RRule
	loc    *time.Location
	hour   int
	minute int
	second int
}

func ParseRecurrence(value, timezone string, startsAt time.Time) (Recurrence, error) {
	loc, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil || strings.TrimSpace(timezone) == "" {
		return Recurrence{}, fmt.Errorf("invalid IANA timezone")
	}
	option, err := rrule.StrToROptionInLocation(strings.TrimSpace(value), loc)
	if err != nil {
		return Recurrence{}, fmt.Errorf("invalid RRULE: %w", err)
	}
	// rrule-go orders frequencies from YEARLY to SECONDLY, so values above
	// DAILY are the sub-daily frequencies that the product does not support.
	if option.Freq > rrule.DAILY {
		return Recurrence{}, fmt.Errorf("recurrence cadence must be daily or slower")
	}
	if len(option.Byhour) > 1 || len(option.Byminute) > 1 || len(option.Bysecond) > 1 {
		return Recurrence{}, fmt.Errorf("recurrence must produce at most one instant per day")
	}
	if startsAt.IsZero() {
		return Recurrence{}, fmt.Errorf("start instant is required")
	}
	option.Dtstart = startsAt.In(loc)
	parsed, err := rrule.NewRRule(*option)
	if err != nil {
		return Recurrence{}, fmt.Errorf("invalid RRULE: %w", err)
	}
	hour, minute, second := startsAt.In(loc).Clock()
	return Recurrence{rule: parsed, loc: loc, hour: hour, minute: minute, second: second}, nil
}

// Next returns the first occurrence strictly after the supplied instant.
func (r Recurrence) Next(after time.Time) (time.Time, error) {
	if r.rule == nil || r.loc == nil {
		return time.Time{}, fmt.Errorf("uninitialized recurrence")
	}
	candidate := r.rule.After(after.In(r.loc), false)
	if candidate.IsZero() {
		return time.Time{}, fmt.Errorf("recurrence has no next occurrence")
	}
	year, month, day := candidate.In(r.loc).Date()
	return resolveCivil(year, month, day, r.hour, r.minute, r.second, r.loc), nil
}

// resolveCivil selects the earliest instant for an overlapping wall clock and
// shifts a nonexistent wall clock forward to the first valid minute.
func resolveCivil(y int, m time.Month, d, hour, minute, second int, loc *time.Location) time.Time {
	wanted := hour*3600 + minute*60 + second
	start := time.Date(y, m, d, 0, 0, second, 0, time.UTC).Add(-14 * time.Hour)
	end := start.Add(52 * time.Hour)
	var firstAfter time.Time
	for instant := start; !instant.After(end); instant = instant.Add(time.Minute) {
		local := instant.In(loc)
		ly, lm, ld := local.Date()
		if ly != y || lm != m || ld != d {
			continue
		}
		lh, lmin, ls := local.Clock()
		civil := lh*3600 + lmin*60 + ls
		if civil == wanted {
			return instant.In(loc)
		}
		if civil > wanted && firstAfter.IsZero() {
			firstAfter = instant.In(loc)
		}
	}
	if !firstAfter.IsZero() {
		return firstAfter
	}
	return time.Date(y, m, d, hour, minute, second, 0, loc)
}

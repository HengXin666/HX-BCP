// Package cron implements a minimal 5-field cron expression parser
// (minute hour day-of-month month day-of-week) with `*`, lists, ranges and
// steps. It exists so subscription refresh schedules do not need an external
// dependency; times are evaluated in UTC.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type field struct {
	// set is a bitmask of allowed values.
	set uint64
	// wildcard records whether the field was `*` (relevant for the
	// day-of-month / day-of-week OR rule).
	wildcard bool
}

// Schedule is a parsed cron expression.
type Schedule struct {
	minute, hour, dayOfMonth, month, dayOfWeek field
	source                                     string
}

type bounds struct{ min, max int }

var fieldBounds = []bounds{
	{0, 59}, // minute
	{0, 23}, // hour
	{1, 31}, // day of month
	{1, 12}, // month
	{0, 6},  // day of week, 0 = Sunday
}

// Parse validates a standard 5-field cron expression.
func Parse(expression string) (*Schedule, error) {
	parts := strings.Fields(strings.TrimSpace(expression))
	if len(parts) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields, got %d", len(parts))
	}
	fields := make([]field, 5)
	for index, part := range parts {
		parsed, err := parseField(part, fieldBounds[index])
		if err != nil {
			return nil, fmt.Errorf("cron field %d (%q): %w", index+1, part, err)
		}
		fields[index] = parsed
	}
	return &Schedule{
		minute:     fields[0],
		hour:       fields[1],
		dayOfMonth: fields[2],
		month:      fields[3],
		dayOfWeek:  fields[4],
		source:     strings.Join(parts, " "),
	}, nil
}

func (s *Schedule) String() string { return s.source }

// Next returns the first matching time strictly after the given time.
// The scan is bounded to four years, which covers every valid expression.
func (s *Schedule) Next(after time.Time) time.Time {
	candidate := after.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := candidate.AddDate(4, 0, 0)
	for candidate.Before(limit) {
		if !s.month.contains(int(candidate.Month())) {
			candidate = time.Date(candidate.Year(), candidate.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
			continue
		}
		if !s.matchesDay(candidate) {
			candidate = candidate.Truncate(24 * time.Hour).Add(24 * time.Hour)
			continue
		}
		if !s.hour.contains(candidate.Hour()) {
			candidate = candidate.Truncate(time.Hour).Add(time.Hour)
			continue
		}
		if !s.minute.contains(candidate.Minute()) {
			candidate = candidate.Add(time.Minute)
			continue
		}
		return candidate
	}
	return time.Time{}
}

// matchesDay implements the POSIX rule: when both day-of-month and
// day-of-week are restricted, a date matches if either field matches.
func (s *Schedule) matchesDay(t time.Time) bool {
	domMatch := s.dayOfMonth.contains(t.Day())
	dowMatch := s.dayOfWeek.contains(int(t.Weekday()))
	switch {
	case s.dayOfMonth.wildcard && s.dayOfWeek.wildcard:
		return true
	case s.dayOfMonth.wildcard:
		return dowMatch
	case s.dayOfWeek.wildcard:
		return domMatch
	default:
		return domMatch || dowMatch
	}
}

func (f field) contains(value int) bool {
	return f.set&(1<<uint(value)) != 0
}

func parseField(expression string, limits bounds) (field, error) {
	result := field{wildcard: expression == "*"}
	for _, part := range strings.Split(expression, ",") {
		if part == "" {
			return field{}, fmt.Errorf("empty list item")
		}
		rangePart := part
		step := 1
		if index := strings.IndexByte(part, '/'); index >= 0 {
			rangePart = part[:index]
			parsedStep, err := strconv.Atoi(part[index+1:])
			if err != nil || parsedStep < 1 {
				return field{}, fmt.Errorf("invalid step %q", part[index+1:])
			}
			step = parsedStep
		}
		low, high := limits.min, limits.max
		switch {
		case rangePart == "*":
		case strings.Contains(rangePart, "-"):
			pieces := strings.SplitN(rangePart, "-", 2)
			parsedLow, err := strconv.Atoi(pieces[0])
			if err != nil {
				return field{}, fmt.Errorf("invalid range start %q", pieces[0])
			}
			parsedHigh, err := strconv.Atoi(pieces[1])
			if err != nil {
				return field{}, fmt.Errorf("invalid range end %q", pieces[1])
			}
			low, high = parsedLow, parsedHigh
		default:
			value, err := strconv.Atoi(rangePart)
			if err != nil {
				return field{}, fmt.Errorf("invalid value %q", rangePart)
			}
			low, high = value, value
			if step != 1 {
				// "N/step" means "from N to max by step".
				high = limits.max
			}
		}
		if low < limits.min || high > limits.max || low > high {
			return field{}, fmt.Errorf("value out of range %d-%d", limits.min, limits.max)
		}
		for value := low; value <= high; value += step {
			result.set |= 1 << uint(value)
		}
	}
	if result.set == 0 {
		return field{}, fmt.Errorf("field selects no values")
	}
	return result, nil
}

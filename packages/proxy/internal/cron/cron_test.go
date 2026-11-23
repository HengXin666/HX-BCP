package cron

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, expression string) *Schedule {
	t.Helper()
	schedule, err := Parse(expression)
	if err != nil {
		t.Fatalf("parse %q: %v", expression, err)
	}
	return schedule
}

func at(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func TestParseRejectsInvalidExpressions(t *testing.T) {
	invalid := []string{
		"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *",
		"* * 0 * *", "* * 32 * *", "* * * 13 *", "* * * * 7",
		"a * * * *", "*/0 * * * *", "5-1 * * * *", ", * * * *",
	}
	for _, expression := range invalid {
		if _, err := Parse(expression); err == nil {
			t.Fatalf("expected error for %q", expression)
		}
	}
}

func TestNextBasicPatterns(t *testing.T) {
	cases := []struct {
		expression string
		after      string
		expected   string
	}{
		{"*/15 * * * *", "2026-07-26T10:07:00Z", "2026-07-26T10:15:00Z"},
		{"0 * * * *", "2026-07-26T10:00:00Z", "2026-07-26T11:00:00Z"},
		{"30 3 * * *", "2026-07-26T10:07:00Z", "2026-07-27T03:30:00Z"},
		{"0 0 1 * *", "2026-07-26T10:07:00Z", "2026-08-01T00:00:00Z"},
		{"0 12 * * 1", "2026-07-26T10:07:00Z", "2026-07-27T12:00:00Z"}, // Monday
		{"0 0 29 2 *", "2026-07-26T10:07:00Z", "2028-02-29T00:00:00Z"}, // leap year scan
		{"5,35 8-10 * * *", "2026-07-26T09:05:00Z", "2026-07-26T09:35:00Z"},
	}
	for _, testCase := range cases {
		schedule := mustParse(t, testCase.expression)
		next := schedule.Next(at(testCase.after))
		if !next.Equal(at(testCase.expected)) {
			t.Fatalf("%q after %s: expected %s, got %s",
				testCase.expression, testCase.after, testCase.expected, next.Format(time.RFC3339))
		}
	}
}

func TestNextDayOfMonthOrDayOfWeek(t *testing.T) {
	// POSIX: when both are restricted, either matching day counts.
	schedule := mustParse(t, "0 0 15 * 1")
	// 2026-07-26 is a Sunday; next Monday is 2026-07-27, before the 15th.
	next := schedule.Next(at("2026-07-26T10:00:00Z"))
	if !next.Equal(at("2026-07-27T00:00:00Z")) {
		t.Fatalf("expected day-of-week match first, got %s", next.Format(time.RFC3339))
	}
}

func TestNextIsStrictlyAfter(t *testing.T) {
	schedule := mustParse(t, "0 * * * *")
	exact := at("2026-07-26T10:00:00Z")
	if next := schedule.Next(exact); !next.After(exact) {
		t.Fatalf("Next must be strictly after, got %s", next.Format(time.RFC3339))
	}
}

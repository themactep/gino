package cron

import (
	"testing"
	"time"
)

func TestParseCron(t *testing.T) {
	tests := []struct {
		expr string
		ok   bool
	}{
		{"*/15 9-16 * * 1-5", true},   // every 15min, 9-4pm, weekdays
		{"0 8 * * 1-5", true},          // 8am weekdays
		{"0 17 * * 1-5", true},         // 5pm weekdays
		{"30 9 * * 1-5", true},         // 9:30am weekdays (market open)
		{"0 0 1 1 *", true},            // midnight Jan 1
		{"0,30 * * * *", true},         // every 30 min
		{"*/5 * * * *", true},          // every 5 min
		{"0 0 * * 0", true},            // midnight Sundays
		{"0 0 * * 7", true},            // midnight Sundays (7=Sunday)
		{"0 0 1 * 1-5", true},          // DOM and DOW both restricted
		{"* * * * *", true},            // every minute
		{"60 * * * *", false},          // minute out of range
		{"* 25 * * *", false},          // hour out of range
		{"* * * * 8", false},           // DOW out of range
		{"*/0 * * * *", false},         // step 0 invalid
		{"* * * *", false},             // too few fields
		{"* * * * * *", false},         // too many fields
		{"-5 * * * *", false},          // negative not allowed
		{"a-b * * * *", false},         // non-numeric
	}

	for _, tt := range tests {
		_, err := ParseCron(tt.expr)
		if tt.ok && err != nil {
			t.Errorf("ParseCron(%q) unexpected error: %v", tt.expr, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("ParseCron(%q) expected error, got nil", tt.expr)
		}
	}
}

func TestNextAfterSkipWeekend(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	e, err := ParseCron("0 9 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}

	// Friday 5pm → should fire Monday 9am
	after := time.Date(2026, 7, 10, 17, 0, 0, 0, loc)
	next, err := e.NextAfter(after, loc)
	if err != nil {
		t.Fatal(err)
	}

	if next.Weekday() != time.Monday {
		t.Errorf("expected Monday, got %s (full: %s)", next.Weekday(), next)
	}
	if next.Hour() != 9 || next.Minute() != 0 {
		t.Errorf("expected 09:00, got %02d:%02d", next.Hour(), next.Minute())
	}
}

func TestNextAfterEvery15Min(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	e, err := ParseCron("*/15 9-16 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}

	// Monday 8:55 AM → first fire at 9:00
	after := time.Date(2026, 7, 6, 8, 55, 0, 0, loc) // Monday
	next, err := e.NextAfter(after, loc)
	if err != nil {
		t.Fatal(err)
	}
	if next.Hour() != 9 || next.Minute() != 0 {
		t.Errorf("expected 09:00, got %02d:%02d", next.Hour(), next.Minute())
	}

	// Monday 9:30 → next at 9:45
	after2 := time.Date(2026, 7, 6, 9, 30, 0, 0, loc)
	next2, err := e.NextAfter(after2, loc)
	if err != nil {
		t.Fatal(err)
	}
	if next2.Hour() != 9 || next2.Minute() != 45 {
		t.Errorf("expected 09:45, got %02d:%02d", next2.Hour(), next2.Minute())
	}
}

func TestNextAfterPreMarket(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	e, err := ParseCron("0 8 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}

	after := time.Date(2026, 7, 6, 7, 0, 0, 0, loc) // Monday 7am
	next, err := e.NextAfter(after, loc)
	if err != nil {
		t.Fatal(err)
	}
	if next.Hour() != 8 || next.Minute() != 0 {
		t.Errorf("expected 08:00, got %02d:%02d", next.Hour(), next.Minute())
	}
}

func TestNextAfterDOMDOWOrLogic(t *testing.T) {
	loc := time.UTC
	e, err := ParseCron("0 0 1 * 1-5")
	if err != nil {
		t.Fatal(err)
	}

	// July 1, 2026 is a Wednesday (weekday).
	after := time.Date(2026, 6, 30, 23, 59, 0, 0, loc)
	next, err := e.NextAfter(after, loc)
	if err != nil {
		t.Fatal(err)
	}

	// Should match July 1 since it's both DOM=1 and a weekday.
	if next.Day() != 1 {
		t.Errorf("expected day 1, got %d (full: %s)", next.Day(), next)
	}
}

func TestDayOfWeek7IsSunday(t *testing.T) {
	e, err := ParseCron("0 0 * * 7")
	if err != nil {
		t.Fatal(err)
	}

	loc := time.UTC
	// Saturday → next Sunday midnight
	after := time.Date(2026, 7, 4, 12, 0, 0, 0, loc) // July 4 2026 is a Saturday
	next, err := e.NextAfter(after, loc)
	if err != nil {
		t.Fatal(err)
	}

	if next.Weekday() != time.Sunday {
		t.Errorf("expected Sunday, got %s", next.Weekday())
	}
}

func TestStepWithinRange(t *testing.T) {
	e, err := ParseCron("1-10/3 * * * *")
	if err != nil {
		t.Fatal(err)
	}

	// Should match minutes 1, 4, 7, 10
	expected := map[int]bool{1: true, 4: true, 7: true, 10: true}
	for m := 0; m < 60; m++ {
		got := e.Minute.contains(m)
		want := expected[m]
		if got != want {
			t.Errorf("minute %d: got %v, want %v", m, got, want)
		}
	}
}

func TestNextAfterPostMarketSkipToNextDay(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	e, err := ParseCron("0 17 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}

	// Monday 5:30 PM → next fire is Tuesday 5:00 PM
	after := time.Date(2026, 7, 6, 17, 30, 0, 0, loc)
	next, err := e.NextAfter(after, loc)
	if err != nil {
		t.Fatal(err)
	}
	if next.Weekday() != time.Tuesday {
		t.Errorf("expected Tuesday, got %s", next.Weekday())
	}
	if next.Hour() != 17 || next.Minute() != 0 {
		t.Errorf("expected 17:00, got %02d:%02d", next.Hour(), next.Minute())
	}
}

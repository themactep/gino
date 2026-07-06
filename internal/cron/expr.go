package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronField holds the allowed values for a single cron field.
type CronField struct {
	values map[int]bool // set of allowed values
}

// contains reports whether v is allowed by this field.
func (f *CronField) contains(v int) bool {
	return f.values[v]
}

// CronExpr represents a parsed 5-field cron expression.
type CronExpr struct {
	Minute    CronField // 0-59
	Hour      CronField // 0-23
	DayOfMonth CronField // 1-31
	Month     CronField // 1-12
	DayOfWeek CronField // 0-6 (0=Sunday)
}

// ParseCron parses a standard 5-field cron expression.
//
// Fields: minute hour day-of-month month day-of-week
//
// Supported syntax per field:
//   - *           (wildcard)
//   - 5           (single value)
//   - 1-5         (range)
//   - 1,3,5       (list)
//   - */15        (step from range min)
//   - 1-10/2      (step within range)
func ParseCron(expr string) (*CronExpr, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron: expected 5 fields, got %d", len(fields))
	}

	e := &CronExpr{}

	min, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("cron: minute field %q: %w", fields[0], err)
	}
	e.Minute = min

	hour, err := parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("cron: hour field %q: %w", fields[1], err)
	}
	e.Hour = hour

	dom, err := parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("cron: day-of-month field %q: %w", fields[2], err)
	}
	e.DayOfMonth = dom

	month, err := parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("cron: month field %q: %w", fields[3], err)
	}
	e.Month = month

	// Day of week: allow both 0-6 (Sunday=0) and 7 for Sunday.
	dow, err := parseFieldWithSunday7(fields[4], 0, 7)
	if err != nil {
		return nil, fmt.Errorf("cron: day-of-week field %q: %w", fields[4], err)
	}
	e.DayOfWeek = dow

	return e, nil
}

// parseField parses a single cron field with the given min/max bounds.
func parseField(s string, min, max int) (CronField, error) {
	return parseFieldGeneral(s, min, max, false)
}

// parseFieldWithSunday7 is like parseField but normalizes 7 → 0 (Sunday).
func parseFieldWithSunday7(s string, min, max int) (CronField, error) {
	return parseFieldGeneral(s, min, max, true)
}

func parseFieldGeneral(s string, min, max int, sunday7 bool) (CronField, error) {
	f := CronField{values: make(map[int]bool)}

	// Handle comma-separated lists.
	for _, part := range strings.Split(s, ",") {
		vals, err := parseFieldPart(part, min, max)
		if err != nil {
			return f, err
		}
		for _, v := range vals {
			if sunday7 && v == 7 {
				v = 0
			}
			f.values[v] = true
		}
	}

	return f, nil
}

// parseFieldPart parses one component of a field (no commas).
func parseFieldPart(s string, min, max int) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty value")
	}

	// Check for step: "range/step" or "*/step"
	var rangePart, stepStr string
	if idx := strings.Index(s, "/"); idx >= 0 {
		rangePart = s[:idx]
		stepStr = s[idx+1:]
	} else {
		rangePart = s
	}

	// Parse step
	step := 1
	if stepStr != "" {
		var err error
		step, err = strconv.Atoi(stepStr)
		if err != nil || step < 1 {
			return nil, fmt.Errorf("invalid step %q", stepStr)
		}
	}

	// Determine range bounds
	lo, hi := min, max
	if rangePart != "*" && rangePart != "" {
		if dashIdx := strings.Index(rangePart, "-"); dashIdx >= 0 {
			// Explicit range: A-B
			a, err := strconv.Atoi(rangePart[:dashIdx])
			if err != nil {
				return nil, fmt.Errorf("invalid range start %q", rangePart[:dashIdx])
			}
			b, err := strconv.Atoi(rangePart[dashIdx+1:])
			if err != nil {
				return nil, fmt.Errorf("invalid range end %q", rangePart[dashIdx+1:])
			}
			if a < min || b > max || a > b {
				return nil, fmt.Errorf("range %d-%d out of bounds [%d,%d] or inverted", a, b, min, max)
			}
			lo, hi = a, b
		} else {
			// Single value
			v, err := strconv.Atoi(rangePart)
			if err != nil {
				return nil, fmt.Errorf("invalid value %q", rangePart)
			}
			// Validate against bounds (with special case for DOW 7=Sunday)
			if max == 7 && v == 7 {
				v = 0 // normalize later, but bounds check against 7
			} else if v < min || v > max {
				return nil, fmt.Errorf("value %d out of bounds [%d,%d]", v, min, max)
			}
			if stepStr != "" {
				// "5/15" means start at 5, step by 15
				lo = v
				// For DOW (max==7), hi stays at 7 so 0 and 7 both map to Sunday.
			} else {
				return []int{v}, nil
			}
		}
	}

	// Generate values for range with step
	var result []int
	for v := lo; v <= hi; v += step {
		result = append(result, v)
	}
	return result, nil
}

// NextAfter computes the next time after `after` that satisfies the cron
// expression, evaluated in the given location (timezone). The returned time
// is strictly greater than `after`.
//
// If no matching time is found within ~5 years, an error is returned.
func (e *CronExpr) NextAfter(after time.Time, loc *time.Location) (time.Time, error) {
	// Start one second after the reference, truncated to the minute boundary.
	// We round up to the next minute to ensure we don't match the same minute.
	t := after.In(loc).Add(time.Minute - time.Duration(after.Second())*time.Second - time.Duration(after.Nanosecond()))
	// Truncate to minute.
	t = t.Truncate(time.Minute)

	// Brute-force search with a generous limit.
	limit := after.AddDate(5, 0, 0) // 5 years

	for t.Before(limit) {
		if e.matches(t) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}

	return time.Time{}, fmt.Errorf("cron: no matching time found within 5 years")
}

// matches checks whether the given time satisfies all cron fields.
func (e *CronExpr) matches(t time.Time) bool {
	return e.Minute.contains(t.Minute()) &&
		e.Hour.contains(t.Hour()) &&
		e.Month.contains(int(t.Month())) &&
		e.dayMatches(t)
}

// dayMatches handles the special semantics of day-of-month and day-of-week.
//
// Per POSIX: if both day-of-month and day-of-week are restricted (not *),
// the command is executed when either field matches (OR logic).
// If only one is restricted, it must match. If neither is restricted, always matches.
func (e *CronExpr) dayMatches(t time.Time) bool {
	domMatch := e.DayOfMonth.contains(t.Day())
	dowMatch := e.DayOfWeek.contains(int(t.Weekday()))

	// Check if DOM was explicitly restricted (not all days 1-31).
	domRestricted := !e.allDaysOfMonth()
	// Check if DOW was explicitly restricted (not all days 0-6).
	dowRestricted := !e.allDaysOfWeek()

	if domRestricted && dowRestricted {
		return domMatch || dowMatch // OR logic
	}
	if domRestricted {
		return domMatch
	}
	if dowRestricted {
		return dowMatch
	}
	return true // neither restricted
}

// allDaysOfMonth returns true if the DOM field allows every day.
func (e *CronExpr) allDaysOfMonth() bool {
	for d := 1; d <= 31; d++ {
		if !e.DayOfMonth.contains(d) {
			return false
		}
	}
	return true
}

// allDaysOfWeek returns true if the DOW field allows every weekday.
func (e *CronExpr) allDaysOfWeek() bool {
	for d := 0; d <= 6; d++ {
		if !e.DayOfWeek.contains(d) {
			return false
		}
	}
	return true
}

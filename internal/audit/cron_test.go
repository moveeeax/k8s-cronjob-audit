package audit

import (
	"testing"
	"time"
)

func TestParseScheduleGap(t *testing.T) {
	cases := []struct {
		expr string
		gap  int
	}{
		{"* * * * *", 1},
		{"*/5 * * * *", 5},
		{"*/2 * * * *", 2},
		{"0 * * * *", 60},
		{"0 2 * * *", 1440},
		{"0,30 * * * *", 30},
		{"15 3 * * *", 1440},
		{"59 23 * * *", 1440},
		{"0,59 23 * * *", 59},
		{"@daily", 1440},
		{"@hourly", 60},
		{"@weekly", 1440},
		{"@every 90s", 1},
		{"@every 10m", 10},
		{"30 1 * * SUN", 1440},
		{"0 0 1 JAN *", 1440},
		{"5-10 * * * *", 1},
		{"0 9-17/4 * * *", 240},
	}
	for _, tc := range cases {
		s, err := ParseSchedule(tc.expr)
		if err != nil {
			t.Fatalf("ParseSchedule(%q) returned error: %v", tc.expr, err)
		}
		if s.MinGapMinutes != tc.gap {
			t.Errorf("ParseSchedule(%q) gap = %d, want %d", tc.expr, s.MinGapMinutes, tc.gap)
		}
	}
}

func TestParseScheduleErrors(t *testing.T) {
	cases := []string{
		"",
		"* * * *",
		"* * * * * *",
		"*/5 25 * * *",
		"60 * * * *",
		"0 0 32 * *",
		"0 0 * 13 *",
		"0 0 * * 8",
		"every minute",
		"@sometimes",
		"@every nope",
		"@every 0s",
		"*/0 * * * *",
		"10-5 * * * *",
		"0 0 * JANUARYISH *",
	}
	for _, expr := range cases {
		if _, err := ParseSchedule(expr); err == nil {
			t.Errorf("ParseSchedule(%q) accepted an invalid expression", expr)
		}
	}
}

func TestParseScheduleDayOfWeekSevenFoldsToSunday(t *testing.T) {
	// 0 and 7 are both Sunday; folding them keeps "0,7" from looking like two
	// separate firing days.
	if _, err := ParseSchedule("0 0 * * 0,7"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseScheduleStripsTimeZonePrefix(t *testing.T) {
	// The upstream parser accepts the prefix; the API rejects it. Parsing it
	// here keeps bad-schedule quiet so the timezone rule owns the finding.
	for expr, zone := range map[string]string{
		"CRON_TZ=America/New_York 0 6 * * *": "America/New_York",
		"TZ=Etc/UTC */5 * * * *":             "Etc/UTC",
	} {
		s, err := ParseSchedule(expr)
		if err != nil {
			t.Fatalf("ParseSchedule(%q): %v", expr, err)
		}
		if s.TZPrefix != zone {
			t.Errorf("ParseSchedule(%q).TZPrefix = %q, want %q", expr, s.TZPrefix, zone)
		}
	}
	if _, err := ParseSchedule("CRON_TZ=Etc/UTC"); err == nil {
		t.Error("a prefix with no fields after it should not parse")
	}
	if _, err := ParseSchedule("TZ=Etc/UTC 0 25 * * *"); err == nil {
		t.Error("the fields after the prefix should still be validated")
	}
}

func TestScheduleFixedHours(t *testing.T) {
	cases := map[string]bool{
		"0 2 * * *":    true,
		"0 */6 * * *":  true,
		"0 9-17 * * *": true,
		"@daily":       true,
		"*/5 * * * *":  false,
		"@hourly":      false,
		"@every 6h":    false,
	}
	for expr, want := range cases {
		s, err := ParseSchedule(expr)
		if err != nil {
			t.Fatalf("ParseSchedule(%q): %v", expr, err)
		}
		if got := s.FixedHours(); got != want {
			t.Errorf("ParseSchedule(%q).FixedHours() = %v, want %v", expr, got, want)
		}
	}
}

func TestScheduleMatchesDayFieldsLikeCron(t *testing.T) {
	// 2026-03-08 is a Sunday, the 8th.
	sunday8 := time.Date(2026, 3, 8, 2, 0, 0, 0, time.UTC)
	cases := map[string]bool{
		"0 2 * * *":   true,
		"0 2 8 * *":   true,
		"0 2 * * 0":   true,
		"0 2 * * 1":   false,
		"0 2 1 * *":   false,
		"0 2 1 * 0":   true, // both day fields restricted: either one matches
		"0 2 1 * 1":   false,
		"0 2 * 4 *":   false,
		"1 2 * * *":   false,
		"@every 1m":   false,
		"0 2 8 MAR *": true,
	}
	for expr, want := range cases {
		s, err := ParseSchedule(expr)
		if err != nil {
			t.Fatalf("ParseSchedule(%q): %v", expr, err)
		}
		if got := s.Matches(sunday8); got != want {
			t.Errorf("ParseSchedule(%q).Matches(%s) = %v, want %v", expr, sunday8, got, want)
		}
	}
}

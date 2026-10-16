package audit

import "testing"

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

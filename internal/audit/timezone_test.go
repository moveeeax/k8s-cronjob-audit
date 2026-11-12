package audit

import (
	"testing"
	"time"
)

func TestValidateTimeZone(t *testing.T) {
	for _, tz := range []string{"Etc/UTC", "UTC", "Europe/Berlin", "America/Argentina/Buenos_Aires", "Australia/Lord_Howe", "Etc/GMT+5"} {
		if _, err := ValidateTimeZone(tz); err != nil {
			t.Errorf("ValidateTimeZone(%q) = %v, want valid", tz, err)
		}
	}
	// Each of these is refused by the API server's own check.
	for _, tz := range []string{"", "Local", "local", "Europe/Atlantis", "../etc/passwd", "-Europe/Berlin", "Europe//Berlin", "America/Some_Very_Long_City"} {
		if _, err := ValidateTimeZone(tz); err == nil {
			t.Errorf("ValidateTimeZone(%q) accepted an invalid zone", tz)
		}
	}
}

func dst(t *testing.T, expr, zone string) []dstHit {
	t.Helper()
	sched, err := ParseSchedule(expr)
	if err != nil {
		t.Fatalf("ParseSchedule(%q): %v", expr, err)
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	return dstHits(sched, loc, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
}

func TestDSTHits(t *testing.T) {
	cases := []struct {
		expr, zone string
		want       []string // "skip <date time>" or "twice <date time>"
	}{
		// Berlin jumps 02:00 -> 03:00 on 2026-03-29 and 03:00 -> 02:00 on 2026-10-25.
		{"30 2 * * *", "Europe/Berlin", []string{"skip 2026-03-29 02:30", "twice 2026-10-25 02:30"}},
		{"0 3 * * *", "Europe/Berlin", nil},
		{"0 4 * * 1-5", "Europe/Berlin", nil},
		// New York skips 02:xx on 2026-03-08 and repeats 01:xx on 2026-11-01.
		{"0 1 * * *", "America/New_York", []string{"twice 2026-11-01 01:00"}},
		{"0 2 * * *", "America/New_York", []string{"skip 2026-03-08 02:00"}},
		// A day restriction that misses the change day is safe.
		{"0 2 1 * *", "America/New_York", nil},
		{"0 2 * * 1", "America/New_York", nil},
		// Lord Howe moves by 30 minutes: 02:00 -> 02:30 in October, 02:00 -> 01:30 in April.
		{"15 2 * * *", "Australia/Lord_Howe", []string{"skip 2026-10-04 02:15"}},
		{"45 1 * * *", "Australia/Lord_Howe", []string{"twice 2026-04-05 01:45"}},
		{"30 2 * * *", "Etc/UTC", nil},
	}
	for _, tc := range cases {
		var got []string
		for _, h := range dst(t, tc.expr, tc.zone) {
			kind := "twice"
			if h.skipped {
				kind = "skip"
			}
			got = append(got, kind+" "+h.at.Format("2006-01-02 15:04"))
		}
		if !equal(got, tc.want) {
			t.Errorf("%s in %s: hits = %v, want %v", tc.expr, tc.zone, got, tc.want)
		}
	}
}

package audit

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	// kube-controller-manager ships the Go time zone database as a fallback,
	// so a zone it accepts must not fail here just because the machine running
	// the audit (a scratch CI image, say) has no /usr/share/zoneinfo.
	_ "time/tzdata"
)

// validTimeZoneCharacters is the per-component pattern the API server applies
// to spec.timeZone (pkg/apis/batch/validation in kubernetes/kubernetes).
var validTimeZoneCharacters = regexp.MustCompile(`^[A-Za-z\.\-_0-9+]{1,14}$`)

// ValidateTimeZone mirrors the API server's spec.timeZone validation and
// returns the location on success, so a manifest that would be rejected on
// apply is rejected here with the same reason.
func ValidateTimeZone(tz string) (*time.Location, error) {
	if tz == "" {
		return nil, fmt.Errorf("must be unset or a non-empty zone name")
	}
	for _, part := range strings.Split(tz, "/") {
		if part == "." || part == ".." || strings.HasPrefix(part, "-") || !validTimeZoneCharacters.MatchString(part) {
			return nil, fmt.Errorf("unknown time zone %s", tz)
		}
	}
	if strings.EqualFold(tz, "Local") {
		return nil, fmt.Errorf("must be an explicit IANA zone, not Local")
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, err
	}
	return loc, nil
}

// dstWindow is one stretch of local wall-clock time that a clock change either
// skips (spring forward) or plays twice (fall back).
type dstWindow struct {
	// start is the first affected wall-clock minute, stored in UTC fields so
	// Schedule.Matches reads it as a plain date and time.
	start   time.Time
	minutes int
	skipped bool
}

// dstWindows finds every offset change in loc during the year starting at
// from. It steps an hour at a time and narrows each change down to the second,
// so half-hour shifts such as Australia/Lord_Howe are found too.
func dstWindows(loc *time.Location, from time.Time) []dstWindow {
	var out []dstWindow
	t := from.UTC().Truncate(time.Hour)
	end := t.AddDate(1, 0, 0)
	_, prev := t.In(loc).Zone()
	for t.Before(end) {
		next := t.Add(time.Hour)
		_, off := next.In(loc).Zone()
		if off != prev {
			lo, hi := t, next
			for hi.Sub(lo) > time.Second {
				mid := lo.Add(hi.Sub(lo) / 2)
				if _, o := mid.In(loc).Zone(); o == prev {
					lo = mid
				} else {
					hi = mid
				}
			}
			out = append(out, newWindow(hi, prev, off))
			prev = off
		}
		t = next
	}
	return out
}

// newWindow turns an offset change at instant at into the wall-clock range it
// affects. Going from offset before to after, a forward jump skips
// [at+before, at+after) and a backward jump repeats [at+after, at+before).
func newWindow(at time.Time, before, after int) dstWindow {
	if after > before {
		return dstWindow{
			start:   at.Add(time.Duration(before) * time.Second).UTC(),
			minutes: (after - before) / 60,
			skipped: true,
		}
	}
	return dstWindow{
		start:   at.Add(time.Duration(after) * time.Second).UTC(),
		minutes: (before - after) / 60,
	}
}

// dstHit is the first firing of a schedule that falls into a dstWindow.
type dstHit struct {
	at      time.Time
	skipped bool
}

// dstHits returns, at most once per kind, a firing that the next year's clock
// changes in loc would skip or run twice. The upstream scheduler does exactly
// that: a wall-clock time that does not exist is passed over, and one that
// occurs twice fires on both occurrences.
func dstHits(s *Schedule, loc *time.Location, from time.Time) []dstHit {
	var hits []dstHit
	seen := map[bool]bool{}
	for _, w := range dstWindows(loc, from) {
		if seen[w.skipped] {
			continue
		}
		for m := 0; m < w.minutes; m++ {
			wall := w.start.Add(time.Duration(m) * time.Minute)
			if s.Matches(wall) {
				hits = append(hits, dstHit{at: wall, skipped: w.skipped})
				seen[w.skipped] = true
				break
			}
		}
	}
	return hits
}

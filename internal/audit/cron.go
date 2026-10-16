package audit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Schedule is a validated cron expression, reduced to the only two things the
// rules need: does it parse, and how close together can two runs be.
type Schedule struct {
	Expr string
	// MinGapMinutes is the smallest interval between two consecutive firings
	// within a day, wraparound included. A daily schedule is 1440.
	MinGapMinutes int
}

var macros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var dayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

type fieldSpec struct {
	name    string
	min     int
	max     int
	aliases map[string]int
}

var cronFields = []fieldSpec{
	{name: "minute", min: 0, max: 59},
	{name: "hour", min: 0, max: 23},
	{name: "day-of-month", min: 1, max: 31},
	{name: "month", min: 1, max: 12, aliases: monthNames},
	{name: "day-of-week", min: 0, max: 7, aliases: dayNames},
}

// ParseSchedule validates a Kubernetes CronJob schedule. It accepts the five
// standard fields plus the @-descriptors the upstream parser allows, and it is
// deliberately hand-written: pulling in a scheduler library to answer "is this
// string legal" would mean carrying a dependency for one function.
func ParseSchedule(expr string) (*Schedule, error) {
	raw := strings.TrimSpace(expr)
	if raw == "" {
		return nil, fmt.Errorf("schedule is empty")
	}

	if strings.HasPrefix(raw, "@every ") {
		d, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(raw, "@every ")))
		if err != nil {
			return nil, fmt.Errorf("@every: %v", err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("@every: duration must be positive, got %q", d)
		}
		gap := int(d.Minutes())
		if gap < 1 {
			gap = 1
		}
		return &Schedule{Expr: raw, MinGapMinutes: gap}, nil
	}

	if strings.HasPrefix(raw, "@") {
		expanded, ok := macros[strings.ToLower(raw)]
		if !ok {
			return nil, fmt.Errorf("unknown descriptor %q", raw)
		}
		raw = expanded
	}

	parts := strings.Fields(raw)
	if len(parts) != 5 {
		return nil, fmt.Errorf("expected 5 fields, got %d in %q", len(parts), expr)
	}

	sets := make([][]int, len(parts))
	for i, part := range parts {
		values, err := parseField(part, cronFields[i])
		if err != nil {
			return nil, fmt.Errorf("%s field %q: %v", cronFields[i].name, part, err)
		}
		sets[i] = values
	}

	return &Schedule{Expr: expr, MinGapMinutes: minGap(sets[1], sets[0])}, nil
}

// minGap walks every firing time in a single day and returns the smallest
// distance between two of them, treating the day as a circle so a 23:59 / 00:00
// pair is not reported as 1439 minutes apart.
func minGap(hours, minutes []int) int {
	times := make([]int, 0, len(hours)*len(minutes))
	for _, h := range hours {
		for _, m := range minutes {
			times = append(times, h*60+m)
		}
	}
	if len(times) == 0 {
		return 1440
	}
	sort.Ints(times)
	if len(times) == 1 {
		return 1440
	}
	gap := 1440 - times[len(times)-1] + times[0]
	for i := 1; i < len(times); i++ {
		if d := times[i] - times[i-1]; d < gap {
			gap = d
		}
	}
	return gap
}

func parseField(field string, spec fieldSpec) ([]int, error) {
	seen := map[int]bool{}
	for _, item := range strings.Split(field, ",") {
		if item == "" {
			return nil, fmt.Errorf("empty list element")
		}
		values, err := parseItem(item, spec)
		if err != nil {
			return nil, err
		}
		for _, v := range values {
			seen[v] = true
		}
	}
	out := make([]int, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Ints(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("matches nothing")
	}
	return out, nil
}

func parseItem(item string, spec fieldSpec) ([]int, error) {
	step := 1
	if idx := strings.Index(item, "/"); idx >= 0 {
		stepStr := item[idx+1:]
		item = item[:idx]
		n, err := strconv.Atoi(stepStr)
		if err != nil {
			return nil, fmt.Errorf("step %q is not a number", stepStr)
		}
		if n < 1 {
			return nil, fmt.Errorf("step must be >= 1, got %d", n)
		}
		step = n
	}

	lo, hi := spec.min, spec.max
	switch {
	case item == "*" || item == "?":
		// full range
	case strings.Contains(item, "-"):
		bounds := strings.SplitN(item, "-", 2)
		var err error
		if lo, err = parseValue(bounds[0], spec); err != nil {
			return nil, err
		}
		if hi, err = parseValue(bounds[1], spec); err != nil {
			return nil, err
		}
		if lo > hi {
			return nil, fmt.Errorf("range %s is inverted", item)
		}
	default:
		v, err := parseValue(item, spec)
		if err != nil {
			return nil, err
		}
		lo, hi = v, v
		if step > 1 {
			hi = spec.max
		}
	}

	out := []int{}
	for v := lo; v <= hi; v += step {
		out = append(out, normalize(v, spec))
	}
	return out, nil
}

func parseValue(s string, spec fieldSpec) (int, error) {
	s = strings.TrimSpace(s)
	if spec.aliases != nil {
		if v, ok := spec.aliases[strings.ToLower(s)]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number or a known name", s)
	}
	if v < spec.min || v > spec.max {
		return 0, fmt.Errorf("value %d out of range %d-%d", v, spec.min, spec.max)
	}
	return v, nil
}

// normalize folds the day-of-week 7 onto 0 so "sunday twice" is one value.
func normalize(v int, spec fieldSpec) int {
	if spec.name == "day-of-week" && v == 7 {
		return 0
	}
	return v
}

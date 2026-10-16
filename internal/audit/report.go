package audit

import (
	"encoding/json"
	"fmt"
	"io"
)

// Summarize counts findings by rule and severity for the report envelope.
func Summarize(docs []Document, findings []Finding) ReportSummary {
	s := ReportSummary{
		CronJobs:   len(docs),
		Findings:   len(findings),
		ByRule:     map[string]int{},
		BySeverity: map[string]int{},
	}
	for _, f := range findings {
		s.ByRule[f.Rule]++
		s.BySeverity[string(f.Severity)]++
	}
	return s
}

// WriteJSON prints the machine-readable report. findings is always an array,
// never null, so `jq '.findings[]'` works on a clean run too.
func WriteJSON(w io.Writer, docs []Document, findings []Finding) error {
	if findings == nil {
		findings = []Finding{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(Report{Findings: findings, Summary: Summarize(docs, findings)})
}

// WriteText prints findings grouped by file and CronJob: the thing a human
// reads in a pull request diff.
func WriteText(w io.Writer, docs []Document, findings []Finding) {
	if len(docs) == 0 {
		fmt.Fprintln(w, "no CronJobs found")
		return
	}
	if len(findings) == 0 {
		fmt.Fprintf(w, "no findings in %s\n", plural(len(docs), "CronJob", "CronJobs"))
		return
	}

	var file, cronjob string
	for _, f := range findings {
		if f.File != file {
			if file != "" {
				fmt.Fprintln(w)
			}
			fmt.Fprintln(w, f.File)
			file = f.File
			cronjob = ""
		}
		key := f.Namespace + "/" + f.Name
		if key != cronjob {
			fmt.Fprintf(w, "  %s  (%s)\n", key, f.Schedule)
			cronjob = key
		}
		fmt.Fprintf(w, "    %-7s %-24s %s\n", f.Severity, f.Rule, f.Message)
	}

	s := Summarize(docs, findings)
	fmt.Fprintf(w, "\n%s in %s (high %d, medium %d, low %d)\n",
		plural(s.Findings, "finding", "findings"),
		plural(s.CronJobs, "CronJob", "CronJobs"),
		s.BySeverity["high"], s.BySeverity["medium"], s.BySeverity["low"])
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// FailOn reports whether any finding matches one of the rules named in
// --fail-on, which is what turns the audit into a CI gate.
func FailOn(findings []Finding, rules map[string]bool) bool {
	if len(rules) == 0 {
		return false
	}
	for _, f := range findings {
		if rules["all"] || rules[f.Rule] {
			return true
		}
	}
	return false
}

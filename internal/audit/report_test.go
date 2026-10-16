package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriteJSONShape(t *testing.T) {
	docs := load(t, "defaults.yaml")
	findings := Analyze(docs, Options{})
	var buf bytes.Buffer
	if err := WriteJSON(&buf, docs, findings); err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if report.Summary.CronJobs != 1 || report.Summary.Findings != len(findings) {
		t.Errorf("summary = %+v", report.Summary)
	}
	if report.Summary.ByRule["allow-concurrency"] != 1 {
		t.Errorf("by_rule = %v", report.Summary.ByRule)
	}
	if report.Summary.BySeverity["high"] != 1 {
		t.Errorf("by_severity = %v", report.Summary.BySeverity)
	}
	if report.Findings[0].Name != "report-nightly" {
		t.Errorf("finding name = %q", report.Findings[0].Name)
	}
}

func TestWriteJSONEmitsEmptyArrayNotNull(t *testing.T) {
	docs := load(t, "clean.yaml")
	var buf bytes.Buffer
	if err := WriteJSON(&buf, docs, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"findings": []`) {
		t.Fatalf("a clean run must still produce an array:\n%s", buf.String())
	}
}

func TestWriteTextGroupsByFileAndCronJob(t *testing.T) {
	docs := load(t, "helm-rendered.yaml")
	findings := Analyze(docs, Options{})
	var buf bytes.Buffer
	WriteText(&buf, docs, findings)
	out := buf.String()
	for _, want := range []string{
		"testdata/helm-rendered.yaml",
		"billing/invoice-sweep",
		"billing/ledger-reconcile",
		"allow-concurrency",
		"CronJobs (high",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "billing/invoice-sweep") != 1 {
		t.Errorf("CronJob header repeated instead of grouping:\n%s", out)
	}
}

func TestWriteTextCleanAndEmptyRuns(t *testing.T) {
	var buf bytes.Buffer
	WriteText(&buf, load(t, "clean.yaml"), nil)
	if got := strings.TrimSpace(buf.String()); got != "no findings in 1 CronJob" {
		t.Errorf("clean output = %q", got)
	}

	buf.Reset()
	WriteText(&buf, nil, nil)
	if got := strings.TrimSpace(buf.String()); got != "no CronJobs found" {
		t.Errorf("empty output = %q", got)
	}
}

func TestFailOn(t *testing.T) {
	findings := Analyze(load(t, "defaults.yaml"), Options{})
	if FailOn(findings, nil) {
		t.Error("no --fail-on should never fail the run")
	}
	if !FailOn(findings, map[string]bool{"allow-concurrency": true}) {
		t.Error("a rule that fired should fail the run")
	}
	if FailOn(findings, map[string]bool{"suspended": true}) {
		t.Error("a rule that did not fire should not fail the run")
	}
	if !FailOn(findings, map[string]bool{"all": true}) {
		t.Error("--fail-on all should fail when anything fired")
	}
	if FailOn(nil, map[string]bool{"all": true}) {
		t.Error("--fail-on all with no findings should pass")
	}
}

func TestSeverityAtLeast(t *testing.T) {
	if !SeverityHigh.AtLeast(SeverityLow) || !SeverityMedium.AtLeast(SeverityMedium) {
		t.Error("severity ordering is wrong")
	}
	if SeverityLow.AtLeast(SeverityMedium) {
		t.Error("low should not pass a medium filter")
	}
	if _, ok := ParseSeverity("critical"); ok {
		t.Error("ParseSeverity accepted an unknown level")
	}
}

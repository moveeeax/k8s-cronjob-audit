package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const examples = "../../examples/manifests"

func exec(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func TestExamplesAuditCleanExit(t *testing.T) {
	code, out, _ := exec(t, "", examples)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 without --fail-on\n%s", code, out)
	}
	for _, want := range []string{"prod/db-backup", "allow-concurrency", "bad-schedule"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "invoice-export") {
		t.Errorf("the hardened CronJob should produce nothing:\n%s", out)
	}
}

func TestFailOnMatchedRuleExitsOne(t *testing.T) {
	code, _, _ := exec(t, "", "--fail-on", "allow-concurrency", examples)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestFailOnUnmatchedRuleExitsZero(t *testing.T) {
	code, _, _ := exec(t, "", "--fail-on", "suspended", examples)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — no CronJob here is suspended", code)
	}
}

func TestFailOnUnknownRuleIsAnError(t *testing.T) {
	// A typo in --fail-on must not silently turn the gate into a no-op.
	code, _, errOut := exec(t, "", "--fail-on", "allow-concurency", examples)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "unknown rule") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestIgnoreNameRemovesOnlyThatCronJob(t *testing.T) {
	code, out, _ := exec(t, "", "--fail-on", "all", "--ignore-name", "db-backup", examples)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 once the only noisy CronJob is ignored\n%s", code, out)
	}
	if strings.Contains(out, "db-backup") {
		t.Errorf("ignored CronJob still in output:\n%s", out)
	}
}

func TestIgnoreRuleRemovesOnlyThatRule(t *testing.T) {
	_, out, _ := exec(t, "", "--ignore-rule", "allow-concurrency,unbounded-history", examples)
	if strings.Contains(out, "allow-concurrency") || strings.Contains(out, "unbounded-history") {
		t.Errorf("ignored rules still in output:\n%s", out)
	}
	if !strings.Contains(out, "no-starting-deadline") {
		t.Errorf("other rules should survive:\n%s", out)
	}
}

func TestJSONOutputIsParseable(t *testing.T) {
	code, out, _ := exec(t, "", "--format", "json", examples)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var report struct {
		Findings []struct {
			Rule     string `json:"rule"`
			Name     string `json:"name"`
			Severity string `json:"severity"`
		} `json:"findings"`
		Summary struct {
			CronJobs int `json:"cronjobs"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if report.Summary.CronJobs != 2 {
		t.Errorf("cronjobs = %d, want 2", report.Summary.CronJobs)
	}
	if len(report.Findings) == 0 {
		t.Fatal("expected findings for the example manifests")
	}
}

func TestSeverityFlagFiltersOutput(t *testing.T) {
	_, out, _ := exec(t, "", "--severity", "high", examples)
	if strings.Contains(out, "unbounded-history") {
		t.Errorf("a low finding survived --severity high:\n%s", out)
	}
	if !strings.Contains(out, "allow-concurrency") {
		t.Errorf("the high finding should remain:\n%s", out)
	}
}

func TestStdinInput(t *testing.T) {
	manifest := `apiVersion: batch/v1
kind: CronJob
metadata:
  name: piped
  namespace: ops
spec:
  schedule: "0 5 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: Never
`
	code, out, _ := exec(t, manifest, "--fail-on", "allow-concurrency", "-")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "ops/piped") {
		t.Errorf("output missing the piped CronJob:\n%s", out)
	}
}

func TestBadFlagsAndHelp(t *testing.T) {
	if code, _, _ := exec(t, "", "--format", "xml", examples); code != 2 {
		t.Error("unknown --format should exit 2")
	}
	if code, _, _ := exec(t, "", "--severity", "critical", examples); code != 2 {
		t.Error("unknown --severity should exit 2")
	}
	if code, _, _ := exec(t, ""); code != 2 {
		t.Error("no paths should exit 2 with usage")
	}
	if code, _, _ := exec(t, "", "--ignore-rule", "nope", examples); code != 2 {
		t.Error("unknown --ignore-rule should exit 2")
	}
}

func TestListRulesAndVersion(t *testing.T) {
	code, out, _ := exec(t, "", "--list-rules")
	if code != 0 || !strings.Contains(out, "allow-concurrency") {
		t.Fatalf("--list-rules: exit=%d out=%q", code, out)
	}
	if lines := strings.Count(strings.TrimSpace(out), "\n") + 1; lines != 7 {
		t.Errorf("--list-rules printed %d lines, want one per rule", lines)
	}
	if code, out, _ := exec(t, "", "--version"); code != 0 || strings.TrimSpace(out) == "" {
		t.Errorf("--version: exit=%d out=%q", code, out)
	}
}

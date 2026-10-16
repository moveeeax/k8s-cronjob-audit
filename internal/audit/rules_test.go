package audit

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func load(t *testing.T, name string) []Document {
	t.Helper()
	docs, err := ParsePaths([]string{filepath.Join("testdata", name)}, nil)
	if err != nil {
		t.Fatalf("ParsePaths(%s): %v", name, err)
	}
	return docs
}

func rulesFired(findings []Finding, cronjob string) []string {
	var out []string
	for _, f := range findings {
		if cronjob != "" && f.Name != cronjob {
			continue
		}
		out = append(out, f.Rule)
	}
	sort.Strings(out)
	return out
}

func TestCleanManifestHasNoFindings(t *testing.T) {
	findings := Analyze(load(t, "clean.yaml"), Options{})
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %v", rulesFired(findings, ""))
	}
}

func TestDefaultsFireEveryDefaultRule(t *testing.T) {
	findings := Analyze(load(t, "defaults.yaml"), Options{})
	want := []string{
		"allow-concurrency",
		"no-active-deadline",
		"no-starting-deadline",
		"unbounded-history",
	}
	if got := rulesFired(findings, "report-nightly"); !equal(got, want) {
		t.Fatalf("rules = %v, want %v", got, want)
	}
	// One CronJob producing several findings is the point: each missing field
	// is its own decision.
	for _, f := range findings {
		if f.Namespace != "default" {
			t.Errorf("namespace = %q, want the implicit default", f.Namespace)
		}
		if f.Schedule != "15 3 * * *" {
			t.Errorf("schedule = %q, want the manifest value", f.Schedule)
		}
	}
}

func TestAllowConcurrencyNamesScheduleAndDisappearsWithForbid(t *testing.T) {
	findings := Analyze(load(t, "defaults.yaml"), Options{IgnoreRules: skip("no-active-deadline", "no-starting-deadline", "unbounded-history")})
	if len(findings) != 1 {
		t.Fatalf("expected exactly the concurrency finding, got %v", rulesFired(findings, ""))
	}
	f := findings[0]
	if f.Rule != "allow-concurrency" || f.Severity != SeverityHigh {
		t.Fatalf("got rule=%s severity=%s", f.Rule, f.Severity)
	}
	if !strings.Contains(f.Message, "15 3 * * *") {
		t.Errorf("message should quote the schedule, got %q", f.Message)
	}

	docs := load(t, "defaults.yaml")
	docs[0].CronJob.Spec.ConcurrencyPolicy = "Forbid"
	if got := rulesFired(Analyze(docs, Options{}), ""); contains(got, "allow-concurrency") {
		t.Errorf("Forbid should silence allow-concurrency, got %v", got)
	}
}

func TestSuspendedFiresRegardlessOfOtherSettings(t *testing.T) {
	findings := Analyze(load(t, "suspended.yaml"), Options{})
	if got := rulesFired(findings, "etcd-snapshot"); !equal(got, []string{"suspended"}) {
		t.Fatalf("rules = %v, want only suspended", got)
	}
	if !strings.Contains(findings[0].Message, "0 */6 * * *") {
		t.Errorf("message should quote the schedule, got %q", findings[0].Message)
	}
}

func TestBadScheduleNamesTheOffendingValue(t *testing.T) {
	findings := Analyze(load(t, "bad-schedule.yaml"), Options{})
	if got := rulesFired(findings, "cache-warm"); !equal(got, []string{"bad-schedule"}) {
		t.Fatalf("rules = %v, want only bad-schedule", got)
	}
	f := findings[0]
	if !strings.Contains(f.Message, `"*/5 25 * * *"`) {
		t.Errorf("message should quote the schedule, got %q", f.Message)
	}
	if f.Severity != SeverityHigh {
		t.Errorf("severity = %s, want high for an expression that cannot run", f.Severity)
	}
}

func TestTightScheduleOnlyWithAllowConcurrency(t *testing.T) {
	docs := load(t, "clean.yaml")
	docs[0].CronJob.Spec.Schedule = "*/2 * * * *"
	if got := rulesFired(Analyze(docs, Options{}), ""); len(got) != 0 {
		t.Fatalf("Forbid plus a tight schedule is fine, got %v", got)
	}

	docs[0].CronJob.Spec.ConcurrencyPolicy = "Allow"
	findings := Analyze(docs, Options{IgnoreRules: skip("allow-concurrency")})
	if got := rulesFired(findings, ""); !equal(got, []string{"bad-schedule"}) {
		t.Fatalf("rules = %v, want bad-schedule for the tight overlap window", got)
	}
	if findings[0].Severity != SeverityMedium {
		t.Errorf("severity = %s, want medium for a tight-but-valid schedule", findings[0].Severity)
	}
	if !strings.Contains(findings[0].Message, "every 2 min") {
		t.Errorf("message should state the interval, got %q", findings[0].Message)
	}
}

func TestUnboundedHistoryNamesFieldsAndDefaults(t *testing.T) {
	findings := Analyze(load(t, "defaults.yaml"), Options{IgnoreRules: skipAllBut("unbounded-history")})
	if len(findings) != 1 {
		t.Fatalf("expected one history finding, got %d", len(findings))
	}
	msg := findings[0].Message
	for _, want := range []string{"successfulJobsHistoryLimit", "default 3", "failedJobsHistoryLimit", "default 1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}
}

func TestHighHistoryLimitFires(t *testing.T) {
	findings := Analyze(load(t, "helm-rendered.yaml"), Options{IgnoreRules: skipAllBut("unbounded-history")})
	if got := rulesFired(findings, "ledger-reconcile"); !equal(got, []string{"unbounded-history"}) {
		t.Fatalf("rules = %v, want unbounded-history for a 500-Job history", got)
	}
}

func TestRestartPolicyRules(t *testing.T) {
	missing := Analyze(load(t, "no-restart-policy.yaml"), Options{IgnoreRules: skipAllBut("no-restart-policy-never")})
	if len(missing) != 1 || missing[0].Severity != SeverityMedium {
		t.Fatalf("unset restartPolicy should be a medium finding, got %v", missing)
	}

	onFailure := Analyze(load(t, "helm-rendered.yaml"), Options{IgnoreRules: skipAllBut("no-restart-policy-never")})
	if got := rulesFired(onFailure, "invoice-sweep"); !equal(got, []string{"no-restart-policy-never"}) {
		t.Fatalf("rules = %v, want no-restart-policy-never", got)
	}
	if !strings.Contains(onFailure[0].Message, "backoffLimit is unset") {
		t.Errorf("message should mention the unset backoffLimit, got %q", onFailure[0].Message)
	}
}

func TestIgnoreNameSuppressesOnlyItsTarget(t *testing.T) {
	docs := load(t, "helm-rendered.yaml")
	all := Analyze(docs, Options{})
	filtered := Analyze(docs, Options{IgnoreNames: map[string]bool{"invoice-sweep": true}})
	if len(filtered) >= len(all) {
		t.Fatalf("--ignore-name suppressed nothing: %d vs %d", len(filtered), len(all))
	}
	for _, f := range filtered {
		if f.Name == "invoice-sweep" {
			t.Fatalf("ignored CronJob still reported: %+v", f)
		}
	}
	if !contains(rulesFired(filtered, "ledger-reconcile"), "unbounded-history") {
		t.Error("the other CronJob should still be audited")
	}
}

func TestIgnoreRuleSuppressesOnlyItsTarget(t *testing.T) {
	docs := load(t, "defaults.yaml")
	filtered := Analyze(docs, Options{IgnoreRules: skip("allow-concurrency")})
	if contains(rulesFired(filtered, ""), "allow-concurrency") {
		t.Fatal("ignored rule still reported")
	}
	if len(filtered) != 3 {
		t.Fatalf("expected the remaining three findings, got %v", rulesFired(filtered, ""))
	}
}

func TestSeverityFilter(t *testing.T) {
	docs := load(t, "defaults.yaml")
	high := Analyze(docs, Options{MinSeverity: SeverityHigh})
	if got := rulesFired(high, ""); !equal(got, []string{"allow-concurrency"}) {
		t.Fatalf("rules = %v, want only the high finding", got)
	}
	medium := Analyze(docs, Options{MinSeverity: SeverityMedium})
	if contains(rulesFired(medium, ""), "unbounded-history") {
		t.Error("a low finding survived --severity medium")
	}
}

func TestFindingsAreStableAcrossRuns(t *testing.T) {
	docs := load(t, "helm-rendered.yaml")
	first := Analyze(docs, Options{})
	for i := 0; i < 5; i++ {
		next := Analyze(docs, Options{})
		if len(next) != len(first) {
			t.Fatalf("finding count changed between runs: %d vs %d", len(next), len(first))
		}
		for j := range next {
			if next[j] != first[j] {
				t.Fatalf("finding %d changed between runs:\n%+v\n%+v", j, next[j], first[j])
			}
		}
	}
}

func TestEveryRuleIsCoveredByAFixture(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range entries {
		paths = append(paths, filepath.Join("testdata", e.Name()))
	}
	docs, err := ParsePaths(paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	fired := map[string]bool{}
	for _, f := range Analyze(docs, Options{}) {
		fired[f.Rule] = true
	}
	for _, name := range RuleNames() {
		if !fired[name] {
			t.Errorf("rule %q never fires on any fixture", name)
		}
	}
}

func skip(names ...string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		out[n] = true
	}
	return out
}

func skipAllBut(keep string) map[string]bool {
	out := map[string]bool{}
	for _, n := range RuleNames() {
		if n != keep {
			out[n] = true
		}
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

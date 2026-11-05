// Command k8s-cronjob-audit reads Kubernetes CronJob manifests and reports the
// settings that turn a scheduled job into an incident.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/moveeeax/k8s-cronjob-audit/internal/audit"
)

// version is overridden at build time with -ldflags "-X main.version=v0.2.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("k8s-cronjob-audit", flag.ContinueOnError)
	fs.SetOutput(stderr)

	format := fs.String("format", "text", "output format: text or json")
	failOn := fs.String("fail-on", "", "comma-separated rules (or \"all\") that make the command exit 1")
	ignoreRule := fs.String("ignore-rule", "", "comma-separated rules to skip entirely")
	ignoreName := fs.String("ignore-name", "", "comma-separated CronJob names to skip entirely")
	severity := fs.String("severity", "low", "minimum severity to report: low, medium or high")
	listRules := fs.Bool("list-rules", false, "print the rule set and exit")
	showVersion := fs.Bool("version", false, "print the version and exit")

	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: k8s-cronjob-audit [flags] <path|-> [path...]\n\n")
		fmt.Fprintf(stderr, "Audits batch/v1 CronJob manifests. Pass directories, files, or - for stdin.\n\n")
		fs.PrintDefaults()
		fmt.Fprintf(stderr, "\nrules: %s\n", strings.Join(audit.RuleNames(), ", "))
	}

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}

	if *listRules {
		for _, r := range audit.Rules {
			fmt.Fprintf(stdout, "%-24s %-7s %s\n", r.Name, r.Severity, r.Summary)
		}
		return 0
	}

	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "error: unknown --format %q (want text or json)\n", *format)
		return 2
	}

	minSeverity, ok := audit.ParseSeverity(*severity)
	if !ok {
		fmt.Fprintf(stderr, "error: unknown --severity %q (want low, medium or high)\n", *severity)
		return 2
	}

	failRules, err := ruleSet(*failOn, true)
	if err != nil {
		fmt.Fprintf(stderr, "error: --fail-on: %v\n", err)
		return 2
	}
	skipRules, err := ruleSet(*ignoreRule, false)
	if err != nil {
		fmt.Fprintf(stderr, "error: --ignore-rule: %v\n", err)
		return 2
	}

	paths := fs.Args()
	if len(paths) == 0 {
		fs.Usage()
		return 2
	}

	docs, err := audit.ParsePaths(paths, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	findings := audit.Analyze(docs, audit.Options{
		IgnoreRules: skipRules,
		IgnoreNames: nameSet(*ignoreName),
		MinSeverity: minSeverity,
	})

	if *format == "json" {
		if err := audit.WriteJSON(stdout, docs, findings); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 2
		}
	} else {
		audit.WriteText(stdout, docs, findings)
	}

	if audit.FailOn(findings, failRules) {
		return 1
	}
	return 0
}

// ruleSet parses a comma-separated rule list. An unknown rule name is an error
// rather than a filter that quietly matches nothing — a typo in --fail-on would
// otherwise turn a CI gate into a no-op that always passes.
func ruleSet(csv string, allowAll bool) (map[string]bool, error) {
	out := map[string]bool{}
	for _, item := range splitCSV(csv) {
		if allowAll && item == "all" {
			out["all"] = true
			continue
		}
		if !audit.KnownRule(item) {
			return nil, fmt.Errorf("unknown rule %q (known: %s)", item, strings.Join(audit.RuleNames(), ", "))
		}
		out[item] = true
	}
	return out, nil
}

func nameSet(csv string) map[string]bool {
	out := map[string]bool{}
	for _, item := range splitCSV(csv) {
		out[item] = true
	}
	return out
}

func splitCSV(csv string) []string {
	var out []string
	for _, item := range strings.Split(csv, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

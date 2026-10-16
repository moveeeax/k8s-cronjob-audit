package audit

import (
	"fmt"
	"sort"
	"strings"
)

// historyLimitCeiling is where "keep some history" turns into "keep every Job
// object and its pods forever". 50 finished Jobs per CronJob is already a lot
// of etcd for something nobody reads.
const historyLimitCeiling = 50

// tightScheduleMinutes is the overlap danger zone: a job that fires this often
// has to finish in under that gap, every time, or Allow concurrency stacks runs.
const tightScheduleMinutes = 5

// Rule is one check. It gets a decoded CronJob and returns a message, or the
// empty string when the CronJob is fine. Returning a severity lets a rule
// escalate itself — a schedule that does not parse is worse than one that is
// merely too tight.
type Rule struct {
	Name     string
	Severity Severity
	Summary  string
	check    func(cj CronJob) (string, Severity)
}

// Rules is the full rule set, in report order.
var Rules = []Rule{
	{
		Name:     "allow-concurrency",
		Severity: SeverityHigh,
		Summary:  "concurrencyPolicy is Allow or unset, so a slow run overlaps the next one",
		check: func(cj CronJob) (string, Severity) {
			policy := cj.Spec.ConcurrencyPolicy
			switch policy {
			case "", "Allow":
				state := "Allow"
				if policy == "" {
					state = "unset (defaults to Allow)"
				}
				return fmt.Sprintf(
					"concurrencyPolicy is %s; schedule %q will run twice if one run is slow — set Forbid or Replace unless the job is reentrant",
					state, cj.Spec.Schedule), ""
			}
			return "", ""
		},
	},
	{
		Name:     "no-starting-deadline",
		Severity: SeverityMedium,
		Summary:  "startingDeadlineSeconds unset, so a missed schedule is a silent skip",
		check: func(cj CronJob) (string, Severity) {
			if cj.Spec.StartingDeadlineSeconds == nil {
				return "startingDeadlineSeconds is unset; after a control-plane pause the run is either fired late or skipped with only a FailedNeedsStart event nobody reads", ""
			}
			return "", ""
		},
	},
	{
		Name:     "unbounded-history",
		Severity: SeverityLow,
		Summary:  "history limits unset or high, so debuggable failures are evicted first",
		check: func(cj CronJob) (string, Severity) {
			var notes []string
			if cj.Spec.SuccessfulJobsHistoryLimit == nil {
				notes = append(notes, "successfulJobsHistoryLimit is unset (effective default 3)")
			} else if *cj.Spec.SuccessfulJobsHistoryLimit > historyLimitCeiling {
				notes = append(notes, fmt.Sprintf("successfulJobsHistoryLimit is %d, which keeps that many Jobs and their pods in etcd", *cj.Spec.SuccessfulJobsHistoryLimit))
			}
			if cj.Spec.FailedJobsHistoryLimit == nil {
				notes = append(notes, "failedJobsHistoryLimit is unset (effective default 1, so the failures you want to debug are evicted before the successes)")
			} else if *cj.Spec.FailedJobsHistoryLimit > historyLimitCeiling {
				notes = append(notes, fmt.Sprintf("failedJobsHistoryLimit is %d, which keeps that many Jobs and their pods in etcd", *cj.Spec.FailedJobsHistoryLimit))
			}
			if len(notes) == 0 {
				return "", ""
			}
			return strings.Join(notes, "; "), ""
		},
	},
	{
		Name:     "no-active-deadline",
		Severity: SeverityMedium,
		Summary:  "nothing reaps a hung run",
		check: func(cj CronJob) (string, Severity) {
			spec := cj.Spec.JobTemplate.Spec
			if spec.ActiveDeadlineSeconds == nil && spec.TTLSecondsAfterFinished == nil {
				return "neither activeDeadlineSeconds nor ttlSecondsAfterFinished is set on the job template; a hung run holds its pod until someone notices", ""
			}
			return "", ""
		},
	},
	{
		Name:     "suspended",
		Severity: SeverityHigh,
		Summary:  "suspend: true — a paused job looks healthy on every dashboard",
		check: func(cj CronJob) (string, Severity) {
			if cj.Spec.Suspend != nil && *cj.Spec.Suspend {
				return fmt.Sprintf("suspend is true; schedule %q has not fired since it was paused", cj.Spec.Schedule), ""
			}
			return "", ""
		},
	},
	{
		Name:     "bad-schedule",
		Severity: SeverityHigh,
		Summary:  "the cron expression does not parse, or it is too tight to allow overlap",
		check: func(cj CronJob) (string, Severity) {
			sched, err := ParseSchedule(cj.Spec.Schedule)
			if err != nil {
				return fmt.Sprintf("schedule %q does not parse: %v", cj.Spec.Schedule, err), SeverityHigh
			}
			policy := cj.Spec.ConcurrencyPolicy
			if sched.MinGapMinutes <= tightScheduleMinutes && (policy == "" || policy == "Allow") {
				return fmt.Sprintf(
					"schedule %q fires every %d min with concurrencyPolicy %s; the job has less than that to finish before the next run starts",
					cj.Spec.Schedule, sched.MinGapMinutes, policyLabel(policy)), SeverityMedium
			}
			return "", ""
		},
	},
	{
		Name:     "no-restart-policy-never",
		Severity: SeverityLow,
		Summary:  "job template restartPolicy hides retries from backoffLimit",
		check: func(cj CronJob) (string, Severity) {
			policy := cj.Spec.JobTemplate.Spec.Template.Spec.RestartPolicy
			switch policy {
			case "":
				return "job template restartPolicy is unset; a pod defaults to Always and the Job API rejects that, so this manifest never creates a run", SeverityMedium
			case "OnFailure":
				note := "job template restartPolicy is OnFailure, so retries happen in place and backoffLimit counts container restarts instead of pods"
				if cj.Spec.JobTemplate.Spec.BackoffLimit == nil {
					note += "; backoffLimit is unset too (effective default 6)"
				}
				return note, ""
			}
			return "", ""
		},
	},
}

func policyLabel(policy string) string {
	if policy == "" {
		return "unset (Allow)"
	}
	return policy
}

// Options controls which findings survive.
type Options struct {
	IgnoreRules map[string]bool
	IgnoreNames map[string]bool
	MinSeverity Severity
}

// Analyze runs every rule against every document and returns the findings that
// pass the filters, sorted so output is stable across runs.
func Analyze(docs []Document, opts Options) []Finding {
	if opts.MinSeverity == "" {
		opts.MinSeverity = SeverityLow
	}
	var findings []Finding
	for _, doc := range docs {
		cj := doc.CronJob
		if opts.IgnoreNames[cj.Metadata.Name] {
			continue
		}
		for _, rule := range Rules {
			if opts.IgnoreRules[rule.Name] {
				continue
			}
			msg, sev := rule.check(cj)
			if msg == "" {
				continue
			}
			if sev == "" {
				sev = rule.Severity
			}
			if !sev.AtLeast(opts.MinSeverity) {
				continue
			}
			findings = append(findings, Finding{
				File:      doc.File,
				Name:      cj.Metadata.Name,
				Namespace: namespaceOrDefault(cj.Metadata.Namespace),
				Schedule:  cj.Spec.Schedule,
				Rule:      rule.Name,
				Severity:  sev,
				Message:   msg,
			})
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Rule < b.Rule
	})
	return findings
}

func namespaceOrDefault(ns string) string {
	if ns == "" {
		return "default"
	}
	return ns
}

// RuleNames lists every rule name, for --help and for flag validation.
func RuleNames() []string {
	names := make([]string, 0, len(Rules))
	for _, r := range Rules {
		names = append(names, r.Name)
	}
	return names
}

// KnownRule reports whether name is a rule, so a typo in --fail-on is an error
// instead of a filter that silently matches nothing.
func KnownRule(name string) bool {
	for _, r := range Rules {
		if r.Name == name {
			return true
		}
	}
	return false
}

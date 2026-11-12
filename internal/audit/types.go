// Package audit decodes Kubernetes CronJob manifests and applies reliability
// rules to them. Everything here works on decoded structs, never on a cluster,
// so each rule is testable from a YAML fixture.
package audit

// CronJob is the subset of batch/v1 CronJob the rules care about. Pointer
// fields are deliberate: an unset startingDeadlineSeconds and an explicit 0
// mean different things, and most of the rules exist precisely because the
// field was never written down.
type CronJob struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   Metadata    `json:"metadata"`
	Spec       CronJobSpec `json:"spec"`
}

type Metadata struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type CronJobSpec struct {
	Schedule                   string      `json:"schedule"`
	TimeZone                   *string     `json:"timeZone"`
	ConcurrencyPolicy          string      `json:"concurrencyPolicy"`
	StartingDeadlineSeconds    *int64      `json:"startingDeadlineSeconds"`
	SuccessfulJobsHistoryLimit *int32      `json:"successfulJobsHistoryLimit"`
	FailedJobsHistoryLimit     *int32      `json:"failedJobsHistoryLimit"`
	Suspend                    *bool       `json:"suspend"`
	JobTemplate                JobTemplate `json:"jobTemplate"`
}

type JobTemplate struct {
	Spec JobSpec `json:"spec"`
}

type JobSpec struct {
	ActiveDeadlineSeconds   *int64      `json:"activeDeadlineSeconds"`
	TTLSecondsAfterFinished *int32      `json:"ttlSecondsAfterFinished"`
	BackoffLimit            *int32      `json:"backoffLimit"`
	Template                PodTemplate `json:"template"`
}

type PodTemplate struct {
	Spec PodSpec `json:"spec"`
}

type PodSpec struct {
	RestartPolicy string `json:"restartPolicy"`
}

// Severity orders findings so --severity can cut the noise without hiding the
// things that page someone.
type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

var severityRank = map[Severity]int{
	SeverityLow:    1,
	SeverityMedium: 2,
	SeverityHigh:   3,
}

// AtLeast reports whether s is as severe as min.
func (s Severity) AtLeast(min Severity) bool {
	return severityRank[s] >= severityRank[min]
}

// ParseSeverity validates a --severity value.
func ParseSeverity(s string) (Severity, bool) {
	sev := Severity(s)
	_, ok := severityRank[sev]
	return sev, ok
}

// Finding is one rule firing on one CronJob.
type Finding struct {
	File      string   `json:"file"`
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Schedule  string   `json:"schedule"`
	Rule      string   `json:"rule"`
	Severity  Severity `json:"severity"`
	Message   string   `json:"message"`
}

// Report is the JSON output envelope.
type Report struct {
	Findings []Finding     `json:"findings"`
	Summary  ReportSummary `json:"summary"`
}

type ReportSummary struct {
	CronJobs   int            `json:"cronjobs"`
	Findings   int            `json:"findings"`
	ByRule     map[string]int `json:"by_rule"`
	BySeverity map[string]int `json:"by_severity"`
}

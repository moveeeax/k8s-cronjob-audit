# k8s-cronjob-audit

[![ci](https://github.com/moveeeax/k8s-cronjob-audit/actions/workflows/ci.yml/badge.svg)](https://github.com/moveeeax/k8s-cronjob-audit/actions/workflows/ci.yml)
[![go](https://img.shields.io/badge/go-1.26%2B-00ADD8)](https://go.dev/dl/)
[![license](https://img.shields.io/badge/license-MIT-green)](LICENSE)

Audit Kubernetes CronJob manifests for the settings that turn a scheduled job into an incident.

A CronJob with no `concurrencyPolicy` is a promise that the previous run always finishes in time. The night the database is slow, two of them start at once, and the job that was never written to be reentrant corrupts exactly the thing it was backing up. Every ingredient of that outage is visible in the YAML long before it reaches a cluster. Nobody looks, because there is nothing to look with.

This is the thing to look with: a single static binary that reads manifests (files, directories, `helm template` output on stdin) and prints one finding per problem, with the CronJob's name, namespace and schedule attached.

## Rules

| rule | severity | what it catches |
| --- | --- | --- |
| `allow-concurrency` | high | `concurrencyPolicy` is `Allow` or unset, so a slow run overlaps the next one |
| `suspended` | high | `suspend: true` — a paused backup looks healthy on every dashboard |
| `bad-schedule` | high / medium | the cron expression does not parse, or it fires every 5 minutes or tighter while concurrency is allowed |
| `no-starting-deadline` | medium | `startingDeadlineSeconds` is unset, so a missed schedule is a silent skip |
| `no-active-deadline` | medium | neither `activeDeadlineSeconds` nor `ttlSecondsAfterFinished` is set, so a hung run is never reaped |
| `no-restart-policy-never` | medium / low | the job template `restartPolicy` is unset (the Job API rejects that) or `OnFailure`, which hides retries from `backoffLimit` |
| `timezone` | high / medium / low | high: `timeZone` is not a zone the API accepts, or `CRON_TZ=`/`TZ=` is in `schedule` (the API rejects both); medium: in the `timeZone` given, a DST change in the next year skips a run or fires it twice; low: the schedule fires at fixed hours and `timeZone` is unset, so it runs in kube-controller-manager's zone |
| `unbounded-history` | low | history limits unset or absurdly high; the default keeps 3 successes and **1** failure, so the runs worth debugging are evicted first |

`k8s-cronjob-audit --list-rules` prints the rule set straight from the binary, so the help never drifts from the code.

## Usage

```console
$ k8s-cronjob-audit examples/manifests
examples/manifests/backup.yaml
  prod/db-backup  (*/3 * * * *)
    high    allow-concurrency        concurrencyPolicy is unset (defaults to Allow); schedule "*/3 * * * *" will run twice if one run is slow — set Forbid or Replace unless the job is reentrant
    medium  bad-schedule             schedule "*/3 * * * *" fires every 3 min with concurrencyPolicy unset (Allow); the job has less than that to finish before the next run starts
    medium  no-active-deadline       neither activeDeadlineSeconds nor ttlSecondsAfterFinished is set on the job template; a hung run holds its pod until someone notices
    low     no-restart-policy-never  job template restartPolicy is OnFailure, so retries happen in place and backoffLimit counts container restarts instead of pods; backoffLimit is unset too (effective default 6)
    medium  no-starting-deadline     startingDeadlineSeconds is unset; after a control-plane pause the run is either fired late or skipped with only a FailedNeedsStart event nobody reads
    low     unbounded-history        successfulJobsHistoryLimit is unset (effective default 3); failedJobsHistoryLimit is unset (effective default 1, so the failures you want to debug are evicted before the successes)

6 findings in 2 CronJobs (high 1, medium 3, low 2)
```

Rendered charts work the same way:

```sh
helm template chart/ | k8s-cronjob-audit -
```

JSON for anything that is not a human:

```sh
k8s-cronjob-audit --format json ./manifests | jq '.findings[] | select(.rule=="suspended")'
```

As a CI gate, failing only on what you actually care about:

```sh
k8s-cronjob-audit --fail-on allow-concurrency,suspended --ignore-name report-nightly ./manifests
```

### Flags

| flag | default | meaning |
| --- | --- | --- |
| `--format` | `text` | `text` or `json` |
| `--fail-on` | empty | comma-separated rules (or `all`) that make the command exit 1 |
| `--ignore-rule` | empty | rules to skip entirely |
| `--ignore-name` | empty | CronJob names to skip entirely |
| `--severity` | `low` | minimum severity to report: `low`, `medium`, `high` |
| `--list-rules` | | print the rule set and exit |
| `--version` | | print the version and exit |

Exit codes: `0` clean (or findings without `--fail-on`), `1` a `--fail-on` rule fired, `2` bad input or bad flags. An unknown rule name in `--fail-on` is an error rather than a filter that matches nothing, because the alternative is a CI gate that passes forever after a typo.

## How it works

The stream is split on top-level `---` lines first, then each document is decoded with `sigs.k8s.io/yaml`. Anything whose `kind` is not `CronJob` under a `batch/*` API version is skipped, so pointing the tool at a whole chart directory is fine. `batch/v1beta1` is audited too: old manifests are exactly the ones with unset fields.

Every field that has a meaningful Kubernetes default is a pointer on the decoded struct. That is the whole trick — `startingDeadlineSeconds: 0` and a missing `startingDeadlineSeconds` are different statements, and only one of them is a finding.

The cron expression is validated by a hand-written parser rather than a scheduler library, which also gives the one number the overlap rule needs: the smallest gap between two consecutive firings in a day, wraparound included. `*/5 * * * *` is 5 minutes, `0,30 * * * *` is 30, `@daily` is 1440. The five standard fields, ranges, steps, lists, month and day names, the `@`-descriptors and `@every <duration>` all parse; an hour field of `25` does not.

`timeZone` is checked the way the API server checks it (`pkg/apis/batch/validation`): non-empty, not `Local`, each path component within the IANA naming rules, and loadable by `time.LoadLocation`. The binary embeds Go's zone database, as kube-controller-manager does, so the answer does not depend on the machine's `/usr/share/zoneinfo`. For the DST check the tool walks the next 12 months of offset changes in that zone and asks whether the schedule fires inside a skipped or repeated stretch of wall-clock time; the upstream scheduler skips a time that does not exist and fires twice on one that occurs twice. Day-of-month, month and weekday fields are honored, so `0 2 1 * *` in `America/New_York` is not flagged. Schedules without a fixed hour (`*/10 * * * *`, `@hourly`, `@every`) do not get the unset-`timeZone` finding.

Each rule is a function from a decoded CronJob to a message, so the test suite is fixtures in `internal/audit/testdata` and no cluster anywhere.

## Install

```sh
go install github.com/moveeeax/k8s-cronjob-audit/cmd/k8s-cronjob-audit@latest
```

Or from a checkout:

```sh
go build -o k8s-cronjob-audit ./cmd/k8s-cronjob-audit
go test ./...
```

## License

MIT

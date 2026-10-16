package audit

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitDocumentsKeepsEveryDocument(t *testing.T) {
	src := "---\na: 1\n---\nb: 2\n---   \nc: 3\n"
	docs := SplitDocuments(src)
	// A leading separator produces an empty first document; callers skip it.
	if len(docs) != 4 {
		t.Fatalf("got %d documents, want 4: %q", len(docs), docs)
	}
	if strings.TrimSpace(docs[0]) != "" {
		t.Errorf("first document should be empty, got %q", docs[0])
	}
	if strings.TrimSpace(docs[3]) != "c: 3" {
		t.Errorf("last document = %q", docs[3])
	}
}

func TestSplitDocumentsIgnoresInlineTripleDash(t *testing.T) {
	// A `---` inside a string value is not a document separator.
	src := "cmd: \"echo ---\"\nother: 1\n"
	if docs := SplitDocuments(src); len(docs) != 1 {
		t.Fatalf("got %d documents, want 1: %q", len(docs), docs)
	}
}

func TestParseStreamPicksCronJobsOutOfHelmOutput(t *testing.T) {
	docs := load(t, "helm-rendered.yaml")
	if len(docs) != 2 {
		t.Fatalf("got %d CronJobs, want 2 (the ConfigMap and Service must be skipped)", len(docs))
	}
	names := []string{docs[0].CronJob.Metadata.Name, docs[1].CronJob.Metadata.Name}
	if !equal(names, []string{"invoice-sweep", "ledger-reconcile"}) {
		t.Fatalf("names = %v", names)
	}
	// batch/v1beta1 is still audited: old manifests are exactly the ones with
	// unset fields.
	if docs[1].CronJob.APIVersion != "batch/v1beta1" {
		t.Errorf("apiVersion = %q", docs[1].CronJob.APIVersion)
	}
}

func TestParseStreamFromStdin(t *testing.T) {
	src := `apiVersion: batch/v1
kind: CronJob
metadata:
  name: from-stdin
spec:
  schedule: "0 1 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: Never
`
	docs, err := ParsePaths([]string{"-"}, strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].CronJob.Metadata.Name != "from-stdin" {
		t.Fatalf("docs = %+v", docs)
	}
	if docs[0].File != "<stdin>" {
		t.Errorf("file = %q, want <stdin>", docs[0].File)
	}
}

func TestParsePathsWalksDirectoriesAndSkipsNonYAML(t *testing.T) {
	docs, err := ParsePaths([]string{"testdata"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) < 5 {
		t.Fatalf("got %d CronJobs from testdata, want every fixture", len(docs))
	}
	for _, d := range docs {
		if ext := filepath.Ext(d.File); ext != ".yaml" && ext != ".yml" {
			t.Errorf("parsed a non-YAML file: %s", d.File)
		}
	}
}

func TestParsePathsMissingPathIsAnError(t *testing.T) {
	if _, err := ParsePaths([]string{"testdata/does-not-exist.yaml"}, nil); err == nil {
		t.Fatal("expected an error for a missing path")
	}
}

func TestParseStreamReportsBrokenYAMLWithLocation(t *testing.T) {
	src := "apiVersion: batch/v1\nkind: CronJob\n  bad: indent\n"
	_, err := ParseStream("broken.yaml", strings.NewReader(src))
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if !strings.Contains(err.Error(), "broken.yaml") {
		t.Errorf("error should name the file, got %v", err)
	}
}

func TestIsCronJob(t *testing.T) {
	cases := []struct {
		apiVersion, kind string
		want             bool
	}{
		{"batch/v1", "CronJob", true},
		{"batch/v1beta1", "CronJob", true},
		{"batch/v2alpha1", "CronJob", true},
		{"batch/v1", "Job", false},
		{"apps/v1", "CronJob", false},
		{"", "", false},
	}
	for _, tc := range cases {
		if got := IsCronJob(tc.apiVersion, tc.kind); got != tc.want {
			t.Errorf("IsCronJob(%q, %q) = %v", tc.apiVersion, tc.kind, got)
		}
	}
}

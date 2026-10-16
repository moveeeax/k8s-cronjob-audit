package audit

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// Document is one YAML document that turned out to be a CronJob, plus where it
// came from so a finding can point at a file instead of at a struct.
type Document struct {
	File    string
	Index   int
	CronJob CronJob
}

var yamlExts = map[string]bool{".yaml": true, ".yml": true}

// SplitDocuments cuts a YAML stream on top-level `---` separators. Helm prints
// one document per template with `# Source:` comments in between, and a plain
// yaml.Unmarshal of that stream silently keeps only the first one.
func SplitDocuments(src string) []string {
	var docs []string
	var cur strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(src))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "---" || strings.HasPrefix(trimmed, "--- ") {
			docs = append(docs, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
	}
	docs = append(docs, cur.String())
	return docs
}

// probe is the cheap first pass: decode just enough to decide whether the
// document is a CronJob before paying for the full struct.
type probe struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
}

// IsCronJob reports whether a decoded document is a batch CronJob of any
// version, so a manifest still on batch/v1beta1 is audited rather than skipped.
func IsCronJob(apiVersion, kind string) bool {
	return kind == "CronJob" && strings.HasPrefix(apiVersion, "batch/")
}

// ParseStream reads one YAML stream and returns every CronJob in it.
func ParseStream(file string, r io.Reader) ([]Document, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", file, err)
	}
	var out []Document
	for i, doc := range SplitDocuments(string(data)) {
		if strings.TrimSpace(stripComments(doc)) == "" {
			continue
		}
		var p probe
		if err := yaml.Unmarshal([]byte(doc), &p); err != nil {
			return nil, fmt.Errorf("%s (document %d): %v", file, i+1, err)
		}
		if !IsCronJob(p.APIVersion, p.Kind) {
			continue
		}
		var cj CronJob
		if err := yaml.Unmarshal([]byte(doc), &cj); err != nil {
			return nil, fmt.Errorf("%s (document %d): %v", file, i+1, err)
		}
		out = append(out, Document{File: file, Index: i + 1, CronJob: cj})
	}
	return out, nil
}

func stripComments(doc string) string {
	var b strings.Builder
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// ParsePaths collects CronJobs from files, directories (walked recursively) and
// "-" for stdin.
func ParsePaths(paths []string, stdin io.Reader) ([]Document, error) {
	var out []Document
	for _, p := range paths {
		if p == "-" {
			docs, err := ParseStream("<stdin>", stdin)
			if err != nil {
				return nil, err
			}
			out = append(out, docs...)
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			docs, err := parseFile(p)
			if err != nil {
				return nil, err
			}
			out = append(out, docs...)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return fs.SkipDir
				}
				return nil
			}
			if !yamlExts[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			docs, err := parseFile(path)
			if err != nil {
				return err
			}
			out = append(out, docs...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func parseFile(path string) ([]Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseStream(path, f)
}

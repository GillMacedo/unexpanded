// Command unexpanded finds $(VAR) references in Kubernetes manifests that
// kubelet will leave as written, and shell-style ${VAR} that Kubernetes never
// expands, before the pods start with the wrong arguments.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

type report struct {
	Findings []finding `json:"findings"`
	Summary  summary   `json:"summary"`
}

type summary struct {
	Errors     int            `json:"errors"`
	Warnings   int            `json:"warnings"`
	Files      int            `json:"files"`
	Objects    int            `json:"objects"`
	Workloads  int            `json:"workloads"`
	Containers int            `json:"containers"`
	Strings    int            `json:"stringsWithDollar"`
	References int            `json:"references"`
	Skipped    map[string]int `json:"skippedKinds"`
	NotChecked map[string]int `json:"notChecked"`
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("unexpanded", flag.ContinueOnError)
	fs.SetOutput(stderr)
	failOn := fs.String("fail-on", "warn", "exit 1 on findings at or above this severity: error, warn or never")
	format := fs.String("format", "text", "output format: text or json")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: unexpanded [--fail-on error|warn|never] [--format text|json] FILE|DIR|- ...\n\n")
		fmt.Fprintf(stderr, "Reads Kubernetes manifests (YAML or JSON, such as helm template or kubectl get -o yaml output)\nand reports $(VAR) references that kubelet will leave as written.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "unexpanded", version)
		return 0
	}
	switch *failOn {
	case "error", "warn", "never":
	default:
		fmt.Fprintf(stderr, "unexpanded: --fail-on must be error, warn or never, not %q\n", *failOn)
		return 2
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "unexpanded: --format must be text or json, not %q\n", *format)
		return 2
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	rep, err := check(fs.Args(), stdin)
	if err != nil {
		var r *refusal
		if errors.As(err, &r) {
			fmt.Fprintf(stderr, "unexpanded: can't trust the input, nothing was checked: %s\n", r.msg)
		} else {
			fmt.Fprintf(stderr, "unexpanded: %v\n", err)
		}
		return 2
	}
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if rep.Findings == nil {
			rep.Findings = []finding{}
		}
		if err := enc.Encode(rep); err != nil {
			return 2
		}
	} else {
		printText(stdout, rep)
	}
	switch {
	case *failOn == "error" && rep.Summary.Errors > 0:
		return 1
	case *failOn == "warn" && rep.Summary.Errors+rep.Summary.Warnings > 0:
		return 1
	}
	return 0
}

func check(args []string, stdin io.Reader) (*report, error) {
	files, err := collectFiles(args)
	if err != nil {
		return nil, err
	}
	stdinUsed := false
	var objs []*object
	for _, f := range files {
		if f == "-" {
			if stdinUsed {
				return nil, refuse("- (stdin) is given more than once")
			}
			stdinUsed = true
		}
		data, err := readInput(f, stdin)
		if err != nil {
			return nil, err
		}
		got, err := parseFile(f, data)
		if err != nil {
			return nil, err
		}
		objs = append(objs, got...)
	}
	sum := summary{Files: len(files), Objects: len(objs), Skipped: map[string]int{}, NotChecked: map[string]int{}}
	var pods []*pod
	var notChecked []finding
	for _, o := range objs {
		p, err := readPod(o)
		if err != nil {
			return nil, err
		}
		if p == nil {
			sum.Skipped[o.kind]++
			if o.kind != "CustomResourceDefinition" && hasKey(get(o.node, "spec"), "containers") {
				// A custom resource with containers somewhere unexpanded
				// doesn't know is reported, never skipped quietly.
				sum.NotChecked[o.kind]++
				notChecked = append(notChecked, finding{
					Severity: "WARN", Code: "not-checked", File: o.file, Line: o.node.Line,
					Object: o.kind + " " + o.name, Namespace: o.namespace,
					Message: "this resource has containers at a path unexpanded doesn't read, so their references weren't checked",
					Fix:     "check it by hand, or pass the pods its operator creates, from kubectl get pod -o yaml",
				})
			}
			continue
		}
		pods = append(pods, p)
	}
	if len(pods) == 0 {
		return nil, refuse("no workloads with containers in %s (%s); give the manifests of Deployments, StatefulSets, DaemonSets, Jobs, CronJobs or Pods", plural(len(objs), "object", "objects"), kindList(sum.Skipped))
	}
	ix := buildIndex(objs)
	var st stats
	findings := append(checkPods(pods, ix, &st), notChecked...)
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	for _, f := range findings {
		if f.Severity == "ERROR" {
			sum.Errors++
		} else {
			sum.Warnings++
		}
	}
	sum.Workloads, sum.Containers, sum.Strings, sum.References = st.workloads, st.containers, st.strings, st.references
	return &report{Findings: findings, Summary: sum}, nil
}

func kindList(m map[string]int) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func printText(w io.Writer, rep *report) {
	for _, f := range rep.Findings {
		if f.Container == "" {
			fmt.Fprintf(w, "%s %s: %s\n", f.Severity, f.Code, f.Object)
		} else {
			fmt.Fprintf(w, "%s %s: %s, container %s, %s\n", f.Severity, f.Code, f.Object, f.Container, f.Field)
			fmt.Fprintf(w, "  %s\n", quoteValue(f.Value))
		}
		fmt.Fprintf(w, "  %s\n", f.Message)
		for _, h := range f.Hints {
			fmt.Fprintf(w, "  %s\n", h)
		}
		fmt.Fprintf(w, "  fix: %s\n", f.Fix)
		fmt.Fprintf(w, "  at %s:%d\n\n", f.File, f.Line)
	}
	s := rep.Summary
	head := "No problems"
	if s.Errors+s.Warnings > 0 {
		head = plural(s.Errors, "error", "errors") + " and " + plural(s.Warnings, "warning", "warnings")
	}
	fmt.Fprintf(w, "%s: checked %s in %s of %s, from %s with %s.\n", head,
		plural(s.References, "$ reference", "$ references"),
		plural(s.Containers, "container", "containers"), plural(s.Workloads, "workload", "workloads"),
		plural(s.Files, "file", "files"), plural(s.Objects, "object", "objects"))
}

func quoteValue(v string) string {
	if i := strings.IndexByte(v, '\n'); i >= 0 {
		lines := strings.Count(v, "\n")
		v = v[:i] + fmt.Sprintf(" ... (%d more lines)", lines)
	}
	if len(v) > 160 {
		v = v[:160] + "..."
	}
	return v
}

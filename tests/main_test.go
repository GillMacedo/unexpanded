// Package tests runs unexpanded as a binary, the way the skill does, against
// the examples, real manifests in testdata/real, and inputs it must refuse.
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "unexpanded-tests")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "unexpanded")
	cmd := exec.Command("go", "build", "-o", bin, "./plugin/scripts")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building unexpanded: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	code           int
	stdout, stderr string
}

// runTool runs unexpanded from the repository root.
func runTool(t *testing.T, stdin []byte, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = ".."
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{code, out.String(), errb.String()}
}

type jsonFinding struct {
	Severity, Code, File, Object, Container, Field, Value, Reference, Message, Fix string
	Line                                                                           int
	Hints                                                                          []string
}

type jsonReport struct {
	Findings []jsonFinding
	Summary  struct {
		Errors, Warnings, Files, Objects, Workloads, Containers, References int
	}
}

func runJSON(t *testing.T, stdin []byte, args ...string) (jsonReport, result) {
	t.Helper()
	r := runTool(t, stdin, append([]string{"--format", "json"}, args...)...)
	var rep jsonReport
	if r.code == 2 {
		return rep, r
	}
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("bad JSON (%v):\n%s\nstderr: %s", err, r.stdout, r.stderr)
	}
	return rep, r
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", p))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Paths relative to the repository root.
const (
	arcadeBefore  = "examples/arcadedb/statefulset-before.yaml"
	arcadeAfter   = "examples/arcadedb/statefulset-after.yaml"
	pitfallBroken = "examples/pitfalls/broken.yaml"
	pitfallFixed  = "examples/pitfalls/fixed.yaml"
	realDir       = "tests/testdata/real"
	badDir        = "tests/testdata/bad"
)

func codesOf(rep jsonReport) []string {
	var out []string
	for _, f := range rep.Findings {
		out = append(out, f.Code)
	}
	return out
}

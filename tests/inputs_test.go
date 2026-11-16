package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustRefuse runs args under every --fail-on and both formats, and wants
// exit 2, nothing on stdout, and the refusal on stderr.
func mustRefuse(t *testing.T, name string, stdin []byte, args ...string) {
	t.Helper()
	for _, fail := range []string{"error", "warn", "never"} {
		for _, format := range []string{"text", "json"} {
			all := append([]string{"--fail-on", fail, "--format", format}, args...)
			r := runTool(t, stdin, all...)
			if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "can't trust the input, nothing was checked") {
				t.Errorf("%s (--fail-on %s --format %s): exit %d, want 2 with nothing on stdout\nstdout: %.300s\nstderr: %s", name, fail, format, r.code, r.stdout, r.stderr)
				return
			}
		}
	}
}

// Every file in testdata/bad is refused alone, next to a real manifest with
// findings, next to a clean one, and on stdin.
func TestBadInputs(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", badDir))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.Name() == "README.md" {
			continue
		}
		n++
		p := badDir + "/" + e.Name()
		mustRefuse(t, p, nil, p)
		mustRefuse(t, p+" after a file with findings", nil, arcadeBefore, p)
		mustRefuse(t, p+" before a clean file", nil, p, arcadeAfter)
		mustRefuse(t, p+" on stdin", readFile(t, p), pitfallBroken, "-")
	}
	if n < 20 {
		t.Errorf("only %d bad inputs", n)
	}
}

// Bad inputs built from the real files: cut, emptied, and the like.
func TestBadVariants(t *testing.T) {
	dir := t.TempDir()
	before := readFile(t, arcadeBefore)
	broken := readFile(t, pitfallBroken)
	cases := map[string][]byte{
		"no final line break":                before[:len(before)-1],
		"half of the file":                   before[:len(before)/2],
		"cut mid-line then newline":          append(append([]byte{}, broken[:strings.Index(string(broken), "image: ghcr.io")+6]...), '\n'),
		"NUL byte":                           append(append([]byte{}, broken[:100]...), append([]byte{0}, broken[100:]...)...),
		"not UTF-8":                          append(append([]byte{}, broken[:100]...), append([]byte{0xff, 0xfe}, broken[100:]...)...),
		"failed command before the manifest": append([]byte("Error: INSTALLATION FAILED: cannot re-use a name that is still in use\n"), broken...),
		"klog line before the manifest":      append([]byte("W1116 10:00:00.000000   12345 warnings.go:70] unknown field \"spec.foo\"\n"), broken...),
		"JSON list":                          []byte("[1, 2, 3]\n"),
		"a number":                           []byte("42\n"),
		"curl error":                         []byte("curl: (7) Failed to connect to localhost port 6443 after 0 ms: Couldn't connect to server\n"),
		"env value is a map":                 []byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\nspec:\n  containers:\n    - name: a\n      image: x\n      env:\n        - name: A\n          value: {b: c}\n"),
		"args is a string":                   []byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\nspec:\n  containers:\n    - name: a\n      image: x\n      args: \"--ip=$(IP)\"\n"),
		"container without name":             []byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\nspec:\n  containers:\n    - image: x\n"),
		"env entry without name":             []byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\nspec:\n  containers:\n    - name: a\n      image: x\n      env:\n        - value: x\n"),
		"CronJob without job template":       []byte("apiVersion: batch/v1\nkind: CronJob\nmetadata:\n  name: c\nspec:\n  schedule: \"* * * * *\"\n"),
		"List with a bad item":               []byte("apiVersion: v1\nkind: List\nitems:\n  - hello\n"),
	}
	for name, content := range cases {
		p := writeFile(t, dir, strings.ReplaceAll(name, " ", "-")+".yaml", content)
		mustRefuse(t, name, nil, p)
		mustRefuse(t, name+" next to a real file", nil, arcadeAfter, p)
	}
	// A file with only Services is fine next to workloads, since Services
	// define variables, but alone there is nothing to check.
	mustRefuse(t, "only Services", nil, "tests/testdata/only-services.yaml")
	if r := runTool(t, nil, "tests/testdata/only-services.yaml", arcadeAfter); r.code != 0 {
		t.Errorf("Services next to a workload: exit %d\n%s", r.code, r.stderr)
	}
	mustRefuse(t, "missing file", nil, "does/not/exist.yaml")
	mustRefuse(t, "directory with no manifests", nil, "plugin/.claude-plugin")
	mustRefuse(t, "stdin twice", broken, "-", "-")
	mustRefuse(t, "empty stdin", []byte{}, "-")
}

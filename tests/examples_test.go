package tests

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ArcadeDB's StatefulSet before its fix (PR 6851) passed ${HOSTNAME} and
// ${rootPassword} to the JVM as written. The fixed file must be quiet.
func TestArcadeDB(t *testing.T) {
	rep, r := runJSON(t, nil, arcadeBefore)
	if r.code != 1 {
		t.Fatalf("exit %d, want 1\n%s", r.code, r.stderr)
	}
	var refs []string
	for _, f := range rep.Findings {
		if f.Code != "shell-syntax" || f.Severity != "WARN" {
			t.Errorf("unexpected finding %+v", f)
		}
		refs = append(refs, f.Reference)
	}
	if !reflect.DeepEqual(refs, []string{"${HOSTNAME}", "${rootPassword}"}) {
		t.Errorf("got %q", refs)
	}
	if !strings.Contains(rep.Findings[1].Fix, "write $(rootPassword)") {
		t.Errorf("rootPassword is declared, so the fix should say to write $(rootPassword): %s", rep.Findings[1].Fix)
	}
	// Under --fail-on error the warnings are still printed, and exit is 0.
	r = runTool(t, nil, "--fail-on", "error", arcadeBefore)
	if r.code != 0 || !strings.Contains(r.stdout, "0 errors and 2 warnings") {
		t.Errorf("--fail-on error: exit %d\n%s", r.code, r.stdout)
	}
	r = runTool(t, nil, "--fail-on", "never", arcadeBefore)
	if r.code != 0 || !strings.Contains(r.stdout, "WARN shell-syntax") {
		t.Errorf("--fail-on never: exit %d\n%s", r.code, r.stdout)
	}

	rep, r = runJSON(t, nil, arcadeAfter)
	if r.code != 0 || len(rep.Findings) != 0 || rep.Summary.References != 1 {
		t.Errorf("fixed file: exit %d, %d findings, %d references\n%s", r.code, len(rep.Findings), rep.Summary.References, r.stdout)
	}
}

func TestPitfalls(t *testing.T) {
	rep, r := runJSON(t, nil, pitfallBroken)
	if r.code != 1 {
		t.Fatalf("exit %d", r.code)
	}
	want := []string{"undefined", "defined-later", "shell-syntax", "undefined", "probe-static", "hook-literal", "undefined"}
	if got := codesOf(rep); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	for _, fail := range []string{"error", "warn"} {
		if r := runTool(t, nil, "--fail-on", fail, pitfallBroken); r.code != 1 {
			t.Errorf("--fail-on %s: exit %d", fail, r.code)
		}
	}
	for _, fail := range []string{"error", "warn", "never"} {
		r := runTool(t, nil, "--fail-on", fail, pitfallFixed)
		if r.code != 0 || !strings.HasPrefix(r.stdout, "No problems: checked 7 $ references") {
			t.Errorf("fixed, --fail-on %s: exit %d\n%s", fail, r.code, r.stdout)
		}
	}
}

// Real manifests: released install files and charts rendered with helm
// template and their default values. None of them has a finding, so each
// must be quiet, alone and all together.
func TestRealManifestsAreQuiet(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", realDir))
	if err != nil {
		t.Fatal(err)
	}
	refs := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		p := realDir + "/" + e.Name()
		rep, r := runJSON(t, nil, p)
		if r.code != 0 || len(rep.Findings) != 0 {
			t.Errorf("%s: exit %d, findings %+v\n%s", p, r.code, rep.Findings, r.stderr)
		}
		refs += rep.Summary.References
	}
	rep, r := runJSON(t, nil, realDir)
	if r.code != 0 || rep.Summary.References != refs || rep.Summary.Files != 11 {
		t.Errorf("directory: exit %d, %d references (want %d), %d files", r.code, rep.Summary.References, refs, rep.Summary.Files)
	}
	if refs < 100 {
		t.Errorf("only %d references in the real manifests", refs)
	}
}

// The same file from stdin gives the same result.
func TestStdin(t *testing.T) {
	r := runTool(t, readFile(t, pitfallBroken), "-")
	if r.code != 1 || !strings.Contains(r.stdout, "6 errors and 1 warning") || !strings.Contains(r.stdout, "at -:38") {
		t.Errorf("exit %d\n%s", r.code, r.stdout)
	}
}

func TestFlags(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--fail-on", "sometimes", pitfallFixed},
		{"--format", "xml", pitfallFixed},
		{"--no-such-flag", pitfallFixed},
	} {
		r := runTool(t, nil, args...)
		if r.code != 2 || r.stdout != "" {
			t.Errorf("%q: exit %d, stdout %q", args, r.code, r.stdout)
		}
	}
	if r := runTool(t, nil, "--version"); r.code != 0 || r.stdout != "unexpanded 0.1.0\n" {
		t.Errorf("--version: %d %q", r.code, r.stdout)
	}
}

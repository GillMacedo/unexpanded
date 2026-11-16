package tests

import (
	"testing"
)

// A manifest cut anywhere except right after a line break is refused,
// because it doesn't end with one. A cut right after a line break can be a
// valid, shorter manifest that no tool can tell apart from a complete one.
// Such a cut is refused when it leaves an object without its name, pod spec,
// containers or image, and otherwise reports only what is left, which the
// summary counts show.
func TestCutEverywhere(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{arcadeBefore, pitfallBroken, realDir + "/helm-hashicorp-vault.yaml"} {
		data := readFile(t, path)
		full, r := runJSON(t, nil, path)
		if r.code == 2 {
			t.Fatal(r.stderr)
		}
		mid, lineRefused, lineRead := 0, 0, 0
		for i := 1; i < len(data); i++ {
			onLine := data[i-1] == '\n'
			if !onLine && i%7 != 0 {
				continue
			}
			p := writeFile(t, dir, "cut.yaml", data[:i])
			rep, r := runJSON(t, nil, "--fail-on", "never", p)
			if !onLine {
				mid++
				if r.code != 2 || r.stdout != "" {
					t.Fatalf("%s cut at byte %d (mid-line): exit %d\n%s", path, i, r.code, r.stdout)
				}
				continue
			}
			if r.code == 2 {
				lineRefused++
				continue
			}
			lineRead++
			if rep.Summary.References > full.Summary.References || rep.Summary.Containers > full.Summary.Containers {
				t.Errorf("%s cut at line break %d reads more than the whole file", path, i)
			}
		}
		t.Logf("%s: %d mid-line cuts refused; line cuts: %d refused, %d read as shorter files", path, mid, lineRefused, lineRead)
	}
}

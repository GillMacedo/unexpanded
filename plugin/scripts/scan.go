package main

import (
	"path"
	"strings"
)

// dollarToken is one place in a string where a $ does something, or looks
// like it was meant to.
type dollarToken struct {
	kind   tokenKind
	name   string // the variable name, or the text inside $( )
	offset int    // byte offset of the $ in the string
	text   string // the token as written, such as $(FOO) or ${FOO}
}

type tokenKind int

const (
	tokRef      tokenKind = iota // $(NAME): Kubernetes looks NAME up
	tokBrace                     // ${NAME}: shell syntax, Kubernetes leaves it alone
	tokBare                      // $NAME with an upper-case NAME: shell syntax
	tokUnclosed                  // $( with no closing ): Kubernetes leaves it alone
)

// scanDollars reads s the way k8sExpand does (a copy of kubelet's code), so
// $$ is an escape and $(NAME) is a reference, and also notes the shell forms
// ${NAME} and $NAME that Kubernetes doesn't expand. TestScanMatchesKubernetes
// checks that it finds the same references as k8sExpand.
func scanDollars(s string) []dollarToken {
	var out []dollarToken
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 >= len(s) {
			continue
		}
		next := s[i+1]
		switch {
		case next == '$':
			// Escaped: $$ becomes a literal $, and whatever follows is text.
			i++
		case next == '(':
			end := strings.IndexByte(s[i+2:], ')')
			if end < 0 {
				out = append(out, dollarToken{kind: tokUnclosed, offset: i, text: s[i:]})
				i++
				continue
			}
			name := s[i+2 : i+2+end]
			out = append(out, dollarToken{kind: tokRef, name: name, offset: i, text: s[i : i+3+end]})
			i += 2 + end
		case next == '{':
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				continue
			}
			inner := s[i+2 : i+2+end]
			name := leadingIdent(inner)
			if name != "" {
				out = append(out, dollarToken{kind: tokBrace, name: name, offset: i, text: s[i : i+3+end]})
			}
			// Kubernetes reads on after the {, so a $(NAME) inside ${...}
			// is still expanded.
			i++
		case next == '_' || (next >= 'A' && next <= 'Z'):
			name := leadingIdent(s[i+1:])
			if isEnvStyle(name) && len(name) >= 2 {
				out = append(out, dollarToken{kind: tokBare, name: name, offset: i, text: "$" + name})
			}
			i += len(name)
		}
	}
	return out
}

// leadingIdent returns the shell identifier at the start of s.
func leadingIdent(s string) string {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return s[:i]
	}
	return s
}

// isEnvStyle reports whether name looks like an environment variable written
// in the usual upper-case style, such as POD_IP. In a shell script, $(name)
// with such a name is almost always meant as a variable, while $(hostname -i)
// is command substitution.
func isEnvStyle(name string) bool {
	if name == "" {
		return false
	}
	hasLetter := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z':
			hasLetter = true
		case c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return hasLetter
}

// isValidEnvName follows Kubernetes' rule for env var names: letters, digits,
// '_', '-' and '.', not starting with a digit.
func isValidEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '_' || c == '-' || c == '.':
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

var shells = map[string]bool{"sh": true, "bash": true, "ash": true, "dash": true, "zsh": true, "ksh": true, "mksh": true}
var initWrappers = map[string]bool{"tini": true, "dumb-init": true, "catatonit": true}

// shellScriptIndex returns the index in argv of the script that a shell runs
// with -c, such as 2 in ["sh", "-c", "echo $FOO"], or -1 if argv doesn't
// start a shell with an inline script. It looks through /usr/bin/env, busybox
// and init wrappers such as tini.
func shellScriptIndex(argv []string) int {
	i := 0
	for i < len(argv) {
		base := path.Base(argv[i])
		switch {
		case base == "env":
			i++
			for i < len(argv) && (strings.HasPrefix(argv[i], "-") || strings.Contains(argv[i], "=")) {
				i++
			}
			continue
		case base == "busybox" && i+1 < len(argv) && shells[argv[i+1]]:
			i++
			continue
		case initWrappers[base]:
			i++
			for i < len(argv) && strings.HasPrefix(argv[i], "-") {
				done := argv[i] == "--"
				i++
				if done {
					break
				}
			}
			continue
		case shells[base]:
			return shellScriptAfter(argv, i+1)
		}
		return -1
	}
	return -1
}

func shellScriptAfter(argv []string, i int) int {
	sawC := false
	for ; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--":
			if sawC && i+1 < len(argv) {
				return i + 1
			}
			return -1
		case a == "-o" || a == "+o" || a == "-O" || a == "+O":
			i++
		case strings.HasPrefix(a, "--"):
			// Long options such as --login or --norc.
		case strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+"):
			if strings.HasPrefix(a, "-") && strings.Contains(a, "c") {
				sawC = true
			}
		default:
			if sawC {
				return i
			}
			return -1
		}
	}
	return -1
}

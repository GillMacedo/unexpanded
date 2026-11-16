package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// refusal is an input that can't be trusted. Any refusal ends the run with
// exit 2 before anything is printed on stdout.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refuse(format string, args ...any) error {
	return &refusal{msg: fmt.Sprintf(format, args...)}
}

// object is one Kubernetes object read from a file.
type object struct {
	file      string
	kind      string
	name      string
	namespace string
	node      *Node // the object's mapping node
}

// collectFiles expands directories into their .yaml, .yml and .json files.
func collectFiles(args []string) ([]string, error) {
	var files []string
	for _, a := range args {
		if a == "-" {
			files = append(files, a)
			continue
		}
		info, err := os.Stat(a)
		if err != nil {
			return nil, refuse("%s: %v", a, err)
		}
		if !info.IsDir() {
			files = append(files, a)
			continue
		}
		var found []string
		err = filepath.WalkDir(a, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if p != a && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			switch strings.ToLower(filepath.Ext(p)) {
			case ".yaml", ".yml", ".json":
				found = append(found, p)
			}
			return nil
		})
		if err != nil {
			return nil, refuse("%s: %v", a, err)
		}
		if len(found) == 0 {
			return nil, refuse("%s: directory has no .yaml, .yml or .json files", a)
		}
		sort.Strings(found)
		files = append(files, found...)
	}
	return files, nil
}

func readInput(name string, stdin io.Reader) ([]byte, error) {
	if name == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, refuse("stdin: %v", err)
		}
		return data, nil
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, refuse("%v", err)
	}
	return data, nil
}

// failedOutput lists the starts of lines that tools print when they fail.
// They are checked before parsing, because many of them are valid YAML.
var failedOutput = []string{
	"Error: ", "error: ", "Error from server", "error validating", "error parsing",
	"panic: ", "fatal: ", "FATAL", "--- FAIL", "FAIL\t", "# command-line-arguments",
	"Usage:", "usage:", "<!DOCTYPE", "<html", "<HTML", "<?xml",
	"The connection to the server", "Unable to connect to the server",
	"No resources found", "zsh: ", "bash: ", "sh: ", "/bin/sh: ",
	"walk.go:", "kustomize: ", "WARNING: ", "W1", "E1", "I1",
}

func looksLikeFailedOutput(data []byte) (string, bool) {
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || t == "---" {
			continue
		}
		for _, p := range failedOutput {
			if strings.HasPrefix(line, p) {
				// Klog lines such as "E1116 10:00:00.000000 ..." start with a
				// letter and four digits.
				if len(p) == 2 && !(len(line) > 5 && isDigits(line[1:5])) {
					continue
				}
				return line, true
			}
		}
	}
	return "", false
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func firstLine(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			if len(t) > 80 {
				t = t[:80] + "..."
			}
			return t
		}
	}
	return ""
}

// parseFile reads every Kubernetes object in a YAML or JSON file, including
// the items of a List. It refuses anything that isn't a complete set of
// Kubernetes objects.
func parseFile(name string, data []byte) ([]*object, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, refuse("%s: file is empty", name)
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return nil, refuse("%s: file is binary or not UTF-8 text", name)
	}
	if line, ok := looksLikeFailedOutput(data); ok {
		return nil, refuse("%s: this looks like the output of a failed command, not manifests: %q", name, strings.TrimSpace(line))
	}
	if data[len(data)-1] != '\n' && !(data[len(data)-1] == '}' && bytes.HasPrefix(bytes.TrimSpace(data), []byte("{"))) {
		return nil, refuse("%s: the file doesn't end with a line break, so it may be cut off. If it is complete, add a line break at the end", name)
	}
	dec := NewDecoder(bytes.NewReader(data))
	var objs []*object
	docs := 0
	for {
		var doc Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			hint := ""
			if bytes.Contains(data, []byte("{{")) {
				hint = "; it has {{ }}, so it may be an unrendered Helm template: run helm template and check its output"
			}
			return nil, refuse("%s: not valid YAML or JSON: %v%s", name, err, hint)
		}
		docs++
		root := &doc
		if root.Kind == DocumentNode {
			if len(root.Content) == 0 {
				continue
			}
			root = root.Content[0]
		}
		if root.Kind == ScalarNode && (root.Tag == "!!null" || root.Value == "") {
			continue // an empty document between --- lines
		}
		got, err := objectsFrom(name, root)
		if err != nil {
			return nil, err
		}
		objs = append(objs, got...)
	}
	if len(objs) == 0 {
		return nil, refuse("%s: no Kubernetes objects in the file (first line: %q)", name, firstLine(data))
	}
	if bytes.Contains(data, []byte("{{")) && containsTemplate(objs) {
		return nil, refuse("%s: has {{ }} in a container field, so it looks like an unrendered Helm template: run helm template and check its output", name)
	}
	return objs, nil
}

func objectsFrom(file string, n *Node) ([]*object, error) {
	if n.Kind != MappingNode {
		return nil, refuse("%s:%d: expected a Kubernetes object, found %s %q; is it the output of a failed command?", file, n.Line, kindName(n), shorten(n.Value))
	}
	apiVersion, _ := scalar(get(n, "apiVersion"))
	kind, _ := scalar(get(n, "kind"))
	if apiVersion == "" || kind == "" {
		return nil, refuse("%s:%d: a YAML document without apiVersion and kind is not a Kubernetes object", file, n.Line)
	}
	if kind == "Status" && apiVersion == "v1" {
		msg, _ := scalar(get(n, "message"))
		return nil, refuse("%s:%d: this is an error from the Kubernetes API, not a manifest: %s", file, n.Line, shorten(msg))
	}
	if strings.HasSuffix(kind, "List") && get(n, "items") != nil {
		items := get(n, "items")
		if items.Kind != SequenceNode {
			return nil, refuse("%s:%d: %s items is not a list", file, items.Line, kind)
		}
		var out []*object
		for _, it := range items.Content {
			got, err := objectsFrom(file, it)
			if err != nil {
				return nil, err
			}
			out = append(out, got...)
		}
		return out, nil
	}
	meta := get(n, "metadata")
	name, _ := scalar(get(meta, "name"))
	if name == "" {
		name, _ = scalar(get(meta, "generateName"))
	}
	if name == "" {
		return nil, refuse("%s:%d: %s has no metadata.name; is the file cut off?", file, n.Line, kind)
	}
	ns, _ := scalar(get(meta, "namespace"))
	return []*object{{file: file, kind: kind, name: name, namespace: ns, node: n}}, nil
}

func containsTemplate(objs []*object) bool {
	for _, o := range objs {
		if p := podSpecOf(o); p != nil && strings.Contains(nodeText(p), "{{") {
			for _, c := range append(seq(get(p, "containers")), seq(get(p, "initContainers"))...) {
				img, _ := scalar(get(c, "image"))
				nm, _ := scalar(get(c, "name"))
				if strings.Contains(img, "{{") || strings.Contains(nm, "{{") {
					return true
				}
			}
		}
	}
	return false
}

// nodeText joins the scalar values under n, for quick searches.
func nodeText(n *Node) string {
	var b strings.Builder
	var walk func(*Node)
	walk = func(n *Node) {
		if n == nil {
			return
		}
		if n.Kind == ScalarNode {
			b.WriteString(n.Value)
			b.WriteByte('\n')
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func kindName(n *Node) string {
	switch n.Kind {
	case SequenceNode:
		return "a list"
	case ScalarNode:
		return "the text"
	case AliasNode:
		return "an alias"
	}
	return "something else"
}

func shorten(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + "..."
	}
	if len(s) > 80 {
		s = s[:80] + "..."
	}
	return s
}

// get returns the value of key in a mapping node, or nil.
func get(n *Node, key string) *Node {
	if n == nil || n.Kind != MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			v := n.Content[i+1]
			if v.Kind == AliasNode && v.Alias != nil {
				v = v.Alias
			}
			if v.Kind == ScalarNode && v.Tag == "!!null" {
				// key: with no value is the same as no key, for Kubernetes.
				return nil
			}
			return v
		}
	}
	// YAML merge keys: <<: *base or <<: [*a, *b]. Keys written in the
	// mapping itself win, so these are read last.
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value != "<<" || n.Content[i].Tag != "!!merge" {
			continue
		}
		m := n.Content[i+1]
		var srcs []*Node
		if m.Kind == SequenceNode {
			srcs = m.Content
		} else {
			srcs = []*Node{m}
		}
		for _, src := range srcs {
			if src.Kind == AliasNode && src.Alias != nil {
				src = src.Alias
			}
			if v := get(src, key); v != nil {
				return v
			}
		}
	}
	return nil
}

// hasKey reports whether key appears anywhere under n.
func hasKey(n *Node, key string) bool {
	if n == nil {
		return false
	}
	if n.Kind == MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return true
			}
		}
	}
	for _, c := range n.Content {
		if hasKey(c, key) {
			return true
		}
	}
	return false
}

func scalar(n *Node) (string, bool) {
	if n == nil || n.Kind != ScalarNode {
		return "", false
	}
	if n.Tag == "!!null" {
		return "", false
	}
	return n.Value, true
}

func seq(n *Node) []*Node {
	if n == nil || n.Kind != SequenceNode {
		return nil
	}
	return n.Content
}

func boolValue(n *Node) (bool, bool) {
	s, ok := scalar(n)
	if !ok {
		return false, false
	}
	switch s {
	case "true", "True", "TRUE":
		return true, true
	case "false", "False", "FALSE":
		return false, true
	}
	return false, false
}

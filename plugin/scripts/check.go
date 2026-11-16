package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type finding struct {
	Severity  string   `json:"severity"`
	Code      string   `json:"code"`
	File      string   `json:"file"`
	Line      int      `json:"line"`
	Object    string   `json:"object"`
	Namespace string   `json:"namespace,omitempty"`
	Container string   `json:"container"`
	Field     string   `json:"field"`
	Value     string   `json:"value"`
	Reference string   `json:"reference"`
	Message   string   `json:"message"`
	Hints     []string `json:"hints,omitempty"`
	Fix       string   `json:"fix"`
}

type context int

const (
	ctxEnvValue   context = iota // an env value: expanded in order
	ctxPlain                     // command or args with no shell
	ctxShell                     // the script of sh -c
	ctxProbe                     // an exec probe command, plain
	ctxProbeShell                // an exec probe command, sh -c script
	ctxHook                      // a lifecycle exec command, plain
	ctxHookShell                 // a lifecycle exec command, sh -c script
	ctxSubPath                   // volumeMounts[].subPathExpr
)

func (c context) shell() bool { return c == ctxShell || c == ctxProbeShell || c == ctxHookShell }

type stats struct {
	workloads  int
	containers int
	strings    int // strings that contain a $ token
	references int // $ tokens checked
}

// containerCheck holds what is known about one container's variables.
type containerCheck struct {
	ix        *index
	p         *pod
	c         *container
	svc       map[string]string
	defined   map[string]string // name -> where it comes from
	final     map[string]string
	static    map[string]*envVar // what exec probes see: env names only
	unknown   []string           // envFrom sources not in the inputs
	optMiss   map[string]string  // optional key refs whose key isn't there
	envIndex  int                // index of the env var being expanded
	findings  []finding
	st        *stats
	shellNote string
}

func checkPods(pods []*pod, ix *index, st *stats) []finding {
	var out []finding
	for _, p := range pods {
		st.workloads++
		for _, c := range p.containers {
			st.containers++
			cc := &containerCheck{ix: ix, p: p, c: c, st: st, optMiss: map[string]string{}}
			cc.run()
			out = append(out, cc.findings...)
		}
	}
	return out
}

var runtimeVars = map[string]string{
	"HOSTNAME": "the container runtime sets HOSTNAME",
	"HOME":     "the image or the container runtime sets HOME",
	"PATH":     "the image or the container runtime sets PATH",
	"PWD":      "the shell sets PWD",
	"USER":     "the image or a shell sets USER",
	"SHELL":    "the image or a shell sets SHELL",
	"TERM":     "the container runtime sets TERM",
	"LANG":     "the image sets LANG",
}

func (cc *containerCheck) run() {
	c, ns := cc.c, cc.p.obj.namespace
	cc.svc = cc.ix.serviceEnv(ns, cc.p.serviceLinks)
	cc.defined = map[string]string{}
	for _, src := range c.envFrom {
		ks := cc.ix.keys(src.kind, src.name, ns)
		if ks == nil {
			cc.unknown = append(cc.unknown, src.kind+" "+src.name)
			continue
		}
		for k := range ks.keys {
			cc.defined[src.prefix+k] = "envFrom " + src.kind + " " + src.name
		}
	}
	cc.static = map[string]*envVar{}
	for i := range c.env {
		ev := &c.env[i]
		cc.static[ev.name] = ev
	}
	for i := range c.env {
		ev := &c.env[i]
		cc.envIndex = i
		if ev.value != nil && ev.value.value != "" {
			cc.check(*ev.value, ctxEnvValue, "env "+ev.name, func(n string) (string, bool) {
				if v, ok := cc.defined[n]; ok {
					return v, true
				}
				v, ok := cc.svc[n]
				return v, ok
			})
		} else if ev.source == "configMapKeyRef" || ev.source == "secretKeyRef" {
			kind := "ConfigMap"
			if ev.source == "secretKeyRef" {
				kind = "Secret"
			}
			if ks := cc.ix.keys(kind, ev.refName, ns); ks != nil && !ks.keys[ev.refKey] {
				if ev.optional {
					cc.optMiss[ev.name] = fmt.Sprintf("%s comes from key %s of %s %s, which that %s doesn't have, and the reference is optional, so kubelet doesn't set %s", ev.name, ev.refKey, kind, ev.refName, kind, ev.name)
					continue
				}
				cc.add(finding{
					Severity: "ERROR", Code: "missing-key", Line: ev.node.Line, Field: "env " + ev.name,
					Value:   ev.refKey,
					Message: fmt.Sprintf("%s %s in the inputs has no key %s, so the container won't start: kubelet reports CreateContainerConfigError", kind, ev.refName, ev.refKey),
					Fix:     fmt.Sprintf("add key %s to %s %s, fix the key name, or set optional: true", ev.refKey, kind, ev.refName),
				})
			}
		}
		cc.defined[ev.name] = "env"
	}
	cc.envIndex = -1
	cc.final = map[string]string{}
	for k, v := range cc.svc {
		cc.final[k] = v
	}
	for k, v := range cc.defined {
		cc.final[k] = v
	}
	lookupFinal := func(n string) (string, bool) { v, ok := cc.final[n]; return v, ok }

	// command and args
	var argv []stringAt
	var fields []string
	for i, s := range c.command {
		argv = append(argv, s)
		fields = append(fields, fmt.Sprintf("command[%d]", i))
	}
	for i, s := range c.args {
		argv = append(argv, s)
		fields = append(fields, fmt.Sprintf("args[%d]", i))
	}
	si := shellScriptIndex(values(argv))
	cc.shellNote = ""
	if si < 0 && len(c.command) == 0 && len(c.args) >= 2 && isDashC(c.args[0].value) {
		si = 1
		cc.shellNote = "no command is set and args start with " + c.args[0].value + ", so this reads args[1] as a shell script for the image's entrypoint"
	}
	for i, s := range argv {
		ctx := ctxPlain
		if i == si {
			ctx = ctxShell
		}
		cc.check(s, ctx, fields[i], lookupFinal)
	}

	// exec probes see only env names, with their values as written.
	lookupStatic := func(n string) (string, bool) {
		if _, ok := cc.static[n]; ok {
			return "env", true
		}
		return "", false
	}
	for _, probe := range []string{"startupProbe", "readinessProbe", "livenessProbe"} {
		cmd, err := execCommand(c.node, probe)
		if err != nil || cmd == nil {
			continue
		}
		si := shellScriptIndex(values(cmd))
		for i, s := range cmd {
			ctx := ctxProbe
			if i == si {
				ctx = ctxProbeShell
			}
			cc.check(s, ctx, fmt.Sprintf("%s exec command[%d]", probe, i), lookupStatic)
		}
	}
	for _, hook := range []string{"postStart", "preStop"} {
		cmd, err := execCommand(get(c.node, "lifecycle"), hook)
		if err != nil || cmd == nil {
			continue
		}
		si := shellScriptIndex(values(cmd))
		for i, s := range cmd {
			ctx := ctxHook
			if i == si {
				ctx = ctxHookShell
			}
			cc.check(s, ctx, fmt.Sprintf("lifecycle.%s exec command[%d]", hook, i), func(string) (string, bool) { return "", false })
		}
	}
	for i, vm := range seq(get(c.node, "volumeMounts")) {
		if s, ok := scalar(get(vm, "subPathExpr")); ok && s != "" {
			cc.check(stringAt{value: s, node: get(vm, "subPathExpr")}, ctxSubPath, fmt.Sprintf("volumeMounts[%d].subPathExpr", i), lookupFinal)
		}
	}
}

func execCommand(parent *Node, key string) ([]stringAt, error) {
	cmd := get(get(get(parent, key), "exec"), "command")
	if cmd == nil {
		return nil, nil
	}
	var out []stringAt
	for _, it := range seq(cmd) {
		if it.Kind == ScalarNode {
			out = append(out, stringAt{value: it.Value, node: it})
		}
	}
	return out, nil
}

func isDashC(s string) bool {
	return strings.HasPrefix(s, "-") && !strings.HasPrefix(s, "--") && strings.Contains(s, "c")
}

func values(list []stringAt) []string {
	var out []string
	for _, s := range list {
		out = append(out, s.value)
	}
	return out
}

func (cc *containerCheck) add(f finding) {
	o := cc.p.obj
	f.File = o.file
	f.Object = o.kind + " " + o.name
	f.Namespace = o.namespace
	f.Container = cc.c.name
	if cc.c.role != "container" {
		f.Container = cc.c.name + " (" + cc.c.role + ")"
	}
	cc.findings = append(cc.findings, f)
}

func (cc *containerCheck) check(s stringAt, ctx context, field string, lookup func(string) (string, bool)) {
	toks := scanDollars(s.value)
	if len(toks) == 0 {
		return
	}
	cc.st.strings++
	for _, t := range toks {
		cc.st.references++
		base := finding{Line: s.node.Line, Field: field, Value: s.value, Reference: t.text}
		if strings.Contains(s.value, "\n") {
			// Show the line of a multi-line script that has the token, and
			// point at it when the YAML keeps lines as written.
			before := s.value[:t.offset]
			n := strings.Count(before, "\n")
			start := strings.LastIndexByte(before, '\n') + 1
			end := strings.IndexByte(s.value[t.offset:], '\n')
			if end < 0 {
				end = len(s.value)
			} else {
				end += t.offset
			}
			base.Value = strings.TrimSpace(s.value[start:end])
			if s.node.Style&LiteralStyle != 0 {
				base.Line = s.node.Line + 1 + n
			}
		}
		switch t.kind {
		case tokRef:
			cc.checkRef(base, t, ctx, lookup)
		case tokBrace, tokBare:
			if ctx.shell() {
				continue
			}
			cc.shellSyntax(base, t, ctx)
		case tokUnclosed:
			if ctx.shell() {
				continue
			}
			base.Severity, base.Code = "WARN", "unclosed"
			base.Message = "$( has no closing ), so Kubernetes leaves it as written"
			base.Fix = "close the reference, as in $(NAME), or write $$( for a literal $("
			cc.add(base)
		}
	}
}

func (cc *containerCheck) shellSyntax(f finding, t dollarToken, ctx context) {
	f.Severity, f.Code = "WARN", "shell-syntax"
	where := "this command runs no shell"
	switch ctx {
	case ctxEnvValue:
		where = "env values aren't read by a shell"
	case ctxSubPath:
		where = "subPathExpr isn't read by a shell"
	case ctxHook:
		where = "this hook runs no shell"
	case ctxProbe:
		where = "this probe runs no shell"
	}
	if ctx == ctxPlain && len(cc.c.command) == 0 {
		where = "no command is set, so these args go to the image's entrypoint, which unexpanded can't see"
	}
	f.Message = fmt.Sprintf("Kubernetes expands only the $(NAME) form, and %s, so the program receives %s as written unless it expands it itself", where, t.text)
	declared, laterAt := cc.declared(t.name)
	switch {
	case ctx == ctxHook:
		f.Fix = fmt.Sprintf("hooks expand nothing: run the command through sh -c and keep %s there", t.text)
	case declared && ctx == ctxProbe && cc.static[t.name] != nil && cc.static[t.name].source != "":
		f.Fix = fmt.Sprintf("exec probes expand $(%s) to an empty string, because it comes from valueFrom: run the probe through sh -c and keep %s there", t.name, t.text)
	case declared:
		f.Fix = fmt.Sprintf("%s is declared in this container's env, so write $(%s)", t.name, t.name)
	case laterAt != "":
		f.Fix = fmt.Sprintf("%s is declared later in env; move it above %s and write $(%s)", t.name, strings.TrimPrefix(f.Field, "env "), t.name)
	default:
		f.Hints = cc.whyMissing(t.name)
		f.Fix = fmt.Sprintf("declare %s in this container's env and write $(%s)", t.name, t.name)
		if ctx == ctxPlain {
			f.Fix += ", or run the command through sh -c"
		}
	}
	cc.add(f)
}

// declared reports whether name is available at this point: for an env
// value, declared above it; otherwise anywhere in the container's env. It
// also returns a note when the name is only declared further down.
func (cc *containerCheck) declared(name string) (bool, string) {
	if cc.envIndex < 0 {
		_, ok := cc.final[name]
		return ok, ""
	}
	if _, ok := cc.defined[name]; ok {
		return true, ""
	}
	if _, ok := cc.svc[name]; ok {
		return true, ""
	}
	for _, ev := range cc.c.env[cc.envIndex+1:] {
		if ev.name == name {
			return false, "later"
		}
	}
	return false, ""
}

var strongServiceVar = regexp.MustCompile(`^([A-Z0-9_]+?)_(SERVICE_HOST|SERVICE_PORT(_[A-Z0-9_]+)?|PORT_[0-9]+_(TCP|UDP|SCTP)(_PROTO|_PORT|_ADDR)?)$`)

func (cc *containerCheck) checkRef(f finding, t dollarToken, ctx context, lookup func(string) (string, bool)) {
	name := t.name
	if src, ok := lookup(name); ok {
		if ctx == ctxProbe || ctx == ctxProbeShell {
			ev := cc.static[name]
			switch {
			case ev != nil && ev.source != "":
				f.Severity, f.Code = "ERROR", "probe-static"
				f.Message = fmt.Sprintf("exec probes expand $(%s) from the env list as written, without resolving valueFrom, and %s comes from a %s, so $(%s) becomes an empty string", name, name, ev.source, name)
				f.Fix = fmt.Sprintf("run the probe through sh -c and write ${%s}, which the shell reads from the container's environment", name)
				cc.add(f)
			case ev != nil && ev.value != nil && len(scanDollars(ev.value.value)) > 0:
				f.Severity, f.Code = "ERROR", "probe-static"
				f.Message = fmt.Sprintf("exec probes expand $(%s) to its value as written, %q, without expanding the references inside it", name, ev.value.value)
				f.Fix = fmt.Sprintf("run the probe through sh -c and write ${%s}, which the shell reads from the container's environment", name)
				cc.add(f)
			}
		}
		_ = src
		return
	}
	// Not resolved. In a shell script, $(lower case words) is command
	// substitution, which is fine.
	if ctx.shell() && !isEnvStyle(name) {
		return
	}
	if (ctx == ctxPlain || ctx == ctxProbe || ctx == ctxHook) && !isValidEnvName(name) {
		f.Severity, f.Code = "ERROR", "not-a-shell"
		f.Message = fmt.Sprintf("this command runs no shell, so %s isn't command substitution: the program receives it as written", t.text)
		f.Fix = "run the command through sh -c if it needs a shell"
		cc.add(f)
		return
	}
	f.Severity, f.Code = "ERROR", "undefined"
	switch ctx {
	case ctxHook, ctxHookShell:
		f.Code = "hook-literal"
		if ctx == ctxHookShell {
			f.Message = fmt.Sprintf("Kubernetes doesn't expand $(NAME) in lifecycle hooks, so the shell gets %s and runs %s as a command", t.text, name)
			f.Fix = fmt.Sprintf("write ${%s}, which the shell reads from the container's environment", name)
		} else {
			f.Message = fmt.Sprintf("Kubernetes doesn't expand $(NAME) in lifecycle hooks, so the command receives %s as written", t.text)
			f.Fix = fmt.Sprintf("run the hook through sh -c and write ${%s}", name)
		}
		cc.add(f)
		return
	case ctxProbe, ctxProbeShell:
		if src, ok := cc.final[name]; ok {
			f.Code = "probe-static"
			f.Message = fmt.Sprintf("exec probes expand only variables listed in env, and %s comes from %s, so the probe gets %s as written", name, src, t.text)
			if ctx == ctxProbeShell {
				f.Message += fmt.Sprintf(" and the shell runs %s as a command", name)
			}
			f.Fix = fmt.Sprintf("write ${%s} in a sh -c probe, which the shell reads from the container's environment", name)
			cc.add(f)
			return
		}
	}
	// A name declared later in env, for an env value.
	if ctx == ctxEnvValue {
		if _, later := cc.declared(name); later != "" {
			f.Code = "defined-later"
			f.Message = fmt.Sprintf("%s is declared further down in env, and kubelet expands env values in order, so this value keeps %s as written", name, t.text)
			f.Fix = fmt.Sprintf("move %s above %s in env", name, strings.TrimPrefix(f.Field, "env "))
			cc.add(f)
			return
		}
	}
	f.Hints = cc.whyMissing(name)
	ns := cc.p.obj.namespace
	s := cc.ix.serviceFor(name, ns)
	switch {
	case s == nil && cc.p.serviceLinks && strongServiceVar.MatchString(name):
		f.Severity, f.Code = "WARN", "service-link"
		svc := strings.ToLower(strings.ReplaceAll(strongServiceVar.FindStringSubmatch(name)[1], "_", "-"))
		f.Message = fmt.Sprintf("%s is set by kubelet only if a Service named %s, with a cluster IP, exists in the pod's namespace before the pod starts, and the inputs don't have it. Otherwise %s stays as written", name, svc, t.text)
		f.Fix = fmt.Sprintf("pass the Service's manifest too, or use its DNS name, %s, instead of the variable", svc)
		cc.add(f)
		return
	case len(cc.unknown) > 0:
		f.Severity, f.Code = "WARN", "unknown-source"
		f.Message = fmt.Sprintf("%s isn't declared in env, and may come from %s through envFrom, which the inputs don't have. If it doesn't, %s stays as written", name, strings.Join(cc.unknown, " or "), t.text)
		f.Fix = fmt.Sprintf("pass the %s manifest too, so its keys can be checked", strings.Join(cc.unknown, " and "))
		cc.add(f)
		return
	}
	switch ctx {
	case ctxEnvValue:
		f.Message = fmt.Sprintf("%s isn't declared in this container's env, so the value keeps %s as written", name, t.text)
	case ctxShell, ctxProbeShell:
		f.Message = fmt.Sprintf("%s isn't declared in this container's env, so the shell gets %s as written and runs %s as a command", name, t.text, name)
	case ctxSubPath:
		f.Message = fmt.Sprintf("%s isn't declared in this container's env, so the container won't start: kubelet fails with \"missing value for %s\"", name, name)
	case ctxProbe:
		f.Message = fmt.Sprintf("%s isn't declared in this container's env, so the probe gets %s as written", name, t.text)
	default:
		f.Message = fmt.Sprintf("%s isn't declared in this container's env, so Kubernetes passes %s as written", name, t.text)
	}
	if cc.shellNote != "" && ctx == ctxShell {
		f.Hints = append([]string{cc.shellNote}, f.Hints...)
	}
	switch {
	case s != nil && (s.headless || !cc.p.serviceLinks):
		f.Fix = fmt.Sprintf("use the Service's DNS name, %s, instead of the variable", s.obj.name)
	case name == "HOSTNAME":
		f.Fix = "for the pod name, declare HOSTNAME (or POD_NAME) with valueFrom.fieldRef.fieldPath: metadata.name"
	case ctx.shell():
		f.Fix = fmt.Sprintf("declare %s in env, or write ${%s} if the image sets it", name, name)
	default:
		f.Fix = fmt.Sprintf("declare %s in this container's env, with value, fieldRef, configMapKeyRef or secretKeyRef", name)
	}
	cc.add(f)
}

// whyMissing explains, where it can, why a name isn't defined.
func (cc *containerCheck) whyMissing(name string) []string {
	var hints []string
	if h, ok := cc.optMiss[name]; ok {
		hints = append(hints, h)
	}
	for _, other := range cc.p.containers {
		if other == cc.c {
			continue
		}
		for _, ev := range other.env {
			if ev.name == name {
				hints = append(hints, fmt.Sprintf("%s is declared in %s %s, but each container has its own env", name, other.role, other.name))
				break
			}
		}
	}
	if s := cc.ix.serviceFor(name, cc.p.obj.namespace); s != nil {
		switch {
		case !cc.p.serviceLinks:
			hints = append(hints, fmt.Sprintf("the pod sets enableServiceLinks: false, so kubelet adds no variables for Service %s", s.obj.name))
		case s.headless:
			hints = append(hints, fmt.Sprintf("Service %s is headless (or has no ports), and kubelet adds variables only for Services with a cluster IP", s.obj.name))
		}
	}
	if r, ok := runtimeVars[name]; ok {
		hints = append(hints, r+", and kubelet expands only variables declared in the pod spec")
	}
	if sim := cc.similar(name); len(sim) > 0 {
		hints = append(hints, "declared names that are close: "+strings.Join(sim, ", "))
	}
	return hints
}

func (cc *containerCheck) similar(name string) []string {
	var out []string
	for k := range cc.final {
		if strings.HasPrefix(k, "KUBERNETES_") && !strings.HasPrefix(name, "KUBERNETES") {
			continue
		}
		if k != name && (strings.EqualFold(k, name) || editDistance(strings.ToUpper(k), strings.ToUpper(name)) <= 2 && len(name) > 3) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

package main

import (
	"fmt"
	"strings"
)

// podTemplateKinds are the built-in kinds whose pods come from
// spec.template.spec. Other kinds with that path, such as Argo Rollouts or
// Knative Services, are read the same way when they have containers.
var podTemplateKinds = map[string]bool{
	"Deployment": true, "StatefulSet": true, "DaemonSet": true, "ReplicaSet": true,
	"ReplicationController": true, "Job": true,
}

// podSpecOf returns the pod spec of a workload object, or nil if the object
// has none.
func podSpecOf(o *object) *Node {
	spec := get(o.node, "spec")
	switch o.kind {
	case "Pod":
		return spec
	case "CronJob":
		return get(get(get(get(spec, "jobTemplate"), "spec"), "template"), "spec")
	case "PodTemplate":
		return get(get(o.node, "template"), "spec")
	}
	if ps := get(get(spec, "template"), "spec"); ps != nil {
		if podTemplateKinds[o.kind] || get(ps, "containers") != nil {
			return ps
		}
	}
	return nil
}

// isWorkloadKind reports whether a kind must have a pod spec.
func isWorkloadKind(kind string) bool {
	return podTemplateKinds[kind] || kind == "Pod" || kind == "CronJob" || kind == "PodTemplate"
}

// container is one container of a pod spec, with what the check needs.
type container struct {
	name    string
	role    string // "container", "init container" or "ephemeral container"
	node    *Node
	env     []envVar
	envFrom []envFromSource
	command []stringAt
	args    []stringAt
}

type stringAt struct {
	value string
	node  *Node
}

type envVar struct {
	name      string
	node      *Node // the list item
	value     *stringAt
	source    string // "", "fieldRef", "resourceFieldRef", "configMapKeyRef", "secretKeyRef" or "fileKeyRef"
	refName   string // ConfigMap or Secret name for key refs
	refKey    string
	optional  bool
	fieldPath string
}

type envFromSource struct {
	kind     string // "ConfigMap" or "Secret"
	name     string
	prefix   string
	optional bool
	node     *Node
}

// pod is a pod spec inside a workload object.
type pod struct {
	obj          *object
	spec         *Node
	containers   []*container
	serviceLinks bool
}

func readPod(o *object) (*pod, error) {
	spec := podSpecOf(o)
	if spec == nil {
		if isWorkloadKind(o.kind) {
			return nil, refuse("%s:%d: %s %s has no pod spec; is the file cut off?", o.file, o.node.Line, o.kind, o.name)
		}
		return nil, nil
	}
	if spec.Kind != MappingNode {
		return nil, refuse("%s:%d: %s %s: the pod spec is not a mapping", o.file, spec.Line, o.kind, o.name)
	}
	p := &pod{obj: o, spec: spec, serviceLinks: true}
	if v, ok := boolValue(get(spec, "enableServiceLinks")); ok {
		p.serviceLinks = v
	}
	cs := get(spec, "containers")
	if cs == nil || cs.Kind != SequenceNode || len(cs.Content) == 0 {
		return nil, refuse("%s:%d: %s %s has no containers; is the file cut off?", o.file, spec.Line, o.kind, o.name)
	}
	for _, list := range []struct{ key, role string }{
		{"initContainers", "init container"},
		{"containers", "container"},
		{"ephemeralContainers", "ephemeral container"},
	} {
		ln := get(spec, list.key)
		if ln == nil {
			continue
		}
		if ln.Kind != SequenceNode {
			return nil, refuse("%s:%d: %s %s: %s is not a list", o.file, ln.Line, o.kind, o.name, list.key)
		}
		for _, cn := range ln.Content {
			c, err := readContainer(o, cn, list.role)
			if err != nil {
				return nil, err
			}
			p.containers = append(p.containers, c)
		}
	}
	return p, nil
}

func readContainer(o *object, n *Node, role string) (*container, error) {
	where := func(m *Node) string { return fmt.Sprintf("%s:%d: %s %s", o.file, m.Line, o.kind, o.name) }
	if n.Kind != MappingNode {
		return nil, refuse("%s: a %s is not a mapping", where(n), role)
	}
	name, _ := scalar(get(n, "name"))
	if name == "" {
		return nil, refuse("%s: a %s has no name; is the file cut off?", where(n), role)
	}
	if img, _ := scalar(get(n, "image")); img == "" {
		return nil, refuse("%s: %s %s has no image; is the file cut off?", where(n), role, name)
	}
	c := &container{name: name, role: role, node: n}
	var err error
	if c.command, err = stringList(o, n, "command", role, name); err != nil {
		return nil, err
	}
	if c.args, err = stringList(o, n, "args", role, name); err != nil {
		return nil, err
	}
	if en := get(n, "env"); en != nil {
		if en.Kind != SequenceNode {
			return nil, refuse("%s: %s %s: env is not a list", where(en), role, name)
		}
		for _, e := range en.Content {
			ev, err := readEnvVar(o, e, role, name)
			if err != nil {
				return nil, err
			}
			c.env = append(c.env, ev)
		}
	}
	if ef := get(n, "envFrom"); ef != nil {
		if ef.Kind != SequenceNode {
			return nil, refuse("%s: %s %s: envFrom is not a list", where(ef), role, name)
		}
		for _, e := range ef.Content {
			prefix, _ := scalar(get(e, "prefix"))
			var src envFromSource
			switch {
			case get(e, "configMapRef") != nil:
				r := get(e, "configMapRef")
				nm, _ := scalar(get(r, "name"))
				opt, _ := boolValue(get(r, "optional"))
				src = envFromSource{kind: "ConfigMap", name: nm, optional: opt}
			case get(e, "secretRef") != nil:
				r := get(e, "secretRef")
				nm, _ := scalar(get(r, "name"))
				opt, _ := boolValue(get(r, "optional"))
				src = envFromSource{kind: "Secret", name: nm, optional: opt}
			default:
				return nil, refuse("%s: %s %s: an envFrom entry has neither configMapRef nor secretRef", where(e), role, name)
			}
			src.prefix = prefix
			src.node = e
			c.envFrom = append(c.envFrom, src)
		}
	}
	return c, nil
}

func stringList(o *object, n *Node, key, role, cname string) ([]stringAt, error) {
	ln := get(n, key)
	if ln == nil {
		return nil, nil
	}
	if ln.Kind == ScalarNode && ln.Tag == "!!null" {
		return nil, nil
	}
	if ln.Kind != SequenceNode {
		return nil, refuse("%s:%d: %s %s, %s %s: %s is not a list of strings", o.file, ln.Line, o.kind, o.name, role, cname, key)
	}
	var out []stringAt
	for _, it := range ln.Content {
		if it.Kind != ScalarNode {
			return nil, refuse("%s:%d: %s %s, %s %s: %s has an item that is not a string", o.file, it.Line, o.kind, o.name, role, cname, key)
		}
		out = append(out, stringAt{value: it.Value, node: it})
	}
	return out, nil
}

func readEnvVar(o *object, e *Node, role, cname string) (envVar, error) {
	where := fmt.Sprintf("%s:%d: %s %s, %s %s", o.file, e.Line, o.kind, o.name, role, cname)
	if e.Kind != MappingNode {
		return envVar{}, refuse("%s: an env entry is not a mapping", where)
	}
	name, _ := scalar(get(e, "name"))
	if name == "" {
		return envVar{}, refuse("%s: an env entry has no name; is the file cut off?", where)
	}
	ev := envVar{name: name, node: e}
	if v := get(e, "value"); v != nil {
		if v.Kind != ScalarNode {
			return envVar{}, refuse("%s: env %s: value is not a string", where, name)
		}
		if v.Tag != "!!null" {
			ev.value = &stringAt{value: v.Value, node: v}
		}
	}
	vf := get(e, "valueFrom")
	if vf == nil {
		return ev, nil
	}
	if vf.Kind != MappingNode {
		return envVar{}, refuse("%s: env %s: valueFrom is not a mapping", where, name)
	}
	for _, src := range []string{"fieldRef", "resourceFieldRef", "configMapKeyRef", "secretKeyRef", "fileKeyRef"} {
		r := get(vf, src)
		if r == nil {
			continue
		}
		ev.source = src
		ev.refName, _ = scalar(get(r, "name"))
		ev.refKey, _ = scalar(get(r, "key"))
		ev.optional, _ = boolValue(get(r, "optional"))
		ev.fieldPath, _ = scalar(get(r, "fieldPath"))
		if ev.fieldPath == "" {
			ev.fieldPath, _ = scalar(get(r, "resource"))
		}
		return ev, nil
	}
	return envVar{}, refuse("%s: env %s: valueFrom has no source kubelet knows", where, name)
}

// keySet is the keys of a ConfigMap or Secret found in the inputs.
type keySet struct {
	obj  *object
	keys map[string]bool
}

type service struct {
	obj      *object
	headless bool
	ports    []servicePort
}

type servicePort struct {
	name     string
	port     string
	protocol string
}

// index holds the ConfigMaps, Secrets and Services in the inputs, which can
// define variables for the pods.
type index struct {
	configMaps map[string][]*keySet
	secrets    map[string][]*keySet
	services   map[string][]*service
}

func buildIndex(objs []*object) *index {
	ix := &index{configMaps: map[string][]*keySet{}, secrets: map[string][]*keySet{}, services: map[string][]*service{}}
	for _, o := range objs {
		switch o.kind {
		case "ConfigMap", "Secret":
			ks := &keySet{obj: o, keys: map[string]bool{}}
			for _, field := range []string{"data", "binaryData", "stringData"} {
				d := get(o.node, field)
				if d == nil || d.Kind != MappingNode {
					continue
				}
				for i := 0; i+1 < len(d.Content); i += 2 {
					ks.keys[d.Content[i].Value] = true
				}
			}
			if o.kind == "ConfigMap" {
				ix.configMaps[o.name] = append(ix.configMaps[o.name], ks)
			} else {
				ix.secrets[o.name] = append(ix.secrets[o.name], ks)
			}
		case "Service":
			spec := get(o.node, "spec")
			s := &service{obj: o}
			ip, _ := scalar(get(spec, "clusterIP"))
			typ, _ := scalar(get(spec, "type"))
			s.headless = ip == "None" || typ == "ExternalName"
			for _, p := range seq(get(spec, "ports")) {
				var sp servicePort
				sp.name, _ = scalar(get(p, "name"))
				sp.port, _ = scalar(get(p, "port"))
				sp.protocol, _ = scalar(get(p, "protocol"))
				if sp.protocol == "" {
					sp.protocol = "TCP"
				}
				s.ports = append(s.ports, sp)
			}
			if len(s.ports) == 0 {
				s.headless = true // a Service without ports gets no variables
			}
			ix.services[o.name] = append(ix.services[o.name], s)
		}
	}
	return ix
}

// sameNamespace matches objects that will land in the same namespace. An
// object without a namespace goes wherever it's applied, so it matches any.
func sameNamespace(a, b string) bool {
	return a == "" || b == "" || a == b
}

func (ix *index) keys(kind, name, ns string) *keySet {
	m := ix.configMaps
	if kind == "Secret" {
		m = ix.secrets
	}
	for _, ks := range m[name] {
		if sameNamespace(ks.obj.namespace, ns) {
			return ks
		}
	}
	return nil
}

// masterServiceVars are the variables of the kubernetes Service in the
// default namespace, which kubelet adds to every pod, even with
// enableServiceLinks: false.
var masterServiceVars = []string{
	"KUBERNETES_SERVICE_HOST", "KUBERNETES_SERVICE_PORT", "KUBERNETES_SERVICE_PORT_HTTPS",
	"KUBERNETES_PORT", "KUBERNETES_PORT_443_TCP", "KUBERNETES_PORT_443_TCP_PROTO",
	"KUBERNETES_PORT_443_TCP_PORT", "KUBERNETES_PORT_443_TCP_ADDR",
}

// serviceEnv returns the variables kubelet adds for the Services in the
// inputs that share the pod's namespace, following kubelet's envvars.go.
func (ix *index) serviceEnv(ns string, links bool) map[string]string {
	m := map[string]string{}
	for _, v := range masterServiceVars {
		m[v] = "service kubernetes"
	}
	if !links {
		return m
	}
	for name, list := range ix.services {
		for _, s := range list {
			if s.headless || !sameNamespace(s.obj.namespace, ns) {
				continue
			}
			from := "Service " + name
			p := serviceVarName(name)
			m[p+"_SERVICE_HOST"] = from
			m[p+"_SERVICE_PORT"] = from
			for i, sp := range s.ports {
				if sp.name != "" {
					m[p+"_SERVICE_PORT_"+serviceVarName(sp.name)] = from
				}
				if i == 0 {
					m[p+"_PORT"] = from
				}
				pp := p + "_PORT_" + sp.port + "_" + strings.ToUpper(sp.protocol)
				m[pp] = from
				m[pp+"_PROTO"] = from
				m[pp+"_PORT"] = from
				m[pp+"_ADDR"] = from
			}
		}
	}
	return m
}

func serviceVarName(s string) string {
	return strings.ToUpper(strings.ReplaceAll(s, "-", "_"))
}

// headlessService returns a Service in the inputs whose variables would be
// name, if it gets none because it's headless.
func (ix *index) serviceFor(varName, ns string) *service {
	for name, list := range ix.services {
		p := serviceVarName(name) + "_"
		if !strings.HasPrefix(varName, p) {
			continue
		}
		for _, s := range list {
			if sameNamespace(s.obj.namespace, ns) {
				return s
			}
		}
	}
	return nil
}

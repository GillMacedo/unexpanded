package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// refsByKubernetes returns the names that kubelet's own expansion code looks
// up in s, in order.
func refsByKubernetes(s string) []string {
	var names []string
	k8sExpand(s, func(n string) string { names = append(names, n); return "" })
	return names
}

func refsByScan(s string) []string {
	var names []string
	for _, t := range scanDollars(s) {
		if t.kind == tokRef {
			names = append(names, t.name)
		}
	}
	return names
}

func TestScanMatchesKubernetes(t *testing.T) {
	cases := []string{
		"", "$", "$$", "$$$", "$(A)", "$$(A)", "$$$(A)", "$(A)$(B)", "x$(A)y", "$(A", "$(A$(B)",
		"$()", "${A}", "${A:-$(B)}", "$A$(B)", "$(A)$", "$(a b)", "$(echo $(A))", "$(A))", "(($(A)))",
		"--x=$(POD_IP):8080", "$(é)", "€$(A)", "$é(A)", "a$$b$(C)$$(D)$(E",
	}
	// Every string in the examples and test data, too.
	for _, dir := range []string{"../../examples", "../../tests/testdata"} {
		filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				data, _ := os.ReadFile(p)
				cases = append(cases, strings.Split(string(data), "\n")...)
			}
			return nil
		})
	}
	for _, c := range cases {
		if got, want := refsByScan(c), refsByKubernetes(c); !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("%q: scan found %q, Kubernetes looks up %q", c, got, want)
		}
	}
}

func TestScanShellForms(t *testing.T) {
	toks := scanDollars("a ${HOME} $PATH $lower ${x:-y} $1 $$X $(A")
	var got []string
	for _, tk := range toks {
		got = append(got, tk.text)
	}
	want := []string{"${HOME}", "$PATH", "${x:-y}", "$(A"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestShellScriptIndex(t *testing.T) {
	cases := []struct {
		argv []string
		want int
	}{
		{[]string{"sh", "-c", "x"}, 2},
		{[]string{"/bin/bash", "-ec", "x"}, 2},
		{[]string{"bash", "-o", "pipefail", "-c", "x"}, 4},
		{[]string{"/usr/bin/env", "bash", "-c", "x"}, 3},
		{[]string{"busybox", "sh", "-c", "x"}, 3},
		{[]string{"tini", "--", "sh", "-c", "x"}, 4},
		{[]string{"/bin/sh", "-c", "--", "x"}, 3},
		{[]string{"sh", "/script.sh", "-c"}, -1},
		{[]string{"sh"}, -1},
		{[]string{"/api", "-c", "x"}, -1},
		{[]string{"python", "-c", "x"}, -1},
		{nil, -1},
	}
	for _, c := range cases {
		if got := shellScriptIndex(c.argv); got != c.want {
			t.Errorf("%q: got %d, want %d", c.argv, got, c.want)
		}
	}
}

// pod wraps containers YAML (indented for a pod spec) into a Deployment.
func deployment(containers string) string {
	return `apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
spec:
  template:
    spec:
` + containers
}

func runOn(t *testing.T, docs ...string) (string, string, int) {
	t.Helper()
	dir := t.TempDir()
	var args []string
	for i, d := range docs {
		p := filepath.Join(dir, "in"+string(rune('a'+i))+".yaml")
		if err := os.WriteFile(p, []byte(d), 0o644); err != nil {
			t.Fatal(err)
		}
		args = append(args, p)
	}
	var out, errb bytes.Buffer
	code := run(append([]string{"--format", "json"}, args...), strings.NewReader(""), &out, &errb)
	return out.String(), errb.String(), code
}

func codes(t *testing.T, docs ...string) []string {
	t.Helper()
	out, errs, code := runOn(t, docs...)
	if code == 2 {
		t.Fatalf("refused: %s", errs)
	}
	var got []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `"code": `) {
			got = append(got, strings.Trim(strings.TrimPrefix(line, `"code": `), `",`))
		}
	}
	if (len(got) > 0) != (code == 1) {
		t.Errorf("exit %d with findings %q", code, got)
	}
	return got
}

func TestRules(t *testing.T) {
	cases := []struct {
		name, containers string
		extra            []string
		want             []string
	}{
		{"declared fieldRef in args is quiet", `
      containers:
        - name: a
          image: x
          env:
            - name: POD_IP
              valueFrom: {fieldRef: {fieldPath: status.podIP}}
          args: ["--ip=$(POD_IP)"]
`, nil, nil},
		{"undeclared in args", `
      containers:
        - name: a
          image: x
          args: ["--ip=$(POD_IP)"]
`, nil, []string{"undefined"}},
		{"escaped $$( is quiet", `
      containers:
        - name: a
          image: x
          args: ["--literal=$$(POD_IP)"]
`, nil, nil},
		{"env order: later is reported, earlier is quiet", `
      containers:
        - name: a
          image: x
          env:
            - {name: A, value: "1"}
            - {name: B, value: "$(A)-$(C)"}
            - {name: C, value: "3"}
`, nil, []string{"defined-later"}},
		{"self reference such as PATH", `
      containers:
        - name: a
          image: x
          env:
            - {name: PATH, value: "/opt/bin:$(PATH)"}
`, nil, []string{"undefined"}},
		{"args see env declared anywhere", `
      containers:
        - name: a
          image: x
          args: ["$(LATE)"]
          env:
            - {name: LATE, value: "1"}
`, nil, nil},
		{"brace form in args without a shell", `
      containers:
        - name: a
          image: x
          command: ["/app"]
          args: ["--port=${PORT}"]
          env:
            - {name: PORT, value: "8080"}
`, nil, []string{"shell-syntax"}},
		{"brace and bare forms in sh -c are quiet", `
      containers:
        - name: a
          image: x
          command: ["sh", "-c", "exec /app --port=${PORT} --host=$HOSTNAME --id=$(hostname -i)"]
`, nil, nil},
		{"upper-case $(X) in sh -c that kubelet can't resolve", `
      containers:
        - name: a
          image: x
          command: ["sh", "-c", "exec /app --ip=$(POD_IP)"]
`, nil, []string{"undefined"}},
		{"shell script in args after a -c", `
      containers:
        - name: a
          image: x
          args: ["-c", "echo ${HOME}"]
`, nil, nil},
		{"other container's env", `
      containers:
        - name: a
          image: x
          env:
            - {name: TOKEN, value: "t"}
        - name: b
          image: x
          args: ["--token=$(TOKEN)"]
`, nil, []string{"undefined"}},
		{"envFrom ConfigMap in the inputs", `
      containers:
        - name: a
          image: x
          envFrom:
            - configMapRef: {name: cfg}
              prefix: APP_
          args: ["--mode=$(APP_MODE)", "--other=$(MODE)"]
`, []string{"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\ndata:\n  MODE: fast\n"}, []string{"undefined"}},
		{"envFrom Secret not in the inputs", `
      containers:
        - name: a
          image: x
          envFrom:
            - secretRef: {name: creds}
          args: ["--password=$(PASSWORD)"]
`, nil, []string{"unknown-source"}},
		{"service variables: master service and a Service with a cluster IP", `
      containers:
        - name: a
          image: x
          args: ["$(KUBERNETES_SERVICE_HOST)", "$(REDIS_SERVICE_HOST):$(REDIS_SERVICE_PORT_TCP_REDIS)", "$(REDIS_PORT_6379_TCP_ADDR)"]
`, []string{"apiVersion: v1\nkind: Service\nmetadata:\n  name: redis\nspec:\n  ports:\n    - name: tcp-redis\n      port: 6379\n"}, nil},
		{"service variable of a Service not in the inputs", `
      containers:
        - name: a
          image: x
          args: ["$(REDIS_SERVICE_HOST)"]
`, nil, []string{"service-link"}},
		{"service variable with enableServiceLinks false", `
      enableServiceLinks: false
      containers:
        - name: a
          image: x
          args: ["$(REDIS_SERVICE_HOST)"]
`, []string{"apiVersion: v1\nkind: Service\nmetadata:\n  name: redis\nspec:\n  ports:\n    - port: 6379\n"}, []string{"undefined"}},
		{"exec probe: valueFrom becomes empty, envFrom stays literal, plain value is fine", `
      containers:
        - name: a
          image: x
          env:
            - name: IP
              valueFrom: {fieldRef: {fieldPath: status.podIP}}
            - {name: PORT, value: "8080"}
          envFrom:
            - configMapRef: {name: cfg}
          readinessProbe:
            exec:
              command: ["/check", "$(IP)", "$(PORT)", "$(MODE)"]
`, []string{"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\ndata:\n  MODE: fast\n"}, []string{"probe-static", "probe-static"}},
		{"exec probe through sh with ${X} is quiet", `
      containers:
        - name: a
          image: x
          env:
            - name: IP
              valueFrom: {fieldRef: {fieldPath: status.podIP}}
          livenessProbe:
            exec:
              command: ["sh", "-c", "/check ${IP}"]
`, nil, nil},
		{"lifecycle hook, plain and shell", `
      containers:
        - name: a
          image: x
          env:
            - {name: X, value: "1"}
          lifecycle:
            postStart:
              exec:
                command: ["/hook", "$(X)"]
            preStop:
              exec:
                command: ["sh", "-c", "sleep 5; /hook $(X) $(date +%s) ${X}"]
`, nil, []string{"hook-literal", "hook-literal"}},
		{"subPathExpr", `
      containers:
        - name: a
          image: x
          env:
            - name: POD
              valueFrom: {fieldRef: {fieldPath: metadata.name}}
          volumeMounts:
            - {name: v, mountPath: /a, subPathExpr: "$(POD)"}
            - {name: v, mountPath: /b, subPathExpr: "$(NOPE)"}
`, nil, []string{"undefined"}},
		{"missing key in a ConfigMap in the inputs, and an optional one", `
      containers:
        - name: a
          image: x
          env:
            - name: A
              valueFrom: {configMapKeyRef: {name: cfg, key: nope}}
            - name: B
              valueFrom: {configMapKeyRef: {name: cfg, key: gone, optional: true}}
          args: ["$(B)"]
`, []string{"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cfg\ndata:\n  here: x\n"}, []string{"missing-key", "undefined"}},
		{"not a shell, command substitution", `
      containers:
        - name: a
          image: x
          command: ["/app"]
          args: ["--id=$(cat /etc/id)"]
`, nil, []string{"not-a-shell"}},
		{"unclosed reference", `
      containers:
        - name: a
          image: x
          args: ["--ip=$(POD_IP"]
`, nil, []string{"unclosed"}},
		{"init container and CronJob path", `
      initContainers:
        - name: i
          image: x
          args: ["$(NOPE)"]
      containers:
        - name: a
          image: x
`, nil, []string{"undefined"}},
		{"multi-line literal script", `
      containers:
        - name: a
          image: x
          command: ["/bin/bash", "-c"]
          args:
            - |
              set -e
              echo ${HOME} $(whoami)
              exec /app --ip=$(POD_IP)
`, nil, []string{"undefined"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			docs := append([]string{deployment(c.containers)}, c.extra...)
			got := codes(t, docs...)
			if !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestMultiLineScriptLine(t *testing.T) {
	doc := deployment(`
      containers:
        - name: a
          image: x
          command: ["/bin/bash", "-c"]
          args:
            - |
              set -e
              exec /app --ip=$(POD_IP)
`)
	out, _, _ := runOn(t, doc)
	if !strings.Contains(out, `"line": 16`) || !strings.Contains(out, `"value": "exec /app --ip=$(POD_IP)"`) {
		t.Errorf("want line 16 and the script line, got\n%s", out)
	}
}

func TestOtherKinds(t *testing.T) {
	cron := `apiVersion: batch/v1
kind: CronJob
metadata:
  name: c
spec:
  schedule: "* * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: a
              image: x
              args: ["$(NOPE)"]
`
	podDoc := `apiVersion: v1
kind: Pod
metadata:
  name: p
spec:
  containers:
    - name: a
      image: x
      args: ["$(NOPE)"]
`
	rollout := `apiVersion: argoproj.io/v1alpha1
kind: Rollout
metadata:
  name: r
spec:
  template:
    spec:
      containers:
        - name: a
          image: x
          args: ["$(NOPE)"]
`
	list := `{"apiVersion": "v1", "kind": "List", "items": [` + "\n" + `{"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "p"}, "spec": {"containers": [{"name": "a", "image": "x", "args": ["$(NOPE)"]}]}}]}` + "\n"
	for _, d := range []string{cron, podDoc, rollout, list} {
		if got := codes(t, d); !reflect.DeepEqual(got, []string{"undefined"}) {
			t.Errorf("got %q for\n%s", got, d)
		}
	}
}

// YAML merge keys: env and args that come from an anchor are checked.
func TestMergeKeys(t *testing.T) {
	doc := `apiVersion: v1
kind: Pod
metadata:
  name: p
spec:
  containers:
    - &base
      name: a
      image: x
      args: ["$(NOPE)"]
    - <<: *base
      name: b
`
	if got := codes(t, doc); !reflect.DeepEqual(got, []string{"undefined", "undefined"}) {
		t.Errorf("got %q", got)
	}
}

// A custom resource with containers at a path it doesn't read is reported,
// never skipped quietly. CRDs, whose schemas name containers, are not.
func TestNotChecked(t *testing.T) {
	cr := `apiVersion: serving.kserve.io/v1alpha1
kind: LLMInferenceService
metadata:
  name: m
spec:
  template:
    containers:
      - name: main
        env:
          - name: ARGS
            value: "--topic=kv@${POD_IP}"
`
	crd := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: things.example.com
spec:
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          properties:
            containers:
              type: array
`
	pod := "apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\nspec:\n  containers:\n    - name: a\n      image: x\n"
	if got := codes(t, cr, pod); !reflect.DeepEqual(got, []string{"not-checked"}) {
		t.Errorf("got %q", got)
	}
	if got := codes(t, crd, pod); len(got) != 0 {
		t.Errorf("CRD: got %q", got)
	}
	if _, errs, code := runOn(t, cr); code != 2 || !strings.Contains(errs, "no workloads") {
		t.Errorf("custom resource alone: exit %d, %s", code, errs)
	}
}

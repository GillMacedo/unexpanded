# unexpanded: find Kubernetes $(VAR) references that reach your program as written

[![CI](https://github.com/GillMacedo/unexpanded/actions/workflows/ci.yml/badge.svg)](https://github.com/GillMacedo/unexpanded/actions/workflows/ci.yml) [![MIT license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**A Claude Code skill and Go CLI for people who write Kubernetes manifests and Helm charts. It reads Deployments, StatefulSets, DaemonSets, Jobs, CronJobs and Pods, follows kubelet's rules for `$(VAR)` expansion in env, command, args, exec probes, lifecycle hooks and `subPathExpr`, and reports every reference that kubelet will leave as written.**

Kubernetes expands `$(VAR)` only from variables declared in the same container's `env` or `envFrom`, and in env values only from variables declared above. It never expands `${VAR}` or `$VAR`, because `command` and `args` don't run through a shell. Exec probes see env values as written, so a `valueFrom` variable becomes an empty string there, and lifecycle hooks expand nothing. When a reference doesn't resolve, the pod still starts, and the program gets the literal text `$(POD_IP)` or `${HOSTNAME}`. Nothing fails until something downstream breaks. Manifest linters such as kube-linter and kubeconform treat these strings as plain text, so they don't catch it. unexpanded needs only the files, so it runs in CI next to the charts.

## What you can ask Claude

- "My pod gets a literal $(POD_IP) in its arguments. Why isn't it expanded?"
- "Check this Helm chart's rendered manifests for env vars that won't expand."
- "Is the order of these env vars right for the ones that reference each other?"
- "Will this exec readiness probe see the pod IP?"
- "Why does my preStop hook run a command called POD_NAME?"

## Install

As a Claude Code plugin:

    claude plugin marketplace add GillMacedo/unexpanded
    claude plugin install unexpanded@unexpanded-marketplace

Or run it with Go 1.22 or newer. It has no dependencies outside this repository:

    helm template my-release ./chart > rendered.yaml
    go run github.com/GillMacedo/unexpanded/plugin/scripts@v0.1.0 rendered.yaml

## Example

`examples/arcadedb/statefulset-before.yaml` is ArcadeDB's Kubernetes example as it was before its [PR 6851](https://github.com/ArcadeData/arcadedb/pull/6851). The JVM received `-Darcadedb.server.name=${HOSTNAME}` and `-Darcadedb.server.rootPassword=${rootPassword}` as written, so every pod used the same peer name. `--fail-on never` keeps the exit code at 0 for this demo; without it, the run exits 1:

    $ go run ./plugin/scripts --fail-on never examples/arcadedb/statefulset-before.yaml
    WARN shell-syntax: StatefulSet arcadedb, container arcadedb, command[2]
      -Darcadedb.server.name=${HOSTNAME}
      Kubernetes expands only the $(NAME) form, and this command runs no shell, so the program receives ${HOSTNAME} as written unless it expands it itself
      the container runtime sets HOSTNAME, and kubelet expands only variables declared in the pod spec
      fix: declare HOSTNAME in this container's env and write $(HOSTNAME), or run the command through sh -c
      at examples/arcadedb/statefulset-before.yaml:108

    WARN shell-syntax: StatefulSet arcadedb, container arcadedb, command[3]
      -Darcadedb.server.rootPassword=${rootPassword}
      Kubernetes expands only the $(NAME) form, and this command runs no shell, so the program receives ${rootPassword} as written unless it expands it itself
      fix: rootPassword is declared in this container's env, so write $(rootPassword)
      at examples/arcadedb/statefulset-before.yaml:109

    0 errors and 2 warnings: checked 2 $ references in 1 container of 1 workload, from 1 file with 4 objects.

The file after the fix declares `POD_NAME` with a fieldRef and uses `$(POD_NAME)`. It stays quiet and exits 0:

    $ go run ./plugin/scripts examples/arcadedb/statefulset-after.yaml
    No problems: checked 1 $ reference in 1 container of 1 workload, from 1 file with 4 objects.

`examples/pitfalls/broken.yaml` was written for this README, with one container field for each way a reference goes wrong:

    $ go run ./plugin/scripts --fail-on never examples/pitfalls/broken.yaml | grep -E '^(ERROR|WARN)|^[0-9]'
    ERROR undefined: Deployment api, container wait-for-cache (init container), command[2]
    ERROR defined-later: Deployment api, container api, env DATABASE_URL
    WARN shell-syntax: Deployment api, container api, args[0]
    ERROR undefined: Deployment api, container api, args[1]
    ERROR probe-static: Deployment api, container api, readinessProbe exec command[2]
    ERROR hook-literal: Deployment api, container api, lifecycle.preStop exec command[2]
    ERROR undefined: Deployment api, container api, volumeMounts[0].subPathExpr
    6 errors and 1 warning: checked 7 $ references in 2 containers of 1 workload, from 1 file with 2 objects.

Two of those are easy to miss even when you know the env rules. The readiness probe declares `POD_IP` with a fieldRef, which works in `args`, and the preStop hook uses the same form:

    $ go run ./plugin/scripts --fail-on never examples/pitfalls/broken.yaml | sed -n '/probe-static/,/^$/p;/hook-literal/,/^$/p'
    ERROR probe-static: Deployment api, container api, readinessProbe exec command[2]
      --addr=$(POD_IP):8080
      exec probes expand $(POD_IP) from the env list as written, without resolving valueFrom, and POD_IP comes from a fieldRef, so $(POD_IP) becomes an empty string
      fix: run the probe through sh -c and write ${POD_IP}, which the shell reads from the container's environment
      at examples/pitfalls/broken.yaml:54

    ERROR hook-literal: Deployment api, container api, lifecycle.preStop exec command[2]
      /api drain --addr=$(POD_IP):8080
      Kubernetes doesn't expand $(NAME) in lifecycle hooks, so the shell gets $(POD_IP) and runs POD_IP as a command
      fix: write ${POD_IP}, which the shell reads from the container's environment
      at examples/pitfalls/broken.yaml:58

`examples/pitfalls/fixed.yaml` is the same app with each one fixed. The init container keeps `$(CACHE_SERVICE_HOST)`, which works there because the `cache` Service in the file now has a cluster IP, so kubelet adds its variables:

    $ go run ./plugin/scripts examples/pitfalls/fixed.yaml
    No problems: checked 7 $ references in 2 containers of 1 workload, from 1 file with 2 objects.

The exit code tells CI what happened:

    $ go build -o /tmp/unexpanded ./plugin/scripts && /tmp/unexpanded examples/pitfalls/broken.yaml > /dev/null; echo "exit $?"
    exit 1

Add `--format json` for machine-readable output, with the file, line, field, reference, message, hints and fix of each finding.

## What it reads

- **Manifests:** YAML or JSON files with one or more Kubernetes objects, `kind: List` output from `kubectl get -o yaml` or `-o json`, directories of `.yaml`, `.yml` and `.json` files, and `-` for stdin. Render Helm charts with `helm template` and kustomize overlays with `kubectl kustomize` first.
- **Workloads:** Pod, Deployment, StatefulSet, DaemonSet, ReplicaSet, ReplicationController, Job, CronJob and PodTemplate, plus any other kind with `spec.template.spec.containers`, such as an Argo Rollout. Init, regular and ephemeral containers are all checked.
- **Sources of variables:** each container's `env` in order, `envFrom` with its `prefix`, ConfigMaps and Secrets in the inputs (their `data`, `binaryData` and `stringData` keys), and the service link variables kubelet adds for Services in the inputs that have a cluster IP, such as `REDIS_SERVICE_HOST`. The `kubernetes` Service variables, such as `KUBERNETES_SERVICE_HOST`, always count as set.
- **Fields:** env values, `command`, `args`, exec commands of startup, readiness and liveness probes, postStart and preStop exec commands, and `volumeMounts[].subPathExpr`.

A `sh -c` script (also `bash`, `ash`, `dash`, `zsh`, `ksh`, through `/usr/bin/env`, `busybox` or `tini`) is read as shell: `${VAR}` and `$VAR` there are fine, and `$(lower case words)` is command substitution. An upper-case `$(VAR)` that kubelet can't resolve is reported, because the shell would then run `VAR` as a command. An object without a namespace is matched with objects in any namespace.

## What it reports

| Code | Severity | Meaning |
|---|---|---|
| `undefined` | ERROR | `$(X)` where X isn't declared in this container. Kubernetes passes it as written. In `subPathExpr` the container doesn't start. Hints say if X is declared in another container, set by the image or runtime (such as `HOSTNAME` or `PATH`), or a key missing from an optional ConfigMap reference |
| `defined-later` | ERROR | An env value uses a variable declared further down in `env` |
| `probe-static` | ERROR | An exec probe uses `$(X)` where X comes from `valueFrom` (it becomes an empty string), from a value that itself has references (they aren't expanded), or from `envFrom` (it stays literal) |
| `hook-literal` | ERROR | A postStart or preStop exec command uses `$(X)`, which Kubernetes never expands in hooks |
| `missing-key` | ERROR | A non-optional `configMapKeyRef` or `secretKeyRef` names a key that the ConfigMap or Secret in the inputs doesn't have, so the pod won't start |
| `not-a-shell` | ERROR | `$(cat file)` or similar in a command that runs no shell |
| `shell-syntax` | WARN | `${X}` or upper-case `$X` where no shell reads it. The program gets it as written unless it expands it itself, as Spring Boot does |
| `service-link` | WARN | A variable such as `X_SERVICE_HOST` for a Service that isn't in the inputs. It exists only if that Service has a cluster IP and existed before the pod started |
| `unknown-source` | WARN | X isn't declared, but the container has `envFrom` from a ConfigMap or Secret that isn't in the inputs, so it may come from there |
| `unclosed` | WARN | `$(` with no closing `)` |
| `not-checked` | WARN | A custom resource has containers at a path unexpanded doesn't read, such as KServe's `spec.template.containers`, so its references weren't checked |

`$$(X)` is Kubernetes' escape for a literal `$(X)` and is never reported.

## The Kubernetes rules it follows

These come from the kubelet source at commit `7d26464` (November 2026), and the same rules are in the v1.20.0, v1.26.0, v1.30.0 and v1.34.0 tags:

- `pkg/kubelet/kubelet_pods.go`, `makeEnvironmentVariables`: envFrom first, then each env value expanded with the variables declared so far and the service variables.
- `pkg/kubelet/container/helpers.go`, `ExpandContainerCommandAndArgs`: command and args expanded with the container's whole environment. `ExpandContainerCommandOnlyStatic`, used for exec probes in `pkg/kubelet/prober/prober.go`, maps each env name to its `value` as written. `ExpandContainerVolumeMounts` fails on a missing variable.
- `pkg/kubelet/lifecycle/handlers.go`: exec hooks run the command with no expansion.
- `pkg/kubelet/envvars/envvars.go`: the service link variable names.

`plugin/scripts/k8s_expand.go` is a copy of Kubernetes' expansion code (`third_party/forked/golang/expansion`), and its own tests run in `make test`, so `$$`, unclosed references and nested forms are read exactly as kubelet reads them.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Nothing at or above `--fail-on`, which is `warn` by default. Use `--fail-on error` to fail only on errors, or `never`. |
| 1 | Findings at or above `--fail-on`. |
| 2 | An input can't be trusted or the flags are wrong. Nothing was checked, and stdout is empty. |

## When it refuses

unexpanded exits 2 and prints only the reason when it can't trust what it read, whatever `--fail-on` says. For example, when `helm template` failed and its error went into the file:

    go run ./plugin/scripts tests/testdata/bad/helm-template-error.txt

prints this on stderr. The program exits 2 (under `go run`, Go adds a line `exit status 2` and itself exits 1):

    unexpanded: can't trust the input, nothing was checked: tests/testdata/bad/helm-template-error.txt: this looks like the output of a failed command, not manifests: "Error: execution error at (drust/templates/secret.yaml:12:21): secrets.adminPassword required (or set secrets.create=false)"

It does the same for an empty file, comments only, random text, binary data, the output of a failed `helm template`, `kubectl`, `kubectl kustomize`, `go build`, `go test` or `curl`, klog warning lines, an HTML error page, a Kubernetes API error (`kind: Status`), JSON of another kind, Helm values files, unrendered Helm templates, an empty `kind: List`, a missing file, invalid YAML, and a file cut off partway. A file must end with a line break, every object needs `apiVersion`, `kind` and `metadata.name`, a workload needs a pod spec with containers, and each container needs a name and an image. One bad file among good ones fails the whole run, and a run with no workloads is refused as having nothing to check.

A file cut exactly on a line break can still be a valid, shorter file, and no tool can tell it apart from a complete one. Many such cuts leave an object without its pod spec, containers or image and are refused; the rest are read as the shorter file. Check the counts in the summary line (containers, workloads and objects) against what you expect.

## Limits

- **The rendered pod spec is what counts.** Operators that build pods from custom resources may add or reorder env. Custom resources are read only when they have `spec.template.spec.containers`, and their findings assume the operator copies that spec as it is. Other custom resources with containers get a `not-checked` warning.
- **What the image does.** unexpanded doesn't read images. A `${VAR}` the program expands itself (Spring Boot placeholders, for example) is a warning you can accept with `--fail-on error`. An entrypoint script that runs `eval` on its arguments isn't seen.
- **Objects outside the inputs.** ConfigMaps, Secrets and Services that live only in the cluster can't be read. References that could come from them are warnings.
- **Values.** It checks that references resolve, not that the values are right.

## FAQ

**Why does my container get a literal $(POD_IP)?** The variable isn't declared in that container's `env`, or, in an env value, it's declared further down. Kubernetes leaves an unresolved `$(VAR)` as written. unexpanded names the reference and the reason.

**Does Kubernetes expand ${VAR} in command or args?** No. Only `$(VAR)`. `command` and `args` don't run through a shell, so `${VAR}` reaches the program unchanged unless you run `sh -c`.

**Does the order of env vars matter in Kubernetes?** Yes, for env values that reference other variables. kubelet expands each value with the variables declared before it, so `B: $(A)` needs `A` above it. `command` and `args` see all of them.

**Can an exec probe use $(POD_IP)?** Only if `POD_IP` has a literal `value`. kubelet expands exec probe commands from env values as written, so a fieldRef variable becomes an empty string. Use `sh -c` with `${POD_IP}` instead.

**Does it need a cluster?** No. It reads files: manifests, `helm template` output, or `kubectl get -o yaml` output you saved.

**Is it an MCP server?** No. It's a skill with one Go program, so `go run` or `go build` is all it needs.

## Tested on

- **Real bugs:** ArcadeDB's StatefulSet before PR 6851 reports exactly the two references that PR fixed, and the file after it is quiet. The drust Helm chart (KaelLim/drust, at commit `d0960f8`, rendered with `helm template`) reports the two `defined-later` errors described in its issue 1, where the reporter confirmed on a k3s cluster that `$(AK)` and `$(SK)` reached `mc` as written. The chart after its fix (commit `4ee939f`) is quiet. drust is AGPL-3.0, so its files aren't in this repository.
- **Real manifests that must stay quiet:** `tests/testdata/real` has 11 files with 34 workloads, 64 containers and 115 `$` references: ingress-nginx v1.12.1's install manifest, kube-prometheus v0.15.0's node-exporter DaemonSet, and `helm template` output with default values for the airflow, bitnami elasticsearch, etcd, kafka and rabbitmq, cilium, vault, jenkins and cert-manager charts. All are quiet. By hand, not in `make test`: 25 more release manifests (cert-manager, Argo CD, Prometheus Operator, MetalLB, Calico, Longhorn, KEDA, Kyverno, Tekton, Knative Serving and others) and 27 more charts (kube-prometheus-stack, Grafana, Mimir, Tempo, Consul, MinIO, Traefik, Kong and others) are also quiet.
- **Kubernetes' expansion code:** its own unit tests pass against the copy, and a test checks that the scanner finds the same references as that code for every line in the examples and test data.
- **Input it can't trust:** 24 bad files in `tests/testdata/bad` are each refused alone, next to good files and on stdin, and 21 more, mostly built from the example files, alone and next to a good file, under every `--fail-on` and both formats, with nothing on stdout. Other tests cut three files at every line break and every 7th byte: every mid-line cut is refused, and line cuts are refused or read as shorter files.
- **Go versions:** the tests pass with Go 1.22.12 and Go 1.27.3 on Linux arm64, offline. CI runs them on Linux amd64 with Go 1.22 and the current release.

Not tested: a live cluster (there is none in the build container), so the rules come from the kubelet source above and from the bug reports, not from running pods; macOS and Windows; Kubernetes versions before 1.20; container runtimes other than the ones those bug reports used; a fresh Claude Code session using the skill. The `go run ...@v0.1.0` line works only once the repository is published.

## Contributing

Contributions are welcome, from people and from people working with AI agents. See [CONTRIBUTING.md](CONTRIBUTING.md). Good first issues are [listed here](https://github.com/GillMacedo/unexpanded/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

### How pull requests work

1. **Open a PR.** Fork the repo, work on a branch, and keep it to one change. Write a title that says what changes for users, because it becomes the changelog line: `feat:` goes under Added and `fix:` under Fixed.
2. **Automatic check.** Code changes need tests, and `make test` must pass. Changes over 30 lines need a linked open issue (`Closes #<number>`), and a PR can have at most 800 changed lines. If something is missing, a comment tells you what to fix.
3. **Review.** Claude reviews each PR automatically. Code PRs also get a security review and are tested in a sandbox. The review then merges the PR or asks for changes, and a small problem may become a follow-up issue instead. A PR with no new commit 14 days after a change request is closed, and you can reopen it. PRs that change CI, agent settings, npm or Python dependencies, vendored code or build files can't be merged this way, and a comment says so. The same goes for anything the security review doesn't clear.
4. **Release.** A merge that changes what users install becomes a new version within about two hours, with a GitHub Release and, if the project has one, its npm package. Docs and CI changes wait for the next release.

## AI disclosure

This project was largely generated with Claude Code and reviewed by its maintainer.

## About me

I'm Giulliano Macedo, and I build developer tools at GoTech Solutions. Follow along or get in touch:

- GitHub: [GillMacedo](https://github.com/GillMacedo)
- LinkedIn: [giulliano](https://www.linkedin.com/in/giulliano/)
- X: [@giumacedo](https://x.com/giumacedo)

If `unexpanded` saved you some prompts, a star helps others find it.

---

Built by [Giulliano Macedo](https://github.com/GillMacedo), GoTech Solutions · [LinkedIn](https://www.linkedin.com/in/giulliano/) · [X](https://x.com/giumacedo)

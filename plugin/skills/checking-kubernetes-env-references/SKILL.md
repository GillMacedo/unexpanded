---
name: checking-kubernetes-env-references
description: Checks Kubernetes manifests (YAML or JSON, helm template or kustomize build output) for $(VAR) references that kubelet will leave as written, so the program gets the literal text. Finds variables declared later in env or not at all, ${VAR} and $VAR shell syntax in command and args without a shell, exec probes that expand valueFrom variables to empty strings, lifecycle hooks (which expand nothing), and subPathExpr that stops the container from starting. Use when a pod receives a literal $(POD_IP) or ${HOSTNAME}, after editing env, command, args or probes in a Deployment, StatefulSet, Job or Helm chart, or before merging manifest changes.
---

# Checking Kubernetes env references

unexpanded reads files only. It needs Go 1.22 or newer to build, and never connects to a cluster. Build it once:

    go build -o "${TMPDIR:-/tmp}/unexpanded" $(ls "${CLAUDE_PLUGIN_ROOT}"/scripts/*.go | grep -v _test.go)

## Get the manifests

It reads rendered Kubernetes objects, not Helm templates or kustomize overlays. Ask before running anything that touches a cluster. Any of these works:

- Plain manifests in the repository: pass the files or a directory.
- A Helm chart: `helm template RELEASE CHART -f values.yaml > rendered.yaml`. Check that helm exited 0 first.
- Kustomize: `kubectl kustomize DIR > rendered.yaml`.
- What runs now: `kubectl get deploy,sts,ds,job,cronjob -n NS -o yaml > live.yaml`.

Pass the ConfigMaps and Services too when they are in the repository. Their keys and names define variables through `envFrom` and service links; without them, references that could come from there are warnings, not errors.

## Run

    "${TMPDIR:-/tmp}/unexpanded" rendered.yaml

Exit 0 means no findings at or above `--fail-on` (default `warn`), 1 means findings, and 2 means an input couldn't be trusted and nothing was checked. On exit 2, show the user the reason and fix the input. Never report exit 2 as clean. Add `--format json` for machine-readable output.

## Read the findings

- `undefined` ERROR: `$(X)` where X isn't declared in this container's env. Kubernetes passes it as written; in a `sh -c` script the shell then runs X as a command. Check the hints: X may be declared in another container, set by the image or runtime (HOSTNAME, PATH), or a key missing from an optional ConfigMap reference.
- `defined-later` ERROR: an env value uses a variable declared further down. Move the declaration up.
- `shell-syntax` WARN: `${X}` or `$X` where no shell reads it. The program gets it as written unless it expands it itself (Spring Boot does, for example). Usually the fix is `$(X)`.
- `probe-static` ERROR: exec probes expand `$(X)` only from env values as written, so a valueFrom variable becomes an empty string and envFrom variables stay literal. Use `sh -c` and `${X}`.
- `hook-literal` ERROR: postStart and preStop exec commands aren't expanded at all. Use `sh -c` and `${X}`.
- `service-link` WARN: a `NAME_SERVICE_HOST` style variable for a Service that isn't in the inputs. It exists only if the Service has a cluster IP and existed before the pod started; suggest the DNS name instead.
- `unknown-source` WARN: X may come from an envFrom ConfigMap or Secret that isn't in the inputs. Ask for it, or check its keys.
- `missing-key` ERROR: a configMapKeyRef or secretKeyRef names a key that the ConfigMap or Secret in the inputs doesn't have; the pod won't start.
- `not-a-shell` ERROR and `unclosed` WARN: `$(cat file)` in a command with no shell, and `$(` without `)`.

## Report

1. Group findings by workload and container, and give the smallest fix for each: reorder env, declare the variable with a fieldRef, change `${X}` to `$(X)`, or wrap a probe or hook in `sh -c`.
2. Say what was read, from the summary line, and which ConfigMaps or Services were missing if there are warnings.

## Don't

Don't edit manifests or charts unless the user asks. Don't call a manifest correct because unexpanded is quiet: it checks `$` references, not what the values mean. Custom resources whose operator builds the pod (other than ones with `spec.template.spec.containers`) are skipped.

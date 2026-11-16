# 2. Refuse input it can't trust

Date: 2026-11-16

## Decision
unexpanded exits 2, with nothing on stdout, when any input can't be read completely: empty files, random text, binary data, the output of a failed `helm template`, `kubectl`, `kustomize`, `go build`, `go test` or `curl`, HTML pages, Kubernetes API errors, JSON or YAML that isn't a Kubernetes object, unrendered Helm templates, files that don't end with a line break, objects without a name, workloads without a pod spec or containers, containers without a name or image, and runs with no workloads. `--fail-on` doesn't change this.

## Why
A checker that prints "No problems" for a `helm template` error message, or a manifest cut in half, is worse than none. Exit 2 can't be mistaken for success in CI or by an agent.

## Limits
A file cut exactly on a line break can be a valid shorter file. Such a cut is refused when it leaves an object without its pod spec, containers or image; otherwise the shorter file is read. The summary prints how many files, objects, workloads and containers were read, so a short read is visible. A file that is complete but has no final line break is refused too, and the message says to add one.

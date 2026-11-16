# Changelog

All notable changes are documented here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. This project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-11-16

### Added
- Check Kubernetes manifests for `$(VAR)` references that kubelet leaves as written, following kubelet's own rules: `undefined`, `defined-later`, `probe-static`, `hook-literal`, `missing-key`, `not-a-shell`, `shell-syntax`, `service-link`, `unknown-source` and `unclosed`.
- Env values, command, args, exec probes, lifecycle hooks and `subPathExpr` in Pods, Deployments, StatefulSets, DaemonSets, ReplicaSets, ReplicationControllers, Jobs, CronJobs, PodTemplates and kinds with `spec.template.spec.containers`.
- `sh -c` scripts read as shell, so `${VAR}` and command substitution there stay quiet.
- Variables from `envFrom`, ConfigMaps and Secrets in the inputs, and service links for Services in the inputs.
- `--fail-on`, `--format json`, directories and stdin.
- Exit 2, with nothing checked, for input that can't be trusted: empty, garbage, cut off, unrendered Helm templates, or the output of a failed command.
- `checking-kubernetes-env-references` skill and plugin manifest.

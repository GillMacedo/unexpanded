# 3. Follow kubelet's code, not the docs

Date: 2026-11-16

## Decision
Each rule comes from a named function in the kubelet source, listed in the README, and the expansion itself is a copy of Kubernetes' `third_party/forked/golang/expansion`, with its own tests.

## Why
The Kubernetes docs describe `$(VAR)` for env, command and args, but not what exec probes and lifecycle hooks do. The source shows that exec probes go through `ExpandContainerCommandOnlyStatic`, which maps env names to their `value` as written, so `valueFrom` variables become empty strings, and that exec hooks aren't expanded at all. These rules were checked in v1.20.0, v1.26.0, v1.30.0, v1.34.0 and master in November 2026.

## Limits
No live cluster was available to build this, so behavior was taken from the source and from bug reports where people saw it on real clusters (ArcadeData/arcadedb PR 6851, KaelLim/drust issue 1).

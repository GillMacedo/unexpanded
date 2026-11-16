# 1. One Go program in a skill

Date: 2026-11-16

## Decision
unexpanded is a Claude Code skill with one Go program in `plugin/scripts`, using only the standard library, a copy of go.yaml.in/yaml/v3 renamed to `package main`, and a copy of Kubernetes' expansion code. The skill builds it with `go build` from the plugin directory, and people can `go run` it from the module.

## Why
The work is reading manifests and following fixed rules, which needs no running service, so an MCP server would add a process and a package without giving the agent anything a script can't. yaml.v3 gives line numbers for each value, so findings point at the line to fix. Copying it keeps `go build` working from the installed plugin, which has no `go.mod`, and offline.

## Alternatives
Importing `k8s.io/api` and `sigs.k8s.io/yaml` would decode objects into typed structs, but brings in a large dependency tree and loses line numbers. The checker needs only a few fields, which it reads from the YAML nodes.

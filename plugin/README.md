# unexpanded plugin

This directory is the Claude Code plugin for unexpanded, which finds `$(VAR)` references in Kubernetes manifests that kubelet will leave as written. `skills/checking-kubernetes-env-references/SKILL.md` is the skill, and `scripts/` is the Go program it builds. See the [repository README](https://github.com/GillMacedo/unexpanded#readme) for examples and limits.

Install it with:

    claude plugin marketplace add GillMacedo/unexpanded
    claude plugin install unexpanded@unexpanded-marketplace

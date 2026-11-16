# unexpanded

Claude Code skill and Go CLI that reads Kubernetes manifests and reports `$(VAR)` references that kubelet will leave as written: variables declared later in env or not at all, `${VAR}` in command and args without a shell, exec probes, lifecycle hooks and `subPathExpr`.

## Architecture
`plugin/scripts/` is one `package main` with no dependencies outside it. `unexpanded.go` reads flags and prints the report. `input.go` reads files into Kubernetes objects and refuses bad input. `k8s.go` finds pod specs, containers, env, ConfigMaps, Secrets and Services. `scan.go` finds `$` tokens and `sh -c` scripts. `check.go` applies kubelet's rules. `k8s_expand.go` is a copy of Kubernetes' own expansion code, and the `yaml_*.go` files are a copy of go.yaml.in/yaml/v3 renamed to `package main`. `plugin/skills/checking-kubernetes-env-references/SKILL.md` tells Claude how to use it. See `docs/architecture.md`.

## Commands
- `make test`: gofmt, vet, unit tests and the binary tests in `tests/`
- `make lint`: plugin manifest validation (needs Claude Code)
- `make demo`: run the examples

## Conventions
- Standard library only, plus the vendored copies. No network access, ever: unexpanded reads files.
- An input that can't be read completely exits 2 with nothing on stdout, under every `--fail-on`. Never skip a bad file silently. See `docs/decisions/0002-refuse-untrusted-input.md`.
- Each finding has a kebab-case code, a message that says what kubelet will do, and a fix line.
- Every rule cites the kubelet code it follows. Don't add one from memory.
- README examples are real output. `tests/readme_test.go` runs every `$ ` line and compares.

## Do and don't
- Do try a new rule on the real manifests in `tests/testdata/real`, and read what it reports, before adding it. They must stay quiet.
- Don't claim support for a kind, field or Kubernetes version without a test or a source.

## Definition of done
`make test` and `make lint` pass, and `README.md`, `ROADMAP.md` and the skill are updated when relevant.

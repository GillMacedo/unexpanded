# Architecture

    flags --> collectFiles (files, directories, stdin)
          --> parseFile: refuse empty, binary, failed command output, no final line break, bad YAML
                     --> objects (apiVersion, kind, metadata.name; List items)
          --> readPod: pod spec by kind --> containers with env, envFrom, command, args
          --> buildIndex: ConfigMap and Secret keys, Services with a cluster IP
    each container --> variables in kubelet's order:
                       envFrom keys --> env, one at a time (each value checked against what's declared so far)
                   --> command and args against all of them, with sh -c scripts read as shell
                   --> exec probes against env values as written
                   --> lifecycle exec hooks against nothing
                   --> subPathExpr against all of them
    report --> text or JSON, exit 0, 1 or 2

- **Tokens.** `scanDollars` reads a string the way Kubernetes' `Expand` does: `$$` is an escape, `$(NAME)` runs to the first `)`, and `$(` with no `)` is left alone. It also notes `${NAME}` and upper-case `$NAME`, which Kubernetes never expands. A unit test compares its references with what the copied `k8sExpand` looks up.
- **Shells.** `shellScriptIndex` finds the script of `sh -c` and similar, through `env`, `busybox` and init wrappers. In a script, shell forms are fine and only upper-case `$(NAME)` that kubelet can't resolve is reported, since the shell would run it as a command.
- **Sources.** ConfigMaps, Secrets and Services in the inputs define variables. Missing ones turn a would-be error into a `unknown-source` or `service-link` warning, so an incomplete input never reads as clean.
- **Refusal** is a separate error type. Any input that fails to parse, looks cut off, or is the output of a failed command ends the run with exit 2 before anything is printed on stdout.

Key choices are in `decisions/`.

# Roadmap

## Next
- Read `kustomization.yaml` directories directly, by running the same build as `kubectl kustomize`, so overlays don't need a separate step
- `--ignore NAME` for variables a program is known to expand itself, such as Spring Boot placeholders
- Custom resources whose pod spec is at another path, such as KServe's `spec.template.containers`, with a note that the operator builds the pod
- `$(VAR)` in `workingDir`, which kubelet doesn't expand
- A GitHub Actions example that renders a chart and runs unexpanded on pull requests

## Later
- Kubernetes' `fileKeyRef` env files, checking keys against a file in the repository
- Reading an image's config (from an OCI layout on disk) so `ENV` lines from the Dockerfile can explain `${VAR}` warnings

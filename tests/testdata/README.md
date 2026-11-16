# Test data

- `real/`: manifests that must stay quiet. Secret values were replaced with `cmVkYWN0ZWQ=`; nothing else was changed.
  - `ingress-nginx-controller-v1.12.1.yaml`: `deploy/static/provider/cloud/deploy.yaml` from kubernetes/ingress-nginx at tag controller-v1.12.1, Apache License 2.0.
  - `kube-prometheus-v0.15.0-nodeExporter-daemonset.yaml`: `manifests/nodeExporter-daemonset.yaml` from prometheus-operator/kube-prometheus v0.15.0, Apache License 2.0.
  - `helm-*.yaml`: `helm template rel CHART` with Helm 3.17.3 and default values, on 2026-11-16: apache-airflow/airflow 1.22.0 (Apache-2.0), bitnami elasticsearch 22.1.6, etcd 12.0.18, kafka 32.4.3 and rabbitmq 16.0.14 (Apache-2.0), cilium/cilium 1.20.4 (Apache-2.0), hashicorp/vault 0.34.1 (MPL-2.0), jenkins/jenkins 5.13.3 (Apache-2.0) and jetstack/cert-manager v1.21.3 (Apache-2.0).
- `bad/`: inputs that must be refused. `helm-template-error.txt`, `kubectl-no-server.txt`, `kustomize-error.txt`, `go-build-error.txt` and `go-test-fail.txt` are real output of failed commands, captured on 2026-11-16 (Helm 3.17.3 on a chart missing a required value, kubectl 1.34.2 with no cluster, `kubectl kustomize` on a missing directory, Go 1.27.3). The rest were written by hand: empty, whitespace and comment-only files, text, random bytes, an HTML 404 page, JSON of another kind, a Kubernetes API Status error, an empty `kind: List`, a Helm values file, an unrendered template, files cut off in several places, invalid YAML, and objects missing required fields.
- `only-services.yaml`: refused alone, accepted next to workloads.

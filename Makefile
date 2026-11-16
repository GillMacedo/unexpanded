.PHONY: test lint demo

test:
	test -z "$$(gofmt -l plugin tests)"
	go vet ./...
	go test -race ./...

lint:
	claude plugin validate ./plugin --strict
	claude plugin validate . --strict

# The broken examples exit 1 on purpose. The fixed ones and the real
# manifests must stay quiet.
demo:
	go run ./plugin/scripts examples/arcadedb/statefulset-before.yaml || test $$? -eq 1
	go run ./plugin/scripts examples/arcadedb/statefulset-after.yaml
	go run ./plugin/scripts --fail-on never examples/pitfalls/broken.yaml
	go run ./plugin/scripts examples/pitfalls/fixed.yaml
	go run ./plugin/scripts tests/testdata/real
